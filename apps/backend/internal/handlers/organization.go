package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

type updateOrganizationReq struct {
	Name         *string         `json:"name"`
	Kind         *string         `json:"kind"`
	Timezone     *string         `json:"timezone"`
	WeekStartsOn json.RawMessage `json:"week_starts_on"`
}

func (h *OwnerHandler) GetOrganization(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	access, err := h.store.GetOrganizationForActor(c.Request.Context(), actorID, c.Param("id"))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, access)
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "organization not found")
	default:
		serverError(c, err)
	}
}

func (h *OwnerHandler) UpdateOrganization(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	var req updateOrganizationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}

	patch := store.OrganizationPatch{
		Name:     req.Name,
		Kind:     req.Kind,
		Timezone: req.Timezone,
	}
	if req.WeekStartsOn != nil {
		patch.WeekStartsOnSet = true
		if string(req.WeekStartsOn) != "null" {
			var day int
			if err := json.Unmarshal(req.WeekStartsOn, &day); err != nil {
				badRequest(c, "week_starts_on must be null or an integer from 0 to 6")
				return
			}
			patch.WeekStartsOn = &day
		}
	}

	business, err := h.store.UpdateOrganization(
		c.Request.Context(), actorID, c.Param("id"), patch,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, business)
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	case errors.Is(err, store.ErrConflict):
		badRequest(c, "invalid organization settings")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "organization not found")
	default:
		serverError(c, err)
	}
}

type createOrganizationReq struct {
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	Timezone     string `json:"timezone"`
	WeekStartsOn *int   `json:"week_starts_on"`
}

func (h *OwnerHandler) CreateOrganization(c *gin.Context) {
	userID, _ := auth.UserID(c)
	var req createOrganizationReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.Kind = strings.ToLower(strings.TrimSpace(req.Kind))
	req.Timezone = strings.TrimSpace(req.Timezone)

	if req.Kind == "" {
		// Preserve existing persona-based onboarding default only at creation time.
		req.Kind = "team"
		if u, err := h.store.GetUserByID(c.Request.Context(), userID); err == nil && u.AccountType == "parent" {
			req.Kind = "family"
		}
	}

	business, err := h.store.CreateBusinessConfigured(
		c.Request.Context(), userID, req.Name, req.Kind, req.Timezone, req.WeekStartsOn,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusCreated, business)
	case errors.Is(err, store.ErrConflict):
		badRequest(c, "invalid organization settings")
	default:
		serverError(c, err)
	}
}
