package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

type MFAState struct {
	Enabled   bool       `json:"enabled"`
	EnabledAt *time.Time `json:"enabled_at"`
}

func (s *Store) MFAState(ctx context.Context, userID string) (MFAState, error) {
	var state MFAState
	err := s.pool.QueryRow(ctx, `
		SELECT enabled_at IS NOT NULL, enabled_at
		  FROM user_mfa
		 WHERE user_id = $1`, userID,
	).Scan(&state.Enabled, &state.EnabledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return MFAState{}, nil
	}
	return state, err
}

func (s *Store) MFASecret(ctx context.Context, userID string, requireEnabled bool) ([]byte, error) {
	var secret []byte
	var enabledAt *time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT totp_secret_encrypted, enabled_at
		  FROM user_mfa
		 WHERE user_id = $1`, userID,
	).Scan(&secret, &enabledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if requireEnabled && enabledAt == nil {
		return nil, ErrNotFound
	}
	return secret, nil
}

func (s *Store) BeginMFASetup(ctx context.Context, userID string, encryptedSecret []byte) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `
		INSERT INTO user_mfa (user_id, totp_secret_encrypted, enabled_at, updated_at)
		VALUES ($1, $2, NULL, now())
		ON CONFLICT (user_id) DO UPDATE
		SET totp_secret_encrypted = EXCLUDED.totp_secret_encrypted,
		    enabled_at = NULL,
		   updated_at = now()`,
		userID, encryptedSecret,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) EnableMFA(ctx context.Context, userID string, recoveryHashes []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	ct, err := tx.Exec(ctx, `
		UPDATE user_mfa
		   SET enabled_at = now(), updated_at = now()
		 WHERE user_id = $1`, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, hash := range recoveryHashes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO mfa_recovery_codes (user_id, code_hash)
			VALUES ($1, $2)`, userID, hash); err != nil {
			return err
		}
	}
	if err := insertSecurityEventTx(ctx, tx, userID, userID, "security.mfa_enabled", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID string, hashes []string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var enabled bool
	if err := tx.QueryRow(ctx, `
		SELECT enabled_at IS NOT NULL FROM user_mfa WHERE user_id = $1`,
		userID,
	).Scan(&enabled); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if !enabled {
		return ErrForbidden
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	for _, hash := range hashes {
		if _, err := tx.Exec(ctx, `
			INSERT INTO mfa_recovery_codes (user_id, code_hash)
			VALUES ($1, $2)`, userID, hash); err != nil {
			return err
		}
	}
	if err := insertSecurityEventTx(ctx, tx, userID, userID, "security.recovery_codes_regenerated", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ConsumeRecoveryCode(ctx context.Context, userID, hash string) (bool, error) {
	ct, err := s.pool.Exec(ctx, `
		UPDATE mfa_recovery_codes
		   SET used_at = now()
	 WHERE user_id = $1
	   AND code_hash = $2
	   AND used_at IS NULL`, userID, hash)
	if err != nil {
		return false, err
	}
	return ct.RowsAffected() == 1, nil
}

func (s *Store) DisableMFA(ctx context.Context, userID, actorUserID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx, `DELETE FROM user_mfa WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = $1`, userID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE users SET auth_version = auth_version + 1 WHERE id = $1`,
		userID,
	); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE auth_sessions
		   SET revoked_at = COALESCE(revoked_at, now())
	 WHERE user_id = $1 AND revoked_at IS NULL`, userID); err != nil {
		return err
	}
	if err := insertSecurityEventTx(ctx, tx, userID, actorUserID, "security.mfa_disabled", nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) ResetManagedMemberMFA(ctx context.Context, actorID, businessID, targetUserID string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	access, err := memberAccessInBusiness(ctx, tx, actorID, targetUserID, businessID, CapabilityMembersManage)
	if err != nil {
		return err
	}
	if actorID == targetUserID || access.TargetRole == RoleOwner || (access.ActorRole == RoleAdmin && access.TargetRole == RoleAdmin) {
		return ErrForbidden
	}
	if err := lockMutableOrganizationTx(ctx, tx, businessID); err != nil {
		return err
	}

	if _, err := tx.Exec(ctx, `DELETE FROM user_mfa WHERE user_id = $1`, targetUserID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM mfa_recovery_codes WHERE user_id = $1`, targetUserID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET auth_version = auth_version + 1 WHERE id = $1`, targetUserID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE auth_sessions SET revoked_at = COALESCE(revoked_at, now()) WHERE user_id = $1 AND revoked_at IS NULL`, targetUserID); err != nil {
		return err
	}
	if err := insertSecurityEventTx(ctx, tx, targetUserID, actorID, "security.mfa_reset", map[string]any{"business_id": businessID}); err != nil {
		return err
	}
	if err := insertAuditTx(ctx, tx, businessID, actorID, "member.mfa_reset", "member", targetUserID, nil); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
