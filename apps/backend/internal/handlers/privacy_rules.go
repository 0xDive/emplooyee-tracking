package handlers

import (
	"errors"
	"net/http"
	"strings"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

type createPrivacyRuleReq struct {
	Kind      string `json:"kind"`
	MatchType string `json:"match_type"`
	Pattern   string `json:"pattern"`
}

type updatePrivacyRuleReq struct {
	Kind      *string `json:"kind"`
	MatchType *string `json:"match_type"`
	Pattern   *string `json:"pattern"`
	Enabled   *bool   `json:"enabled"`
}

func (h *OwnerHandler) ListPrivacyRules(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	rules, err := h.store.ListPrivacyRules(c.Request.Context(), actorID, c.Param("id"))
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"rules": rules})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "organization not found")
	default:
		serverError(c, err)
	}
}

func (h *OwnerHandler) CreatePrivacyRule(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	var req createPrivacyRuleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	req.Kind = strings.TrimSpace(req.Kind)
	req.MatchType = strings.TrimSpace(req.MatchType)
	req.Pattern = strings.TrimSpace(req.Pattern)

	rule, err := h.store.CreatePrivacyRule(
		c.Request.Context(), actorID, c.Param("id"),
		req.Kind, req.MatchType, req.Pattern,
	)
	h.writePrivacyRuleResult(c, rule, err, http.StatusCreated)
}

func (h *OwnerHandler) UpdatePrivacyRule(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	var req updatePrivacyRuleReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if req.Kind != nil {
		value := strings.TrimSpace(*req.Kind)
		req.Kind = &value
	}
	if req.MatchType != nil {
		value := strings.TrimSpace(*req.MatchType)
		req.MatchType = &value
	}
	if req.Pattern != nil {
		value := strings.TrimSpace(*req.Pattern)
		req.Pattern = &value
	}
	if req.Kind == nil && req.MatchType == nil && req.Pattern == nil && req.Enabled == nil {
		badRequest(c, "at least one field is required")
		return
	}

	rule, err := h.store.UpdatePrivacyRule(
		c.Request.Context(), actorID, c.Param("id"), c.Param("rule_id"),
		req.Kind, req.MatchType, req.Pattern, req.Enabled,
	)
	h.writePrivacyRuleResult(c, rule, err, http.StatusOK)
}

func (h *OwnerHandler) DeletePrivacyRule(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	err := h.store.DeletePrivacyRule(
		c.Request.Context(), actorID, c.Param("id"), c.Param("rule_id"),
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"status": "deleted"})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "privacy rule not found")
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	default:
		serverError(c, err)
	}
}

func (h *OwnerHandler) writePrivacyRuleResult(
	c *gin.Context,
	rule store.PrivacyRule,
	err error,
	successStatus int,
) {
	switch {
	case err == nil:
		c.JSON(successStatus, gin.H{"rule": rule})
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "privacy rule not found")
	case errors.Is(err, store.ErrConflict):
		badRequest(c, "invalid privacy rule")
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	default:
		serverError(c, err)
	}
}
