package handlers

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/obs"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const (
	defaultEnrollmentHours = 24
	maxEnrollmentHours     = 168 // 7 days
)

type createEnrollmentReq struct {
	ExpiresInHours int `json:"expires_in_hours"`
}

// CreateEnrollmentToken creates a short-lived, one-time deployment credential for
// a member in the organization identified by the route. Only SHA-256 is persisted.
func (h *OwnerHandler) CreateEnrollmentToken(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	var req createEnrollmentReq
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			badRequest(c, "invalid body")
			return
		}
	}
	var ttl time.Duration
	if req.ExpiresInHours == 0 {
		business, err := h.store.GetBusiness(c.Request.Context(), c.Param("id"))
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				notFound(c, "organization not found")
			} else {
				serverError(c, err)
			}
			return
		}
		ttl = time.Duration(business.EnrollmentTokenTTLS) * time.Second
	} else {
		if req.ExpiresInHours < 1 || req.ExpiresInHours > maxEnrollmentHours {
			badRequest(c, "expires_in_hours must be between 1 and 168")
			return
		}
		ttl = time.Duration(req.ExpiresInHours) * time.Hour
	}
	if ttl <= 0 {
		ttl = defaultEnrollmentHours * time.Hour
	}

	token, hash, err := newEnrollmentToken()
	if err != nil {
		serverError(c, err)
		return
	}
	expiresAt := time.Now().UTC().Add(ttl)
	businessID, err := h.store.CreateEnrollmentToken(
		c.Request.Context(), actorID, c.Param("id"), c.Param("user_id"), hash, expiresAt,
	)
	switch {
	case err == nil:
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusCreated, gin.H{
			"token":       token,
			"business_id": businessID,
			"expires_at":  expiresAt.Format(time.RFC3339),
		})
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "member not found")
	case errors.Is(err, store.ErrMemberBlocked):
		apiError(c, http.StatusConflict, ErrCodeMemberBlocked, "member is blocked", nil)
	case errors.Is(err, store.ErrMemberRemoved):
		apiError(c, http.StatusConflict, ErrCodeMemberRemoved, "member was removed from this organization", nil)
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrConflict):
		apiError(c, http.StatusConflict, ErrCodeConflict, "could not create enrollment token", nil)
	default:
		serverError(c, err)
	}
}

type redeemEnrollmentReq struct {
	Token string `json:"token"`
}

// Enroll exchanges a valid one-time deployment token for a normal versioned JWT
// pair. It is public but protected by the same rate limiter as password login.
func (h *AuthHandler) Enroll(c *gin.Context) {
	var req redeemEnrollmentReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if !strings.HasPrefix(req.Token, "atl_enroll_") || len(req.Token) < 32 {
		unauthorized(c, "invalid or expired enrollment token")
		return
	}

	grant, err := h.store.RedeemEnrollmentToken(c.Request.Context(), enrollmentTokenHash(req.Token))
	if err != nil {
		// Do not distinguish unknown, expired, revoked, already-used or stale-version
		// tokens. They are all authentication failures to an unauthenticated caller.
		if errors.Is(err, store.ErrNotFound) || errors.Is(err, store.ErrConflict) {
			unauthorized(c, "invalid or expired enrollment token")
			return
		}
		serverError(c, err)
		return
	}

	// Enrollment creates a first-class desktop session bound to the enrolled
	// organization. The device row itself is bound on the first sync when the
	// client-generated device UUID is known.
	sessionID := uuid.NewString()
	pair, err := h.tok.IssueSessionVersioned(grant.User.ID, grant.AuthVersion, sessionID)
	if err != nil {
		serverError(c, err)
		return
	}
	if err := h.store.CreateAuthSession(
		c.Request.Context(), grant.User.ID, sessionID, auth.HashToken(pair.RefreshToken),
		"desktop", "Managed desktop", grant.AuthVersion, time.Now().UTC().Add(auth.RefreshTTL()),
	); err != nil {
		serverError(c, err)
		return
	}
	obs.Info("enrollment ok", "user", grant.User.ID, "business", grant.BusinessID, "session", sessionID)
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":           grant.User.ID,
			"email":        grant.User.Email,
			"username":     grant.User.Username,
			"display_name": grant.User.DisplayName,
			"account_type": grant.User.AccountType,
		},
		"business_id": grant.BusinessID,
		"tokens":      pair,
	})
}

func newEnrollmentToken() (raw string, hash string, err error) {
	buf := make([]byte, 32)
	if _, err = rand.Read(buf); err != nil {
		return "", "", err
	}
	raw = "atl_enroll_" + base64.RawURLEncoding.EncodeToString(buf)
	return raw, enrollmentTokenHash(raw), nil
}

func enrollmentTokenHash(raw string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(raw)))
	return hex.EncodeToString(sum[:])
}
