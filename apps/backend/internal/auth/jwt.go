package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	kindAccess  = "access"
	kindRefresh = "refresh"
	kindMFA     = "mfa_challenge"
	kindReauth  = "reauth"
)

const (
	accessTTL    = 15 * time.Minute
	refreshTTL   = 30 * 24 * time.Hour
	challengeTTL = 5 * time.Minute
	reauthTTL    = 5 * time.Minute
)

var ErrInvalidToken = errors.New("invalid token")

type Manager struct{ secret []byte }

func NewManager(secret string) *Manager { return &Manager{secret: []byte(secret)} }

type claims struct {
	Kind      string `json:"kind"`
	Version   int    `json:"ver"`
	SessionID string `json:"sid,omitempty"`
	jwt.RegisteredClaims
}

type TokenPair struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	SessionID    string `json:"session_id,omitempty"`
}

// Issue is retained for compatibility/tests. New authenticated product sessions use
// IssueSessionVersioned so each token pair belongs to a revocable session.
func (m *Manager) Issue(userID string) (TokenPair, error) {
	return m.IssueVersioned(userID, 1)
}

func (m *Manager) IssueVersioned(userID string, version int) (TokenPair, error) {
	return m.IssueSessionVersioned(userID, version, "")
}

func (m *Manager) IssueSessionVersioned(userID string, version int, sessionID string) (TokenPair, error) {
	access, err := m.sign(userID, kindAccess, accessTTL, version, sessionID)
	if err != nil {
		return TokenPair{}, err
	}
	refresh, err := m.sign(userID, kindRefresh, refreshTTL, version, sessionID)
	if err != nil {
		return TokenPair{}, err
	}
	return TokenPair{
		AccessToken:  access,
		RefreshToken: refresh,
		ExpiresIn:    int(accessTTL.Seconds()),
		SessionID:    sessionID,
	}, nil
}

func (m *Manager) IssueMFAChallenge(userID string, version int) (string, error) {
	return m.sign(userID, kindMFA, challengeTTL, version, "")
}

func (m *Manager) IssueReauthGrant(userID string, version int) (string, error) {
	return m.sign(userID, kindReauth, reauthTTL, version, "")
}

func (m *Manager) sign(userID, kind string, ttl time.Duration, version int, sessionID string) (string, error) {
	now := time.Now()
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims{
		Kind: kind, Version: version, SessionID: sessionID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			ID:        uuid.NewString(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	})
	return t.SignedString(m.secret)
}

func (m *Manager) ParseAccess(token string) (string, error) {
	id, _, _, err := m.ParseAccessSession(token)
	return id, err
}

func (m *Manager) ParseRefresh(token string) (string, error) {
	id, _, _, err := m.ParseRefreshSession(token)
	return id, err
}

func (m *Manager) ParseAccessVersioned(token string) (string, int, error) {
	id, version, _, err := m.ParseAccessSession(token)
	return id, version, err
}

func (m *Manager) ParseRefreshVersioned(token string) (string, int, error) {
	id, version, _, err := m.ParseRefreshSession(token)
	return id, version, err
}

func (m *Manager) ParseAccessSession(token string) (string, int, string, error) {
	return m.parse(token, kindAccess)
}

func (m *Manager) ParseRefreshSession(token string) (string, int, string, error) {
	return m.parse(token, kindRefresh)
}

func (m *Manager) ParseMFAChallenge(token string) (string, int, error) {
	id, version, _, err := m.parse(token, kindMFA)
	return id, version, err
}

func (m *Manager) ParseReauthGrant(token string) (string, int, error) {
	id, version, _, err := m.parse(token, kindReauth)
	return id, version, err
}

func (m *Manager) parse(token, wantKind string) (string, int, string, error) {
	c := &claims{}
	parsed, err := jwt.ParseWithClaims(token, c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, ErrInvalidToken
		}
		return m.secret, nil
	})
	if err != nil || !parsed.Valid || c.Kind != wantKind || c.Subject == "" || c.Version <= 0 {
		return "", 0, "", ErrInvalidToken
	}
	return c.Subject, c.Version, c.SessionID, nil
}

func AccessTTL() time.Duration { return accessTTL }
func RefreshTTL() time.Duration { return refreshTTL }

func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func RandomTokenBytes(n int) ([]byte, error) {
	out := make([]byte, n)
	_, err := rand.Read(out)
	return out, err
}
