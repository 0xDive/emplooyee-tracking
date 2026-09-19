package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type OrganizationDeletionPreview struct {
	Members         int64 `json:"members"`
	Devices         int64 `json:"devices"`
	Activity        int64 `json:"activity"`
	Browser         int64 `json:"browser"`
	Keystrokes      int64 `json:"keystrokes"`
	Screenshots     int64 `json:"screenshots"`
	ScreenshotBytes int64 `json:"screenshot_bytes"`
	Exports         int64 `json:"exports"`
}

func (s *Store) ArchiveOrganization(
	ctx context.Context,
	actorID, businessID string,
) (Business, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Business{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilityOrganizationManage,
	); err != nil {
		return Business{}, err
	}
	current, err := getBusiness(ctx, tx, businessID)
	if err != nil {
		return Business{}, err
	}
	if current.DeletionScheduledAt != nil {
		return Business{}, ErrOrganizationDeletionPending
	}
	if current.ArchivedAt != nil {
		if err := tx.Commit(ctx); err != nil {
			return Business{}, err
		}
		return current, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE businesses
		   SET archived_at = now(), updated_at = now()
		 WHERE id = $1`, businessID); err != nil {
		return Business{}, err
	}
	if err := insertAuditTx(
		ctx, tx, businessID, actorID, "organization.archived",
		"organization", businessID, nil,
	); err != nil {
		return Business{}, err
	}
	updated, err := getBusiness(ctx, tx, businessID)
	if err != nil {
		return Business{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Business{}, err
	}
	return updated, nil
}

func (s *Store) RestoreOrganization(
	ctx context.Context,
	actorID, businessID string,
) (Business, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Business{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilityOrganizationManage,
	); err != nil {
		return Business{}, err
	}
	current, err := getBusiness(ctx, tx, businessID)
	if err != nil {
		return Business{}, err
	}
	if current.DeletionScheduledAt != nil {
		return Business{}, ErrOrganizationDeletionPending
	}
	if current.ArchivedAt == nil {
		if err := tx.Commit(ctx); err != nil {
			return Business{}, err
		}
		return current, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE businesses
		   SET archived_at = NULL, updated_at = now()
		 WHERE id = $1`, businessID); err != nil {
		return Business{}, err
	}
	if err := insertAuditTx(
		ctx, tx, businessID, actorID, "organization.restored",
		"organization", businessID, nil,
	); err != nil {
		return Business{}, err
	}
	updated, err := getBusiness(ctx, tx, businessID)
	if err != nil {
		return Business{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Business{}, err
	}
	return updated, nil
}

func revokeUserSecurityTx(
	ctx context.Context,
	tx pgx.Tx,
	userID string,
) error {
	if _, err := tx.Exec(ctx, `
		UPDATE users
		   SET auth_version = auth_version + 1
		 WHERE id = $1`, userID); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		   SET revoked_at = COALESCE(revoked_at, now())
		 WHERE user_id = $1
		   AND revoked_at IS NULL`, userID)
	return err
}

func (s *Store) TransferOrganizationOwnership(
	ctx context.Context,
	actorID, businessID, targetUserID string,
) error {
	if actorID == targetUserID {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	actorRole, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilityOrganizationTransfer,
	)
	if err != nil {
		return err
	}
	if actorRole != RoleOwner {
		return ErrForbidden
	}

	current, err := getBusiness(ctx, tx, businessID)
	if err != nil {
		return err
	}
	if current.DeletionScheduledAt != nil {
		return ErrOrganizationDeletionPending
	}
	if current.ArchivedAt != nil {
		return ErrOrganizationArchived
	}

	var targetRole BusinessRole
	var targetStatus string
	err = tx.QueryRow(ctx, `
		SELECT role, status
		  FROM memberships
		 WHERE business_id = $1 AND user_id = $2
		 FOR UPDATE`, businessID, targetUserID,
	).Scan(&targetRole, &targetStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if targetStatus != MemberStatusActive || targetRole != RoleAdmin {
		return ErrConflict
	}

	if _, err := tx.Exec(ctx, `
		UPDATE businesses
		   SET owner_user_id = $1, updated_at = now()
		 WHERE id = $2`, targetUserID, businessID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE memberships
		   SET role = CASE
		       WHEN user_id = $1 THEN 'owner'
		       WHEN user_id = $2 THEN 'admin'
		       ELSE role
		   END,
		       updated_at = now()
		 WHERE business_id = $3
		   AND user_id IN ($1, $2)`,
		targetUserID, actorID, businessID,
	); err != nil {
		return err
	}
	for _, userID := range []string{actorID, targetUserID} {
		if err := revokeUserSecurityTx(ctx, tx, userID); err != nil {
			return err
		}
		if err := insertSecurityEventTx(
			ctx, tx, userID, actorID, "security.ownership_transferred",
			map[string]any{"business_id": businessID},
		); err != nil {
			return err
		}
	}
	if err := insertAuditTx(
		ctx, tx, businessID, actorID, "organization.owner_transferred",
		"organization", businessID,
		map[string]any{
			"previous_owner_user_id": actorID,
			"new_owner_user_id":      targetUserID,
		},
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) OrganizationDeletionPreview(
	ctx context.Context,
	actorID, businessID string,
) (OrganizationDeletionPreview, error) {
	if err := s.BusinessPermissionOrForbidden(
		ctx, actorID, businessID, CapabilityOrganizationDelete,
	); err != nil {
		return OrganizationDeletionPreview{}, err
	}
	var out OrganizationDeletionPreview
	err := s.pool.QueryRow(ctx, `
		SELECT
		  (SELECT count(*) FROM memberships WHERE business_id = $1),
		  (SELECT count(*) FROM devices WHERE business_id = $1),
		  (SELECT count(*) FROM activity_samples WHERE business_id = $1),
		  (SELECT count(*) FROM browser_visits WHERE business_id = $1),
		  (SELECT count(*) FROM keystroke_buckets WHERE business_id = $1),
		  (SELECT count(*) FROM screenshots WHERE business_id = $1),
		  (SELECT COALESCE(sum(byte_size),0) FROM screenshots WHERE business_id = $1),
		  (SELECT count(*) FROM organization_exports WHERE business_id = $1)`,
		businessID,
	).Scan(
		&out.Members, &out.Devices, &out.Activity, &out.Browser,
		&out.Keystrokes, &out.Screenshots, &out.ScreenshotBytes, &out.Exports,
	)
	return out, err
}

func (s *Store) ScheduleOrganizationDeletion(
	ctx context.Context,
	actorID, businessID string,
) (Business, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Business{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilityOrganizationDelete,
	); err != nil {
		return Business{}, err
	}
	current, err := getBusiness(ctx, tx, businessID)
	if err != nil {
		return Business{}, err
	}
	if current.ArchivedAt == nil {
		return Business{}, ErrConflict
	}
	if current.DeletionScheduledAt != nil {
		if err := tx.Commit(ctx); err != nil {
			return Business{}, err
		}
		return current, nil
	}
	deadline := time.Now().UTC().Add(7 * 24 * time.Hour)
	if _, err := tx.Exec(ctx, `
		UPDATE businesses
		   SET deletion_scheduled_at = $1, updated_at = now()
		 WHERE id = $2`, deadline, businessID); err != nil {
		return Business{}, err
	}
	if err := insertAuditTx(
		ctx, tx, businessID, actorID, "organization.deletion_scheduled",
		"organization", businessID,
		map[string]any{"deletion_scheduled_at": deadline},
	); err != nil {
		return Business{}, err
	}
	updated, err := getBusiness(ctx, tx, businessID)
	if err != nil {
		return Business{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Business{}, err
	}
	return updated, nil
}

func (s *Store) CancelOrganizationDeletion(
	ctx context.Context,
	actorID, businessID string,
) (Business, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Business{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilityOrganizationDelete,
	); err != nil {
		return Business{}, err
	}
	current, err := getBusiness(ctx, tx, businessID)
	if err != nil {
		return Business{}, err
	}
	if current.DeletionScheduledAt == nil {
		if err := tx.Commit(ctx); err != nil {
			return Business{}, err
		}
		return current, nil
	}
	if _, err := tx.Exec(ctx, `
		UPDATE businesses
		   SET deletion_scheduled_at = NULL, updated_at = now()
		 WHERE id = $1`, businessID); err != nil {
		return Business{}, err
	}
	if err := insertAuditTx(
		ctx, tx, businessID, actorID, "organization.deletion_cancelled",
		"organization", businessID, nil,
	); err != nil {
		return Business{}, err
	}
	updated, err := getBusiness(ctx, tx, businessID)
	if err != nil {
		return Business{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Business{}, err
	}
	return updated, nil
}

func (s *Store) OrganizationsDueForDeletion(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text
		  FROM businesses
		 WHERE archived_at IS NOT NULL
		   AND deletion_scheduled_at IS NOT NULL
		   AND deletion_scheduled_at <= now()
		 ORDER BY deletion_scheduled_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) DeleteScheduledOrganization(
	ctx context.Context,
	businessID string,
	removeFiles func() error,
) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	var due bool
	err = tx.QueryRow(ctx, `
		SELECT archived_at IS NOT NULL
		       AND deletion_scheduled_at IS NOT NULL
		       AND deletion_scheduled_at <= now()
		  FROM businesses
		 WHERE id = $1
		 FOR UPDATE`, businessID,
	).Scan(&due)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	if !due {
		return 0, ErrConflict
	}

	rows, err := tx.Query(ctx, `
		SELECT user_id::text
		  FROM memberships
		 WHERE business_id = $1
		 FOR UPDATE`, businessID)
	if err != nil {
		return 0, err
	}
	var userIDs []string
	for rows.Next() {
		var userID string
		if err := rows.Scan(&userID); err != nil {
			rows.Close()
			return 0, err
		}
		userIDs = append(userIDs, userID)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()

	// Files are removed while the organization row lock is held. If storage
	// deletion fails, the database remains intact and the worker retries later.
	if removeFiles != nil {
		if err := removeFiles(); err != nil {
			return 0, err
		}
	}

	if _, err := tx.Exec(ctx, `DELETE FROM businesses WHERE id = $1`, businessID); err != nil {
		return 0, err
	}

	var deletedAccounts int64
	for _, userID := range userIDs {
		ct, err := tx.Exec(ctx, `
			DELETE FROM users u
			 WHERE u.id = $1
			   AND NOT EXISTS (
			       SELECT 1 FROM memberships m WHERE m.user_id = u.id
			   )`, userID)
		if err != nil {
			return 0, err
		}
		deletedAccounts += ct.RowsAffected()
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return deletedAccounts, nil
}
