package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// MemberPurgeResult reports organization-scoped data deleted by an irreversible
// owner-only member purge. Audit events are intentionally retained.
type MemberPurgeResult struct {
	ActivityDeleted    int64 `json:"activity_deleted"`
	KeystrokesDeleted  int64 `json:"keystrokes_deleted"`
	BrowserDeleted     int64 `json:"browser_deleted"`
	ScreenshotsDeleted int64 `json:"screenshots_deleted"`
	EnrollmentsDeleted int64 `json:"enrollments_deleted"`
	BytesFreed         int64 `json:"bytes_freed"`
	AccountTombstoned  bool  `json:"account_tombstoned"`
}

func execDeleteCount(ctx context.Context, tx pgx.Tx, query string, args ...any) (int64, error) {
	ct, err := tx.Exec(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

// PurgeMemberFromBusiness irreversibly removes monitoring data and membership for
// one organization. The target membership row is held FOR UPDATE throughout the
// file + database deletion. Sync and screenshot ingestion take FOR SHARE on the
// same row, so no new organization data can race the purge.
//
// removeScreenshotTree must remove only the selected business/user subtree. It is
// called while the membership lock is held and before database screenshot rows are
// deleted. Missing files/directories should be treated as success so retries are
// safe after a partial infrastructure failure.
//
// Audit history is retained. If the user has no remaining organization access, the
// account becomes an inactive tombstone so audit foreign keys stay valid while the
// old email/username can be reused.
func (s *Store) PurgeMemberFromBusiness(
	ctx context.Context,
	actorID, businessID, targetUserID string,
	removeScreenshotTree func() error,
) (MemberPurgeResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return MemberPurgeResult{}, err
	}
	defer tx.Rollback(ctx)

	actorRole, err := membershipRoleTx(ctx, tx, actorID, businessID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return MemberPurgeResult{}, ErrForbidden
		}
		return MemberPurgeResult{}, err
	}
	if actorRole != RoleOwner {
		return MemberPurgeResult{}, ErrForbidden
	}
	if err := lockMutableOrganizationTx(ctx, tx, businessID); err != nil {
		return MemberPurgeResult{}, err
	}

	var targetRole BusinessRole
	err = tx.QueryRow(ctx,
		`SELECT role
		   FROM memberships
		  WHERE user_id = $1 AND business_id = $2
		  FOR UPDATE`,
		targetUserID, businessID,
	).Scan(&targetRole)
	if errors.Is(err, pgx.ErrNoRows) {
		return MemberPurgeResult{}, ErrNotFound
	}
	if err != nil {
		return MemberPurgeResult{}, err
	}
	if targetRole == RoleOwner {
		return MemberPurgeResult{}, ErrForbidden
	}

	var result MemberPurgeResult
	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(SUM(byte_size), 0)
		   FROM screenshots
		  WHERE business_id = $1 AND user_id = $2`,
		businessID, targetUserID,
	).Scan(&result.BytesFreed); err != nil {
		return MemberPurgeResult{}, err
	}

	if removeScreenshotTree != nil {
		if err := removeScreenshotTree(); err != nil {
			return MemberPurgeResult{}, err
		}
	}

	if result.EnrollmentsDeleted, err = execDeleteCount(ctx, tx,
		`DELETE FROM enrollment_tokens WHERE business_id = $1 AND user_id = $2`,
		businessID, targetUserID); err != nil {
		return MemberPurgeResult{}, err
	}
	if result.ScreenshotsDeleted, err = execDeleteCount(ctx, tx,
		`DELETE FROM screenshots WHERE business_id = $1 AND user_id = $2`,
		businessID, targetUserID); err != nil {
		return MemberPurgeResult{}, err
	}
	if result.ActivityDeleted, err = execDeleteCount(ctx, tx,
		`DELETE FROM activity_samples WHERE business_id = $1 AND user_id = $2`,
		businessID, targetUserID); err != nil {
		return MemberPurgeResult{}, err
	}
	if result.KeystrokesDeleted, err = execDeleteCount(ctx, tx,
		`DELETE FROM keystroke_buckets WHERE business_id = $1 AND user_id = $2`,
		businessID, targetUserID); err != nil {
		return MemberPurgeResult{}, err
	}
	if result.BrowserDeleted, err = execDeleteCount(ctx, tx,
		`DELETE FROM browser_visits WHERE business_id = $1 AND user_id = $2`,
		businessID, targetUserID); err != nil {
		return MemberPurgeResult{}, err
	}

	// Managed devices are organization-bound. Purging one organization removes
	// only that organization's device bindings and preserves installations that
	// belong to the same account in other organizations.
	if _, err := tx.Exec(ctx,
		`DELETE FROM devices WHERE business_id = $1 AND user_id = $2`,
		businessID, targetUserID); err != nil {
		return MemberPurgeResult{}, err
	}

	ct, err := tx.Exec(ctx,
		`DELETE FROM memberships WHERE business_id = $1 AND user_id = $2`,
		businessID, targetUserID)
	if err != nil {
		return MemberPurgeResult{}, err
	}
	if ct.RowsAffected() != 1 {
		return MemberPurgeResult{}, ErrNotFound
	}

	var hasOtherAccess bool
	err = tx.QueryRow(ctx, `
		SELECT EXISTS(SELECT 1 FROM memberships WHERE user_id = $1)
		    OR EXISTS(SELECT 1 FROM businesses WHERE owner_user_id = $1)`,
		targetUserID,
	).Scan(&hasOtherAccess)
	if err != nil {
		return MemberPurgeResult{}, err
	}

	if hasOtherAccess {
		// Invalidate sessions that may still carry the removed organization while
		// preserving the account and devices for its remaining organizations.
		if _, err := tx.Exec(ctx,
			`UPDATE users SET auth_version = auth_version + 1 WHERE id = $1`,
			targetUserID); err != nil {
			return MemberPurgeResult{}, err
		}
	} else {
		result.AccountTombstoned = true
		if _, err := tx.Exec(ctx, `
			UPDATE users
			   SET email = NULL,
			       username = 'deleted_' || replace(id::text, '-', ''),
			       display_name = 'Deleted user',
			       password_hash = 'deleted',
			       active = false,
			       disabled_at = COALESCE(disabled_at, now()),
			       auth_version = auth_version + 1
			 WHERE id = $1`, targetUserID); err != nil {
			return MemberPurgeResult{}, err
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM devices WHERE user_id = $1`,
			targetUserID); err != nil {
			return MemberPurgeResult{}, err
		}
	}

	if err := insertAuditTx(ctx, tx, businessID, actorID, "member.purged", "member", targetUserID, map[string]any{
		"role":                string(targetRole),
		"activity_deleted":    result.ActivityDeleted,
		"keystrokes_deleted":  result.KeystrokesDeleted,
		"browser_deleted":     result.BrowserDeleted,
		"screenshots_deleted": result.ScreenshotsDeleted,
		"bytes_freed":         result.BytesFreed,
		"account_tombstoned":  result.AccountTombstoned,
	}); err != nil {
		return MemberPurgeResult{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return MemberPurgeResult{}, err
	}
	return result, nil
}
