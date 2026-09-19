package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

type SecurityEvent struct {
	ID          int64          `json:"id"`
	UserID      string         `json:"user_id"`
	ActorUserID string         `json:"actor_user_id"`
	Action      string         `json:"action"`
	Details     map[string]any `json:"details"`
	CreatedAt   int64          `json:"created_at"`
}

func insertSecurityEventTx(
	ctx context.Context,
	tx pgx.Tx,
	userID, actorUserID, action string,
	details map[string]any,
) error {
	if details == nil {
		details = map[string]any{}
	}
	raw, err := json.Marshal(details)
	if err != nil {
		return err
	}
	var actor any
	if actorUserID != "" {
		actor = actorUserID
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO security_events (user_id, actor_user_id, action, details)
		VALUES ($1, $2, $3, $4::jsonb)`,
		userID, actor, action, string(raw),
	)
	return err
}

func (s *Store) PasswordHashByUserID(ctx context.Context, userID string) (string, error) {
	var hash string
	err := s.pool.QueryRow(ctx, `SELECT password_hash FROM users WHERE id = $1`, userID).Scan(&hash)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return hash, err
}

func (s *Store) UpdateOwnDisplayName(ctx context.Context, userID, displayName string) (User, error) {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" || len([]rune(displayName)) > 120 {
		return User{}, ErrConflict
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)

	var mayEdit bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM businesses WHERE owner_user_id = $1)`,
		userID,
	).Scan(&mayEdit); err != nil {
		return User{}, err
	}
	if !mayEdit {
		return User{}, ErrForbidden
	}

	var oldName string
	if err := tx.QueryRow(ctx, `SELECT display_name FROM users WHERE id = $1 FOR UPDATE`, userID).Scan(&oldName); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	if oldName != displayName {
		if _, err := tx.Exec(ctx,
			`UPDATE users SET display_name = $1 WHERE id = $2`,
			displayName, userID,
		); err != nil {
			return User{}, err
		}
		if err := insertSecurityEventTx(ctx, tx, userID, userID, "account.profile_changed", map[string]any{
			"field": "display_name",
			"from":  oldName,
			"to":    displayName,
		}); err != nil {
			return User{}, err
		}
	}
	u, err := getUserByIDTx(ctx, tx, userID)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) UpdateOwnLoginIdentifiers(
	ctx context.Context,
	userID, email, username string,
) (User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	username = strings.ToLower(strings.TrimSpace(username))
	if email == "" && username == "" {
		return User{}, ErrConflict
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return User{}, err
	}
	defer tx.Rollback(ctx)

	var oldEmail, oldUsername string
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(email,''), COALESCE(username,'')
		  FROM users WHERE id = $1 FOR UPDATE`, userID,
	).Scan(&oldEmail, &oldUsername); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}

	if oldEmail != email || oldUsername != username {
		_, err = tx.Exec(ctx, `
			UPDATE users
			   SET email = $1,
			       username = $2,
			       auth_version = auth_version + 1
			 WHERE id = $3`,
			nullableLower(email), nullableLower(username), userID,
		)
		if isUniqueViolation(err) {
			return User{}, ErrConflict
		}
		if err != nil {
			return User{}, err
		}
		if _, err := tx.Exec(ctx, `
			UPDATE auth_sessions
			   SET revoked_at = COALESCE(revoked_at, now())
			 WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
			return User{}, err
		}
		if err := insertSecurityEventTx(ctx, tx, userID, userID, "account.login_changed", map[string]any{
			"email_changed":    oldEmail != email,
			"username_changed": oldUsername != username,
		}); err != nil {
			return User{}, err
		}
	}

	u, err := getUserByIDTx(ctx, tx, userID)
	if err != nil {
		return User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return User{}, err
	}
	return u, nil
}

func (s *Store) ChangeOwnPassword(ctx context.Context, userID, passwordHash string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE users
		   SET password_hash = $1,
		       auth_version = auth_version + 1
		 WHERE id = $2`, passwordHash, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		   SET revoked_at = COALESCE(revoked_at, now())
		 WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return err
	}
	if err := insertSecurityEventTx(ctx, tx, userID, userID, "security.password_changed", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) UpdateManagedMemberIdentity(
	ctx context.Context,
	actorID, businessID, targetUserID string,
	email, username, displayName *string,
) (Employee, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Employee{}, err
	}
	defer tx.Rollback(ctx)

	access, err := memberAccessInBusiness(
		ctx, tx, actorID, targetUserID, businessID, CapabilityMembersManage,
	)
	if err != nil {
		return Employee{}, err
	}
	if err := lockMutableOrganizationTx(ctx, tx, businessID); err != nil {
		return Employee{}, err
	}

	var curEmail, curUsername, curName string
	var active bool
	if err := tx.QueryRow(ctx, `
		SELECT COALESCE(email,''), COALESCE(username,''), display_name, active
		  FROM users WHERE id = $1 FOR UPDATE`, targetUserID,
	).Scan(&curEmail, &curUsername, &curName, &active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Employee{}, ErrNotFound
		}
		return Employee{}, err
	}

	nextEmail, nextUsername, nextName := curEmail, curUsername, curName
	if email != nil {
		nextEmail = strings.ToLower(strings.TrimSpace(*email))
	}
	if username != nil {
		nextUsername = strings.ToLower(strings.TrimSpace(*username))
	}
	if displayName != nil {
		nextName = strings.TrimSpace(*displayName)
	}
	if nextEmail == "" && nextUsername == "" {
		return Employee{}, ErrConflict
	}
	if nextName == "" || len([]rune(nextName)) > 120 {
		return Employee{}, ErrConflict
	}

	loginChanged := nextEmail != curEmail || nextUsername != curUsername
	nameChanged := nextName != curName
	if loginChanged || nameChanged {
		query := `
			UPDATE users
			   SET email = $1,
			       username = $2,
			       display_name = $3,
			       auth_version = auth_version + CASE WHEN $4 THEN 1 ELSE 0 END
			 WHERE id = $5`
		_, err := tx.Exec(ctx, query,
			nullableLower(nextEmail), nullableLower(nextUsername), nextName, loginChanged, targetUserID,
		)
		if isUniqueViolation(err) {
			return Employee{}, ErrConflict
		}
		if err != nil {
			return Employee{}, err
		}
		if loginChanged {
			if _, err := tx.Exec(ctx, `
				UPDATE auth_sessions
				   SET revoked_at = COALESCE(revoked_at, now())
				 WHERE user_id = $1 AND revoked_at IS NULL`, targetUserID); err != nil {
				return Employee{}, err
			}
			if err := insertSecurityEventTx(ctx, tx, targetUserID, actorID, "account.login_changed", map[string]any{
				"managed_by_organization": businessID,
			}); err != nil {
				return Employee{}, err
			}
		}
		action := "member.profile_changed"
		if loginChanged {
			action = "member.login_changed"
		}
		if err := insertAuditTx(ctx, tx, businessID, actorID, action, "member", targetUserID, map[string]any{
			"display_name": nextName,
			"role":         string(access.TargetRole),
			"login_changed": loginChanged,
		}); err != nil {
			return Employee{}, err
		}
	}

	var e Employee
	err = tx.QueryRow(ctx, `
		SELECT u.id, COALESCE(u.email,''), COALESCE(u.username,''), u.display_name,
		       u.active, m.role, m.monitoring_enabled
		  FROM users u
		  JOIN memberships m ON m.user_id = u.id
		 WHERE u.id = $1 AND m.business_id = $2`,
		targetUserID, businessID,
	).Scan(&e.ID, &e.Email, &e.Username, &e.DisplayName, &e.Active, &e.Role, &e.MonitoringEnabled)
	if err != nil {
		return Employee{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Employee{}, err
	}
	return e, nil
}

func getUserByIDTx(ctx context.Context, tx pgx.Tx, id string) (User, error) {
	var u User
	err := tx.QueryRow(ctx, `
		SELECT id, COALESCE(email,''), COALESCE(username,''), display_name, account_type
		  FROM users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.Username, &u.DisplayName, &u.AccountType)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return u, err
}

func (s *Store) ListSecurityEvents(ctx context.Context, userID string, limit int) ([]SecurityEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, COALESCE(actor_user_id::text,''), action, details,
		       extract(epoch FROM created_at)::bigint
		  FROM security_events
		 WHERE user_id = $1
		 ORDER BY created_at DESC, id DESC
		 LIMIT $2`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []SecurityEvent{}
	for rows.Next() {
		var event SecurityEvent
		var raw []byte
		if err := rows.Scan(
			&event.ID, &event.UserID, &event.ActorUserID, &event.Action, &raw, &event.CreatedAt,
		); err != nil {
			return nil, err
		}
		event.Details = map[string]any{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &event.Details); err != nil {
				return nil, err
			}
		}
		out = append(out, event)
	}
	return out, rows.Err()
}


func (s *Store) ResetManagedMemberPassword(
	ctx context.Context,
	actorID, businessID, targetUserID, passwordHash string,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	access, err := memberAccessInBusiness(
		ctx, tx, actorID, targetUserID, businessID, CapabilityMembersManage,
	)
	if err != nil {
		return err
	}
	if err := lockMutableOrganizationTx(ctx, tx, businessID); err != nil {
		return err
	}

	ct, err := tx.Exec(ctx, `
		UPDATE users
		   SET password_hash = $1,
		       auth_version = auth_version + 1
		 WHERE id = $2`,
		passwordHash, targetUserID,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		   SET revoked_at = COALESCE(revoked_at, now())
		 WHERE user_id = $1 AND revoked_at IS NULL`,
		targetUserID,
	); err != nil {
		return err
	}
	if err := insertSecurityEventTx(
		ctx, tx, targetUserID, actorID, "security.password_reset",
		map[string]any{"business_id": businessID},
	); err != nil {
		return err
	}
	if err := insertAuditTx(
		ctx, tx, businessID, actorID, "employee.password_reset", "member", targetUserID,
		map[string]any{"role": string(access.TargetRole)},
	); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
