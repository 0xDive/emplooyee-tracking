package handlers

import (
	"errors"
	"net/http"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

type restoreMemberReq struct {
	MonitoringEnabled *bool `json:"monitoring_enabled"`
}

func (h *OwnerHandler) ListFormerMembers(c *gin.Context) {
	if !h.requireBusinessPermission(c, c.Param("id"), store.CapabilityMembersManage) {
		return
	}
	list, err := h.store.ListFormerMembers(c.Request.Context(), c.Param("id"))
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"employees": list})
}

func (h *OwnerHandler) BlockMember(c *gin.Context) {
	h.memberLifecycleMutation(c, "blocked", nil)
}

func (h *OwnerHandler) UnblockMember(c *gin.Context) {
	h.memberLifecycleMutation(c, "active", nil)
}

func (h *OwnerHandler) RemoveMember(c *gin.Context) {
	h.memberLifecycleMutation(c, "removed", nil)
}

func (h *OwnerHandler) RestoreMember(c *gin.Context) {
	var req restoreMemberReq
	if err := c.ShouldBindJSON(&req); err != nil || req.MonitoringEnabled == nil {
		badRequest(c, "monitoring_enabled must be a boolean")
		return
	}
	h.memberLifecycleMutation(c, "restore", req.MonitoringEnabled)
}

func (h *OwnerHandler) memberLifecycleMutation(
	c *gin.Context,
	action string,
	monitoringEnabled *bool,
) {
	actorID, _ := auth.UserID(c)
	businessID := c.Param("id")
	targetUserID := c.Param("user_id")

	var err error
	switch action {
	case "blocked":
		err = h.store.BlockMember(c.Request.Context(), actorID, businessID, targetUserID)
	case "active":
		err = h.store.UnblockMember(c.Request.Context(), actorID, businessID, targetUserID)
	case "removed":
		err = h.store.RemoveMember(c.Request.Context(), actorID, businessID, targetUserID)
	case "restore":
		err = h.store.RestoreMember(
			c.Request.Context(), actorID, businessID, targetUserID, *monitoringEnabled,
		)
	default:
		badRequest(c, "invalid member lifecycle action")
		return
	}

	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"status": action})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "member not found")
	case errors.Is(err, store.ErrConflict):
		apiError(c, http.StatusConflict, ErrCodeConflict, "member state conflict", nil)
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	default:
		serverError(c, err)
	}
}
