package store

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type AuthSession struct {
	ID          string    `json:"id"`
	UserID      string    `json:"-"`
	ClientType  string    `json:"client_type"`
	ClientLabel string    `json:"client_label"`
	CreatedAt   time.Time `json:"created_at"`
	LastUsedAt  time.Time `json:"last_used_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	RevokedAt   *time.Time `json:"revoked_at"`
}

func normalizeClientType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "desktop":
		return "desktop"
	default:
		return "web"
	}
}

func (s *Store) CreateAuthSession(
	ctx context.Context,
	userID, sessionID, refreshTokenHash, clientType, clientLabel string,
	authVersion int,
	expiresAt time.Time,
) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO auth_sessions (
			id, user_id, refresh_token_hash, client_type, client_label,
			auth_version_at_issue, expires_at
		)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		sessionID, userID, refreshTokenHash, normalizeClientType(clientType),
		strings.TrimSpace(clientLabel), authVersion, expiresAt,
	)
	return err
}

func (s *Store) RotateAuthSession(
	ctx context.Context,
	userID, sessionID, oldRefreshHash, newRefreshHash string,
	currentVersion int,
	expiresAt time.Time,
) error {
	ct, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		   SET refresh_token_hash = $1,
		       last_used_at = now(),
		       expires_at = $2,
		       auth_version_at_issue = $3
		 WHERE id = $4
		   AND user_id = $5
		   AND refresh_token_hash = $6
		   AND revoked_at IS NULL
		   AND expires_at > now()`,
		newRefreshHash, expiresAt, currentVersion, sessionID, userID, oldRefreshHash,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SessionActive(ctx context.Context, userID, sessionID string) (bool, error) {
	if sessionID == "" {
		return true, nil // compatibility for pre-session JWTs during the migration window
	}
	var active bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS(
			SELECT 1
			  FROM auth_sessions
			 WHERE id = $1
			   AND user_id = $2
			   AND revoked_at IS NULL
			   AND expires_at > now()
		)`, sessionID, userID,
	).Scan(&active)
	return active, err
}

func (s *Store) ListAuthSessions(ctx context.Context, userID string) ([]AuthSession, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, user_id, client_type, client_label, created_at,
		       last_used_at, expires_at, revoked_at
		  FROM auth_sessions
		 WHERE user_id = $1
		   AND expires_at > now()
		 ORDER BY revoked_at NULLS FIRST, last_used_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AuthSession{}
	for rows.Next() {
		var session AuthSession
		if err := rows.Scan(
			&session.ID, &session.UserID, &session.ClientType, &session.ClientLabel,
			&session.CreatedAt, &session.LastUsedAt, &session.ExpiresAt, &session.RevokedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, session)
	}
	return out, rows.Err()
}

func (s *Store) RevokeAuthSession(ctx context.Context, userID, sessionID string) error {
	ct, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		   SET revoked_at = COALESCE(revoked_at, now())
		 WHERE id = $1 AND user_id = $2`, sessionID, userID)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) RevokeOtherAuthSessions(ctx context.Context, userID, currentSessionID string) (int64, error) {
	ct, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		   SET revoked_at = COALESCE(revoked_at, now())
		 WHERE user_id = $1
		   AND id <> $2
		   AND revoked_at IS NULL`, userID, currentSessionID)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

func (s *Store) RevokeAllAuthSessions(ctx context.Context, userID string) (int64, error) {
	ct, err := s.pool.Exec(ctx, `
		UPDATE auth_sessions
		   SET revoked_at = COALESCE(revoked_at, now())
		 WHERE user_id = $1
		   AND revoked_at IS NULL`, userID)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

func (s *Store) DeleteExpiredSessions(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM auth_sessions WHERE expires_at <= now() - interval '7 days'`)
	return err
}

func (s *Store) AuthSession(ctx context.Context, userID, sessionID string) (AuthSession, error) {
	var session AuthSession
	err := s.pool.QueryRow(ctx, `
		SELECT id, user_id, client_type, client_label, created_at,
		       last_used_at, expires_at, revoked_at
		  FROM auth_sessions
		 WHERE id = $1 AND user_id = $2`, sessionID, userID,
	).Scan(
		&session.ID, &session.UserID, &session.ClientType, &session.ClientLabel,
		&session.CreatedAt, &session.LastUsedAt, &session.ExpiresAt, &session.RevokedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return AuthSession{}, ErrNotFound
	}
	return session, err
}
