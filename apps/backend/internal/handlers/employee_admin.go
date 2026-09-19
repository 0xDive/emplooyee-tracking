package handlers

import (
	"errors"
	"net/http"
	"strings"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

type updateEmployeeReq struct {
	Email       *string `json:"email"`
	Username    *string `json:"username"`
	DisplayName *string `json:"display_name"`
	Active      *bool   `json:"active"`
}

// UpdateEmployee lets the business owner edit login/name and archive/restore an employee.
func legacyBusinessID(c *gin.Context) (string, bool) {
	businessID := strings.TrimSpace(c.Query("business_id"))
	if businessID == "" {
		badRequest(c, "business_id is required")
		return "", false
	}
	return businessID, true
}

// UpdateEmployee is retained for old clients but no longer performs global account
// activation changes or infers an organization. New clients use the scoped member API.
func (h *OwnerHandler) UpdateEmployee(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	businessID, ok := legacyBusinessID(c)
	if !ok {
		return
	}
	var req updateEmployeeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if req.Active != nil {
		badRequest(c, "active is no longer supported; use organization member lifecycle")
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

	e, err := h.store.UpdateManagedMemberIdentity(
		c.Request.Context(),
		actorID,
		businessID,
		c.Param("id"),
		req.Email,
		req.Username,
		req.DisplayName,
	)
	if employeeMutationError(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"employee": e})
}

type resetEmployeePasswordReq struct {
	Password string `json:"password"`
}

func (h *OwnerHandler) ResetEmployeePassword(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	businessID, ok := legacyBusinessID(c)
	if !ok {
		return
	}
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
		c.Request.Context(), actorID, businessID, c.Param("id"), hash,
	)
	if employeeMutationError(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

// ArchiveEmployee is a compatibility alias for organization-scoped member removal.
// It never disables the global user account.
func (h *OwnerHandler) ArchiveEmployee(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	businessID, ok := legacyBusinessID(c)
	if !ok {
		return
	}
	err := h.store.RemoveMember(c.Request.Context(), actorID, businessID, c.Param("id"))
	if employeeMutationError(c, err) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "removed"})
}

func employeeMutationError(c *gin.Context, err error) bool {
	switch {
	case err == nil:
		return false
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
	return true
}
