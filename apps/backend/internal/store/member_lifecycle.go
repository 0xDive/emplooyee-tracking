package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

const (
	MemberStatusActive  = "active"
	MemberStatusBlocked = "blocked"
	MemberStatusRemoved = "removed"
)

func canManageLifecycleTarget(actorRole, targetRole BusinessRole) bool {
	if targetRole == RoleOwner {
		return false
	}
	if actorRole == RoleAdmin && targetRole == RoleAdmin {
		return false
	}
	return actorRole == RoleOwner || actorRole == RoleAdmin
}

func (s *Store) setMemberStatus(
	ctx context.Context,
	actorID, businessID, targetUserID, nextStatus string,
	restoreMonitoring *bool,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	actorRole, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilityMembersManage,
	)
	if err != nil {
		return err
	}
	if err := lockMutableOrganizationTx(ctx, tx, businessID); err != nil {
		return err
	}

	var targetRole BusinessRole
	var currentStatus string
	var currentMonitoring bool
	err = tx.QueryRow(ctx, `
		SELECT role, status, monitoring_enabled
		  FROM memberships
		 WHERE user_id = $1 AND business_id = $2
		 FOR UPDATE`,
		targetUserID, businessID,
	).Scan(&targetRole, &currentStatus, &currentMonitoring)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if !canManageLifecycleTarget(actorRole, targetRole) {
		return ErrForbidden
	}

	switch nextStatus {
	case MemberStatusBlocked:
		if currentStatus == MemberStatusRemoved {
			return ErrConflict
		}
		if currentStatus == MemberStatusBlocked {
			return tx.Commit(ctx)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE memberships
			   SET status = 'blocked',
			       blocked_at = now(),
			       removed_at = NULL,
			       updated_at = now()
			 WHERE user_id = $1 AND business_id = $2`,
			targetUserID, businessID,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE enrollment_tokens
			   SET revoked_at = COALESCE(revoked_at, now())
			 WHERE user_id = $1 AND business_id = $2
			   AND used_at IS NULL AND revoked_at IS NULL`,
			targetUserID, businessID,
		); err != nil {
			return err
		}
		if err := insertAuditTx(ctx, tx, businessID, actorID, "member.blocked", "member", targetUserID, map[string]any{
			"role": string(targetRole),
		}); err != nil {
			return err
		}

	case MemberStatusActive:
		if currentStatus == MemberStatusRemoved {
			if restoreMonitoring == nil {
				return ErrConflict
			}
			if _, err := tx.Exec(ctx, `
				UPDATE memberships
				   SET status = 'active',
				       role = 'employee',
				       monitoring_enabled = $1,
				       blocked_at = NULL,
				       removed_at = NULL,
				       updated_at = now()
				 WHERE user_id = $2 AND business_id = $3`,
				*restoreMonitoring, targetUserID, businessID,
			); err != nil {
				return err
			}
			if err := insertAuditTx(ctx, tx, businessID, actorID, "member.restored", "member", targetUserID, map[string]any{
				"role":               "employee",
				"monitoring_enabled": *restoreMonitoring,
				"previous_role":      string(targetRole),
			}); err != nil {
				return err
			}
		} else if currentStatus == MemberStatusBlocked {
			if _, err := tx.Exec(ctx, `
				UPDATE memberships
				   SET status = 'active',
				       blocked_at = NULL,
				       updated_at = now()
				 WHERE user_id = $1 AND business_id = $2`,
				targetUserID, businessID,
			); err != nil {
				return err
			}
			if err := insertAuditTx(ctx, tx, businessID, actorID, "member.unblocked", "member", targetUserID, map[string]any{
				"role": string(targetRole),
			}); err != nil {
				return err
			}
		} else {
			return tx.Commit(ctx)
		}

	case MemberStatusRemoved:
		if currentStatus == MemberStatusRemoved {
			return tx.Commit(ctx)
		}
		if _, err := tx.Exec(ctx, `
			UPDATE memberships
			   SET status = 'removed',
			       monitoring_enabled = false,
			       blocked_at = NULL,
			       removed_at = now(),
			       updated_at = now()
			 WHERE user_id = $1 AND business_id = $2`,
			targetUserID, businessID,
		); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE enrollment_tokens
			   SET revoked_at = COALESCE(revoked_at, now())
			 WHERE user_id = $1 AND business_id = $2
			   AND used_at IS NULL AND revoked_at IS NULL`,
			targetUserID, businessID,
		); err != nil {
			return err
		}
		if err := insertAuditTx(ctx, tx, businessID, actorID, "member.removed", "member", targetUserID, map[string]any{
			"previous_role":               string(targetRole),
			"previous_monitoring_enabled": currentMonitoring,
		}); err != nil {
			return err
		}

	default:
		return ErrConflict
	}

	return tx.Commit(ctx)
}

func (s *Store) BlockMember(ctx context.Context, actorID, businessID, targetUserID string) error {
	return s.setMemberStatus(ctx, actorID, businessID, targetUserID, MemberStatusBlocked, nil)
}

func (s *Store) UnblockMember(ctx context.Context, actorID, businessID, targetUserID string) error {
	return s.setMemberStatus(ctx, actorID, businessID, targetUserID, MemberStatusActive, nil)
}

func (s *Store) RemoveMember(ctx context.Context, actorID, businessID, targetUserID string) error {
	return s.setMemberStatus(ctx, actorID, businessID, targetUserID, MemberStatusRemoved, nil)
}

func (s *Store) RestoreMember(
	ctx context.Context,
	actorID, businessID, targetUserID string,
	monitoringEnabled bool,
) error {
	return s.setMemberStatus(
		ctx, actorID, businessID, targetUserID, MemberStatusActive, &monitoringEnabled,
	)
}

func (s *Store) MembershipStatus(
	ctx context.Context,
	userID, businessID string,
) (string, error) {
	var status string
	err := s.pool.QueryRow(ctx, `
		SELECT status FROM memberships
		 WHERE user_id = $1 AND business_id = $2`,
		userID, businessID,
	).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return status, err
}


func (s *Store) ListFormerMembers(ctx context.Context, businessID string) ([]Employee, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, COALESCE(u.email, ''), COALESCE(u.username, ''), u.display_name, u.active,
		       m.role, m.status, m.monitoring_enabled, m.blocked_at, m.removed_at,
		       (SELECT extract(epoch FROM max(d.last_seen_at))::bigint
		          FROM devices d WHERE d.user_id = u.id AND (d.business_id = $1 OR d.business_id IS NULL)),
		       NULL::text AS current_app,
		       NULL::text AS current_window
		  FROM memberships m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.business_id = $1
		   AND m.status = 'removed'
		   AND m.role IN ('admin','manager','employee')
		 ORDER BY m.removed_at DESC NULLS LAST, u.display_name`,
		businessID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Employee{}
	for rows.Next() {
		var e Employee
		if err := rows.Scan(
			&e.ID, &e.Email, &e.Username, &e.DisplayName, &e.Active,
			&e.Role, &e.Status, &e.MonitoringEnabled, &e.BlockedAt, &e.RemovedAt,
			&e.LastSeen, &e.CurrentApp, &e.CurrentWindow,
		); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
