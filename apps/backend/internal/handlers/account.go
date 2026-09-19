package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/events"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

type accountIdentityReq struct {
	CurrentPassword string `json:"current_password"`
	Email           string `json:"email"`
	Username        string `json:"username"`
}

type accountDisplayNameReq struct {
	DisplayName string `json:"display_name"`
}

type passwordChangeReq struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

type mfaPasswordReq struct {
	CurrentPassword string `json:"current_password"`
}

type mfaCodeReq struct {
	Code            string `json:"code"`
	CurrentPassword string `json:"current_password"`
}

type reauthReq struct {
	CurrentPassword string `json:"current_password"`
	Code            string `json:"code"`
}

type mfaCompleteReq struct {
	ChallengeToken string `json:"challenge_token"`
	Code           string `json:"code"`
	ClientType     string `json:"client_type"`
	ClientLabel    string `json:"client_label"`
	BusinessID     string `json:"business_id"`
}

func (h *AuthHandler) Account(c *gin.Context) {
	userID, _ := auth.UserID(c)
	u, err := h.store.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		serverError(c, err)
		return
	}
	mfa, err := h.store.MFAState(c.Request.Context(), userID)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"user": gin.H{
			"id":           u.ID,
			"email":        u.Email,
			"username":     u.Username,
			"display_name": u.DisplayName,
			"account_type": u.AccountType,
		},
		"mfa": mfa,
	})
}

func (h *AuthHandler) UpdateOwnDisplayName(c *gin.Context) {
	userID, _ := auth.UserID(c)
	var req accountDisplayNameReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	u, err := h.store.UpdateOwnDisplayName(c.Request.Context(), userID, req.DisplayName)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"user": u})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "only an organization owner can change their own display name")
	case errors.Is(err, store.ErrConflict):
		badRequest(c, "invalid display name")
	default:
		serverError(c, err)
	}
}

func (h *AuthHandler) UpdateOwnLoginIdentifiers(c *gin.Context) {
	userID, _ := auth.UserID(c)
	var req accountIdentityReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if !h.verifyCurrentPassword(c, userID, req.CurrentPassword) {
		return
	}
	u, err := h.store.UpdateOwnLoginIdentifiers(
		c.Request.Context(), userID, req.Email, req.Username,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{
			"user":            u,
			"reauth_required": true,
		})
	case errors.Is(err, store.ErrConflict):
		conflict(c, ErrCodeIdentifierTaken, "email or username is already used or invalid")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "account not found")
	default:
		serverError(c, err)
	}
}

func (h *AuthHandler) ChangeOwnPassword(c *gin.Context) {
	userID, _ := auth.UserID(c)
	var req passwordChangeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if len(req.NewPassword) < 8 {
		badRequest(c, "new_password must be at least 8 characters")
		return
	}
	if !h.verifyCurrentPassword(c, userID, req.CurrentPassword) {
		return
	}
	hash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		serverError(c, err)
		return
	}
	if err := h.store.ChangeOwnPassword(c.Request.Context(), userID, hash); err != nil {
		serverError(c, err)
		return
	}
	h.publish(c.Request.Context(), events.Event{
		Type: events.PasswordChanged, UserID: userID, ActorUserID: userID,
	})
	c.JSON(http.StatusOK, gin.H{
		"status":          "password_changed",
			"reauth_required": true,
	})
}

func (h *AuthHandler) Reauth(c *gin.Context) {
	userID, _ := auth.UserID(c)
	var req reauthReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if !h.verifyCurrentPassword(c, userID, req.CurrentPassword) {
		return
	}
	mfa, err := h.store.MFAState(c.Request.Context(), userID)
	if err != nil {
		serverError(c, err)
		return
	}
	if mfa.Enabled {
		if strings.TrimSpace(req.Code) == "" {
			apiError(c, http.StatusUnauthorized, ErrCodeMFARequired, "mfa code is required", nil)
			return
		}
		ok, err := h.verifyMFAInput(c.Request.Context(), userID, req.Code, true)
		if err != nil {
			serverError(c, err)
			return
		}
		if !ok {
			apiError(c, http.StatusUnauthorized, ErrCodeMFARequired, "invalid authentication code", nil)
			return
		}
	}
	active, version, err := h.store.UserSecurity(c.Request.Context(), userID)
	if err != nil || !active {
		unauthorized(c, "account is unavailable")
		return
	}
	grant, err := h.tok.IssueReauthGrant(userID, version)
	if err != nil {
		serverError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"reauth_token": grant, "expires_in": 300})
}

func (h *AuthHandler) ListSessions(c *gin.Context) {
	userID, _ := auth.UserID(c)
	currentID, _ := auth.SessionID(c)
	sessions, err := h.store.ListAuthSessions(c.Request.Context(), userID)
	if err != nil {
		serverError(c, err)
		return
	}
	out := make([]gin.H, 0, len(sessions))
	for _, session := range sessions {
		out = append(out, gin.H{
			"id":           session.ID,
			"client_type":  session.ClientType,
			"client_label": session.ClientLabel,
			"created_at":   session.CreatedAt,
			"last_used_at": session.LastUsedAt,
			"expires_at":   session.ExpiresAt,
			"revoked_at":   session.RevokedAt,
			"current":      session.ID == currentID,
		})
	}
	c.JSON(http.StatusOK, gin.H{"sessions": out})
}

func (h *AuthHandler) RevokeSession(c *gin.Context) {
	userID, _ := auth.UserID(c)
	sessionID := strings.TrimSpace(c.Param("session_id"))
	if sessionID == "" {
		badRequest(c, "session_id is required")
		return
	}
	err := h.store.RevokeAuthSession(c.Request.Context(), userID, sessionID)
	switch {
	case err == nil:
		currentID, _ := auth.SessionID(c)
		h.publish(c.Request.Context(), events.Event{
			Type: events.SessionsRevoked,
			UserID: userID,
			ActorUserID: userID,
			Details: map[string]any{
				"session_id": sessionID,
				"current": sessionID == currentID,
			},
		})
		c.JSON(http.StatusOK, gin.H{
			"status":          "revoked",
			"reauth_required": currentID == sessionID,
		})
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "session not found")
	default:
		serverError(c, err)
	}
}

func (h *AuthHandler) RevokeOtherSessions(c *gin.Context) {
	userID, _ := auth.UserID(c)
	currentID, _ := auth.SessionID(c)
	count, err := h.store.RevokeOtherAuthSessions(c.Request.Context(), userID, currentID)
	if err != nil {
		serverError(c, err)
		return
	}
	h.publish(c.Request.Context(), events.Event{
		Type: events.SessionsRevoked,
		UserID: userID,
		ActorUserID: userID,
		Details: map[string]any{"count": count, "scope": "other_sessions"},
	})
	c.JSON(http.StatusOK, gin.H{"status": "revoked", "count": count})
}

func (h *AuthHandler) MFAState(c *gin.Context) {
	userID, _ := auth.UserID(c)
	state, err := h.store.MFAState(c.Request.Context(), userID)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, state)
}

func (h *AuthHandler) BeginMFASetup(c *gin.Context) {
	userID, _ := auth.UserID(c)
	var req mfaPasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if !h.verifyCurrentPassword(c, userID, req.CurrentPassword) {
		return
	}
	u, err := h.store.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		serverError(c, err)
		return
	}
	raw, encoded, err := h.tok.GenerateTOTPSecret()
	if err != nil {
		serverError(c, err)
		return
	}
	encrypted, err := h.tok.EncryptMFASecret(raw)
	if err != nil {
		serverError(c, err)
		return
	}
	if err := h.store.BeginMFASetup(c.Request.Context(), userID, encrypted); err != nil {
		serverError(c, err)
		return
	}
	account := u.Email
	if account == "" {
		account = u.Username
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"secret":      encoded,
		"otpauth_uri": auth.TOTPURI(encoded, account, "ActiLens"),
	})
}

func (h *AuthHandler) ConfirmMFASetup(c *gin.Context) {
	userID, _ := auth.UserID(c)
	var req mfaCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	encrypted, err := h.store.MFASecret(c.Request.Context(), userID, false)
	if err != nil {
		notFound(c, "mfa setup not found")
		return
	}
	secret, err := h.tok.DecryptMFASecret(encrypted)
	if err != nil {
		serverError(c, err)
		return
	}
	if !auth.VerifyTOTP(secret, req.Code, time.Now()) {
		apiError(c, http.StatusUnauthorized, ErrCodeMFARequired, "invalid authentication code", nil)
		return
	}
	codes, hashes, err := auth.GenerateRecoveryCodes(10)
	if err != nil {
		serverError(c, err)
		return
	}
	if err := h.store.EnableMFA(c.Request.Context(), userID, hashes); err != nil {
		serverError(c, err)
		return
	}
	h.publish(c.Request.Context(), events.Event{
		Type: events.MFAEnabled, UserID: userID, ActorUserID: userID,
	})
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"status":         "enabled",
		"recovery_codes": codes,
	})
}

func (h *AuthHandler) RegenerateRecoveryCodes(c *gin.Context) {
	userID, _ := auth.UserID(c)
	var req mfaCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if !h.verifyCurrentPassword(c, userID, req.CurrentPassword) {
		return
	}
	ok, err := h.verifyMFAInput(c.Request.Context(), userID, req.Code, true)
	if err != nil {
		serverError(c, err)
		return
	}
	if !ok {
		apiError(c, http.StatusUnauthorized, ErrCodeMFARequired, "invalid authentication code", nil)
		return
	}
	codes, hashes, err := auth.GenerateRecoveryCodes(10)
	if err != nil {
		serverError(c, err)
		return
	}
	if err := h.store.ReplaceRecoveryCodes(c.Request.Context(), userID, hashes); err != nil {
		serverError(c, err)
		return
	}
	h.publish(c.Request.Context(), events.Event{
		Type: events.MFARecoveryCodesRegenerated,
		UserID: userID,
		ActorUserID: userID,
	})
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"recovery_codes": codes})
}

func (h *AuthHandler) DisableMFA(c *gin.Context) {
	userID, _ := auth.UserID(c)
	var req mfaCodeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if !h.verifyCurrentPassword(c, userID, req.CurrentPassword) {
		return
	}
	ok, err := h.verifyMFAInput(c.Request.Context(), userID, req.Code, true)
	if err != nil {
		serverError(c, err)
		return
	}
	if !ok {
		apiError(c, http.StatusUnauthorized, ErrCodeMFARequired, "invalid authentication code", nil)
		return
	}
	if err := h.store.DisableMFA(c.Request.Context(), userID, userID); err != nil {
		serverError(c, err)
		return
	}
	h.publish(c.Request.Context(), events.Event{
		Type: events.MFADisabled, UserID: userID, ActorUserID: userID,
	})
	c.JSON(http.StatusOK, gin.H{"status": "disabled", "reauth_required": true})
}

func (h *AuthHandler) CompleteMFA(c *gin.Context) {
	var req mfaCompleteReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	userID, challengeVersion, err := h.tok.ParseMFAChallenge(req.ChallengeToken)
	if err != nil {
		unauthorized(c, "invalid mfa challenge")
		return
	}
	active, currentVersion, err := h.store.UserSecurity(c.Request.Context(), userID)
	if err != nil || !active || currentVersion != challengeVersion {
		unauthorized(c, "invalid mfa challenge")
		return
	}
	ok, err := h.verifyMFAInput(c.Request.Context(), userID, req.Code, true)
	if err != nil {
		serverError(c, err)
		return
	}
	if !ok {
		apiError(c, http.StatusUnauthorized, ErrCodeMFARequired, "invalid authentication code", nil)
		return
	}
	u, err := h.store.GetUserByID(c.Request.Context(), userID)
	if err != nil {
		serverError(c, err)
		return
	}
	if req.ClientType == "desktop" && strings.TrimSpace(req.BusinessID) != "" {
		organizations, err := h.store.LoginOrganizations(c.Request.Context(), userID)
		if err != nil {
			serverError(c, err)
			return
		}
		found := false
		for _, organization := range organizations {
			if organization.BusinessID == strings.TrimSpace(req.BusinessID) {
				found = true
				break
			}
		}
		if !found {
			forbidden(c, "not an active or blocked member of that organization")
			return
		}
	}
	h.issue(c, http.StatusOK, u, req.ClientType, req.ClientLabel, strings.TrimSpace(req.BusinessID))
}

func (h *AuthHandler) ResetMemberMFA(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	err := h.store.ResetManagedMemberMFA(
		c.Request.Context(), actorID, c.Param("id"), c.Param("user_id"),
	)
	switch {
	case err == nil:
		h.publish(c.Request.Context(), events.Event{
			Type: events.MFAReset,
			OrganizationID: c.Param("id"),
			ActorUserID: actorID,
			TargetUserID: c.Param("user_id"),
		})
		c.JSON(http.StatusOK, gin.H{"status": "reset"})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission to reset mfa")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "member not found")
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	default:
		serverError(c, err)
	}
}

func (h *AuthHandler) verifyCurrentPassword(c *gin.Context, userID, password string) bool {
	hash, err := h.store.PasswordHashByUserID(c.Request.Context(), userID)
	if err != nil {
		serverError(c, err)
		return false
	}
	ok, err := auth.VerifyPassword(hash, password)
	if err != nil || !ok {
		apiError(c, http.StatusUnauthorized, ErrCodeReauthRequired, "current password is incorrect", nil)
		return false
	}
	return true
}

func (h *AuthHandler) verifyMFAInput(
	ctx context.Context,
	userID, code string,
	allowRecovery bool,
) (bool, error) {
	encrypted, err := h.store.MFASecret(ctx, userID, true)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	secret, err := h.tok.DecryptMFASecret(encrypted)
	if err != nil {
		return false, err
	}
	if auth.VerifyTOTP(secret, code, time.Now()) {
		return true, nil
	}
	if !allowRecovery {
		return false, nil
	}
	return h.store.ConsumeRecoveryCode(ctx, userID, auth.HashRecoveryCode(code))
}
