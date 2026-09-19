package handlers

import (
	"errors"
	"net/http"
	"strconv"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

type updateDefaultMonitoringReq struct {
	Enabled       *bool `json:"enabled"`
	ApplyExisting bool  `json:"apply_existing"`
}

func (h *OwnerHandler) DefaultMonitoringImpact(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	enabled, err := strconv.ParseBool(c.Query("enabled"))
	if err != nil {
		badRequest(c, "enabled must be true or false")
		return
	}
	count, err := h.store.DefaultMonitoringImpact(
		c.Request.Context(), actorID, c.Param("id"), enabled,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"affected_count": count})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "organization not found")
	default:
		serverError(c, err)
	}
}

func (h *OwnerHandler) UpdateDefaultMonitoring(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	var req updateDefaultMonitoringReq
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		badRequest(c, "enabled must be a boolean")
		return
	}

	count, err := h.store.UpdateDefaultMonitoring(
		c.Request.Context(),
		actorID,
		c.Param("id"),
		*req.Enabled,
		req.ApplyExisting,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{
			"status":         "ok",
			"affected_count": count,
		})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "organization not found")
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	default:
		serverError(c, err)
	}
}
