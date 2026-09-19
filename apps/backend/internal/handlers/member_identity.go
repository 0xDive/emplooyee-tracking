package handlers

import (
	"errors"
	"net/http"
	"strings"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

type updateManagedMemberIdentityReq struct {
	Email       *string `json:"email"`
	Username    *string `json:"username"`
	DisplayName *string `json:"display_name"`
}

func (h *OwnerHandler) UpdateManagedMemberIdentity(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	var req updateManagedMemberIdentityReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}

	if req.Email != nil {
		v := strings.TrimSpace(*req.Email)
		req.Email = &v
	}
	if req.Username != nil {
		v := strings.ToLower(strings.TrimSpace(*req.Username))
		if v != "" && !usernameRe.MatchString(v) {
			badRequest(c, "username must be 3-32 chars: lowercase letters, digits, underscores")
			return
		}
		req.Username = &v
	}
	if req.DisplayName != nil {
		v := strings.TrimSpace(*req.DisplayName)
		if v == "" {
			badRequest(c, "display_name is required")
			return
		}
		req.DisplayName = &v
	}

	employee, err := h.store.UpdateManagedMemberIdentity(
		c.Request.Context(),
		actorID,
		c.Param("id"),
		c.Param("user_id"),
		req.Email,
		req.Username,
		req.DisplayName,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"employee": employee})
	case errors.Is(err, store.ErrConflict):
		conflict(c, ErrCodeIdentifierTaken, "email/username conflict or invalid member data")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "member not found")
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	default:
		serverError(c, err)
	}
}

func (h *OwnerHandler) ResetManagedMemberPassword(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	var req resetEmployeePasswordReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if len(req.Password) < 8 {
		badRequest(c, "password must be at least 8 characters")
		return
	}

	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		serverError(c, err)
		return
	}

	err = h.store.ResetManagedMemberPassword(
		c.Request.Context(),
		actorID,
		c.Param("id"),
		c.Param("user_id"),
		hash,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "member not found")
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	default:
		serverError(c, err)
	}
}
