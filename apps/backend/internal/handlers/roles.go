package handlers

import (
	"errors"
	"net/http"
	"strings"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

// ListMyMemberships returns every organization membership for the authenticated
// user, including the effective role and whether monitoring is enabled for that
// membership. The web console can use this to decide which capabilities to show.
func (h *OwnerHandler) ListMyMemberships(c *gin.Context) {
	userID, _ := auth.UserID(c)
	memberships, err := h.store.MembershipsForUser(c.Request.Context(), userID)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"memberships": memberships})
}

type updateMemberRoleReq struct {
	Role string `json:"role"`
}

// UpdateMemberRole changes a non-owner membership role. Only the owner of the
// business has PermissionManageRoles, so admins cannot promote themselves or
// other users to privileged roles.
func (h *OwnerHandler) UpdateMemberRole(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	var req updateMemberRoleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	role := store.BusinessRole(strings.ToLower(strings.TrimSpace(req.Role)))
	if role != store.RoleAdmin && role != store.RoleManager && role != store.RoleEmployee {
		badRequest(c, "role must be 'admin', 'manager', or 'employee'")
		return
	}

	err := h.store.UpdateMembershipRole(
		c.Request.Context(), actorID, c.Param("id"), c.Param("user_id"), role,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"status": "ok", "role": role})
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "member not found")
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	case errors.Is(err, store.ErrConflict):
		badRequest(c, "invalid role")
	default:
		serverError(c, err)
	}
}

type updateMemberMonitoringReq struct {
	Enabled *bool `json:"enabled"`
}

// UpdateMemberMonitoring controls whether ActiLens may collect/sync telemetry for
// this membership. It is intentionally independent from the console role.
func (h *OwnerHandler) UpdateMemberMonitoring(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	var req updateMemberMonitoringReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		badRequest(c, "enabled must be a boolean")
		return
	}

	err := h.store.UpdateMembershipMonitoring(
		c.Request.Context(), actorID, c.Param("id"), c.Param("user_id"), *req.Enabled,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"status": "ok", "monitoring_enabled": *req.Enabled})
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

// ListConsoleBusinesses exposes the organizations in which the current user has
// a console-capable role (owner/admin/manager). Employees get an empty list.
func (h *OwnerHandler) ListConsoleBusinesses(c *gin.Context) {
	userID, _ := auth.UserID(c)
	businesses, err := h.store.ListBusinessesForConsole(c.Request.Context(), userID)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"businesses": businesses})
}
