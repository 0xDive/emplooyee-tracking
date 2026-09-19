package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/events"
	"actilens/backend/internal/obs"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// AuthHandler serves registration, login, refresh, and the public picker.
type AuthHandler struct {
	store     *store.Store
	tok       *auth.Manager
	publisher events.Publisher
}

// NewAuthHandler wires the auth handler.
func NewAuthHandler(s *store.Store, tok *auth.Manager) *AuthHandler {
	return &AuthHandler{store: s, tok: tok, publisher: events.Discard{}}
}

func (h *AuthHandler) SetEventPublisher(publisher events.Publisher) {
	if publisher != nil {
		h.publisher = publisher
	}
}

func (h *AuthHandler) publish(ctx context.Context, event events.Event) {
	if h.publisher != nil {
		h.publisher.Publish(ctx, event)
	}
}

type registerReq struct {
	Email       string `json:"email"`    // optional if username is set
	Username    string `json:"username"` // optional if email is set
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
	AccountType string `json:"account_type"` // 'manager' (default) | 'parent'
	ClientType  string `json:"client_type"`
	ClientLabel string `json:"client_label"`
}

// Register creates a new account (any user can be an owner) and returns tokens.
func (h *AuthHandler) Register(c *gin.Context) {
	var req registerReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	req.Email = strings.TrimSpace(req.Email)
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" {
		badRequest(c, "display_name is required")
		return
	}
	if req.Email == "" && req.Username == "" {
		badRequest(c, "an email or username is required")
		return
	}
	if req.Username != "" && !usernameRe.MatchString(req.Username) {
		badRequest(c, "username must be 3-32 chars: lowercase letters, digits, underscores")
		return
	}
	if len(req.Password) < 8 {
		badRequest(c, "password must be at least 8 characters")
		return
	}
	if req.AccountType == "" {
		req.AccountType = "manager"
	}
	if req.AccountType != "manager" && req.AccountType != "parent" {
		badRequest(c, "account_type must be 'manager' or 'parent'")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		serverError(c, err)
		return
	}
	u, err := h.store.CreateUser(c.Request.Context(), req.Email, req.Username, hash, req.DisplayName, req.AccountType)
	if errors.Is(err, store.ErrConflict) {
		apiError(c, http.StatusConflict, ErrCodeIdentifierTaken, "that email or username is already taken", nil)
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}
	h.issue(c, http.StatusCreated, u, req.ClientType, req.ClientLabel, "")
}

type loginReq struct {
	Identifier string `json:"identifier"` // email or username
	Email      string `json:"email"`      // legacy field; treated as an identifier
	Password   string `json:"password"`
	BusinessID string `json:"business_id"` // optional: employee picking their company
	ClientType string `json:"client_type"`
	ClientLabel string `json:"client_label"`
}

// Login verifies credentials and returns tokens. If business_id is supplied, the
// user must be a member of that business.
func (h *AuthHandler) Login(c *gin.Context) {
	var req loginReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}

	identifier := req.Identifier
	if identifier == "" {
		identifier = req.Email
	}
	u, hash, err := h.store.GetUserByIdentifier(c.Request.Context(), identifier)
	if err != nil {
		// Same response for unknown user and bad password — don't leak which.
		unauthorized(c, "invalid credentials")
		return
	}
	active, err := h.store.IsUserActive(c.Request.Context(), u.ID)
	if err != nil || !active {
		unauthorized(c, "invalid credentials")
		return
	}
	ok, err := auth.VerifyPassword(hash, req.Password)
	if err != nil || !ok {
		unauthorized(c, "invalid credentials")
		return
	}

	resolvedBusinessID := strings.TrimSpace(req.BusinessID)
	if req.ClientType == "desktop" {
		organizations, err := h.store.LoginOrganizations(c.Request.Context(), u.ID)
		if err != nil {
			serverError(c, err)
			return
		}
		if resolvedBusinessID == "" {
			switch len(organizations) {
			case 0:
				// Standalone account: no managed organization binding.
			case 1:
				resolvedBusinessID = organizations[0].BusinessID
			default:
				apiError(c, http.StatusConflict, ErrCodeOrganizationRequired, "organization selection required", gin.H{
					"organizations": organizations,
				})
				return
			}
		} else {
			found := false
			for _, organization := range organizations {
				if organization.BusinessID == resolvedBusinessID {
					found = true
					break
				}
			}
			if !found {
				forbidden(c, "not an active or blocked member of that organization")
				return
			}
		}
	} else if resolvedBusinessID != "" {
		member, err := h.store.IsMember(c.Request.Context(), u.ID, resolvedBusinessID)
		if err != nil {
			serverError(c, err)
			return
		}
		if !member {
			forbidden(c, "not a member of that organization")
			return
		}
	}

	mfaState, err := h.store.MFAState(c.Request.Context(), u.ID)
	if err != nil {
		serverError(c, err)
		return
	}
	if mfaState.Enabled {
		_, version, err := h.store.UserSecurity(c.Request.Context(), u.ID)
		if err != nil {
			serverError(c, err)
			return
		}
		challenge, err := h.tok.IssueMFAChallenge(u.ID, version)
		if err != nil {
			serverError(c, err)
			return
		}
		c.Header("Cache-Control", "no-store")
		apiError(c, http.StatusUnauthorized, ErrCodeMFARequired, "multi-factor authentication required", gin.H{
			"challenge_token": challenge,
			"business_id":     resolvedBusinessID,
		})
		return
	}
	h.issue(c, http.StatusOK, u, req.ClientType, req.ClientLabel, resolvedBusinessID)
}

type refreshReq struct {
	RefreshToken string `json:"refresh_token"`
}

// Refresh exchanges a valid refresh token for a new token pair.
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req refreshReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	userID, tokenVersion, sessionID, err := h.tok.ParseRefreshSession(req.RefreshToken)
	if err != nil {
		unauthorized(c, "invalid refresh token")
		return
	}
	active, currentVersion, err := h.store.UserSecurity(c.Request.Context(), userID)
	if err != nil || !active || currentVersion != tokenVersion {
		unauthorized(c, "invalid refresh token")
		return
	}

	// Legacy refresh tokens did not carry a session id. Migrate them into a
	// first-class session on first successful refresh.
	if sessionID == "" {
		sessionID = uuid.NewString()
		pair, err := h.tok.IssueSessionVersioned(userID, currentVersion, sessionID)
		if err != nil {
			serverError(c, err)
			return
		}
		if err := h.store.CreateAuthSession(
			c.Request.Context(), userID, sessionID, auth.HashToken(pair.RefreshToken),
			"web", "Migrated session", currentVersion, time.Now().UTC().Add(auth.RefreshTTL()),
		); err != nil {
			serverError(c, err)
			return
		}
		obs.Info("legacy session migrated", "user", userID, "session", sessionID)
		c.JSON(http.StatusOK, pair)
		return
	}

	pair, err := h.tok.IssueSessionVersioned(userID, currentVersion, sessionID)
	if err != nil {
		serverError(c, err)
		return
	}
	if err := h.store.RotateAuthSession(
		c.Request.Context(), userID, sessionID,
		auth.HashToken(req.RefreshToken), auth.HashToken(pair.RefreshToken),
		currentVersion, time.Now().UTC().Add(auth.RefreshTTL()),
	); err != nil {
		apiError(c, http.StatusUnauthorized, ErrCodeSessionRevoked, "session revoked", nil)
		return
	}
	obs.Info("session refreshed", "user", userID, "session", sessionID)
	c.JSON(http.StatusOK, pair)
}

// PublicBusinesses lists businesses + owner names for the login picker (no auth).
func (h *AuthHandler) PublicBusinesses(c *gin.Context) {
	list, err := h.store.ListPublicBusinesses(c.Request.Context())
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"businesses": list})
}

// Me returns the authenticated user (protected route).
func (h *AuthHandler) Me(c *gin.Context) {
	userID, ok := auth.UserID(c)
	if !ok {
		unauthorized(c, "unauthenticated")
		return
	}
	u, err := h.store.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"id":           u.ID,
		"email":        u.Email,
		"username":     u.Username,
		"display_name": u.DisplayName,
		"account_type": u.AccountType,
	})
}

func (h *AuthHandler) issue(
	c *gin.Context,
	status int,
	u store.User,
	clientType, clientLabel, businessID string,
) {
	active, version, err := h.store.UserSecurity(c.Request.Context(), u.ID)
	if err != nil {
		serverError(c, err)
		return
	}
	if !active {
		unauthorized(c, "invalid credentials")
		return
	}
	sessionID := uuid.NewString()
	pair, err := h.tok.IssueSessionVersioned(u.ID, version, sessionID)
	if err != nil {
		serverError(c, err)
		return
	}
	if err := h.store.CreateAuthSession(
		c.Request.Context(), u.ID, sessionID, auth.HashToken(pair.RefreshToken),
		clientType, clientLabel, version, time.Now().UTC().Add(auth.RefreshTTL()),
	); err != nil {
		serverError(c, err)
		return
	}
	c.JSON(status, gin.H{
		"user": gin.H{
			"id":           u.ID,
			"email":        u.Email,
			"username":     u.Username,
			"display_name": u.DisplayName,
			"account_type": u.AccountType,
		},
		"tokens":      pair,
		"business_id": businessID,
	})
}
