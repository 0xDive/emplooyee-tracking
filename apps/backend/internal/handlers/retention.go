package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/retention"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

// RetentionHandler serves the owner's manual screenshot cleanup.
type RetentionHandler struct {
	store     *store.Store
	retention *retention.Service
}

// NewRetentionHandler wires the retention handler.
func NewRetentionHandler(s *store.Store, r *retention.Service) *RetentionHandler {
	return &RetentionHandler{store: s, retention: r}
}

func retentionMutationError(c *gin.Context, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "organization not found")
	default:
		serverError(c, err)
	}
	return true
}

// Preview returns the exact row count (and screenshot bytes) that would be
// removed by a retention window. It is read-only and is used before destructive
// retention reductions.
func (h *RetentionHandler) Preview(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	businessID := c.Param("id")

	allowed, err := h.store.HasBusinessPermission(
		c.Request.Context(),
		actorID,
		businessID,
		store.PermissionSettings,
	)
	if err != nil {
		serverError(c, err)
		return
	}
	if !allowed {
		forbidden(c, "insufficient permission")
		return
	}

	dataClass := c.Query("class")
	switch dataClass {
	case "activity", "screenshots", "browser", "keystrokes":
	default:
		badRequest(c, "class must be activity, screenshots, browser, or keystrokes")
		return
	}

	fromRaw := strings.TrimSpace(c.Query("from"))
	toRaw := strings.TrimSpace(c.Query("to"))
	daysRaw := strings.TrimSpace(c.Query("days"))
	if fromRaw != "" || toRaw != "" {
		if fromRaw == "" || toRaw == "" || daysRaw != "" {
			badRequest(c, "provide either days or both from and to")
			return
		}
		fromTs, fromErr := strconv.ParseInt(fromRaw, 10, 64)
		toTs, toErr := strconv.ParseInt(toRaw, 10, 64)
		if fromErr != nil || toErr != nil || fromTs < 0 || toTs <= fromTs {
			badRequest(c, "from and to must satisfy 0 <= from < to")
			return
		}
		preview, err := h.retention.PreviewRange(
			c.Request.Context(), businessID, dataClass, fromTs, toTs,
		)
		if err != nil {
			serverError(c, err)
			return
		}
		c.JSON(http.StatusOK, preview)
		return
	}

	days, err := strconv.Atoi(daysRaw)
	if err != nil || days < 0 || days > 3650 {
		badRequest(c, "days must be an integer between 0 and 3650")
		return
	}
	preview, err := h.retention.PreviewClass(c.Request.Context(), businessID, dataClass, days)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, preview)
}

type cleanupDataReq struct {
	DataClasses   []string `json:"data_classes"`
	OlderThanDays *int     `json:"older_than_days"`
	From          *int64   `json:"from"`
	To            *int64   `json:"to"`
}

// CleanupData runs one audited manual cleanup across selected structured data classes.
func (h *RetentionHandler) CleanupData(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	businessID := c.Param("id")

	allowed, err := h.store.HasBusinessPermission(
		c.Request.Context(),
		actorID,
		businessID,
		store.PermissionSettings,
	)
	if err != nil {
		serverError(c, err)
		return
	}
	if !allowed {
		forbidden(c, "insufficient permission")
		return
	}
	if retentionMutationError(c, h.store.EnsureBusinessMutable(c.Request.Context(), businessID)) {
		return
	}

	var req cleanupDataReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid cleanup request")
		return
	}
	daysMode := req.OlderThanDays != nil
	rangeMode := req.From != nil || req.To != nil
	if daysMode == rangeMode {
		badRequest(c, "provide either older_than_days or both from and to")
		return
	}
	if daysMode && (*req.OlderThanDays < 0 || *req.OlderThanDays > 3650) {
		badRequest(c, "older_than_days must be between 0 and 3650")
		return
	}
	if rangeMode {
		if req.From == nil || req.To == nil || *req.From < 0 || *req.To <= *req.From {
			badRequest(c, "from and to must satisfy 0 <= from < to")
			return
		}
	}
	if len(req.DataClasses) == 0 || len(req.DataClasses) > 4 {
		badRequest(c, "select between 1 and 4 data classes")
		return
	}

	seen := map[string]bool{}
	classes := make([]string, 0, len(req.DataClasses))
	previews := make([]retention.Preview, 0, len(req.DataClasses))
	for _, dataClass := range req.DataClasses {
		switch dataClass {
		case "activity", "screenshots", "browser", "keystrokes":
		default:
			badRequest(c, "unsupported cleanup data class")
			return
		}
		if seen[dataClass] {
			continue
		}
		seen[dataClass] = true
		classes = append(classes, dataClass)

		var preview retention.Preview
		var previewErr error
		if rangeMode {
			preview, previewErr = h.retention.PreviewRange(
				c.Request.Context(), businessID, dataClass, *req.From, *req.To,
			)
		} else {
			preview, previewErr = h.retention.PreviewClass(
				c.Request.Context(), businessID, dataClass, *req.OlderThanDays,
			)
		}
		if previewErr != nil {
			serverError(c, previewErr)
			return
		}
		previews = append(previews, preview)
	}

	details := map[string]any{
		"data_classes": classes,
		"preview":      previews,
	}
	if rangeMode {
		details["from"] = *req.From
		details["to"] = *req.To
		details["mode"] = "range"
	} else {
		details["older_than_days"] = *req.OlderThanDays
		details["mode"] = "older_than"
	}
	if err := h.store.RecordSettingsAudit(
		c.Request.Context(),
		actorID,
		businessID,
		"data.cleanup_requested",
		"organization",
		businessID,
		details,
	); err != nil {
		serverError(c, err)
		return
	}

	var result retention.MultiResult
	if rangeMode {
		result, err = h.retention.CleanupRanges(
			c.Request.Context(), businessID, classes, *req.From, *req.To,
		)
	} else {
		result, err = h.retention.CleanupClasses(
			c.Request.Context(), businessID, classes, *req.OlderThanDays,
		)
	}
	if retentionMutationError(c, err) {
		return
	}
	c.JSON(http.StatusOK, result)
}

// Cleanup deletes screenshots older than ?older_than_days=N for a business the caller
// owns, returning the count and bytes freed.
func (h *RetentionHandler) Cleanup(c *gin.Context) {
	ownerID, _ := auth.UserID(c)
	businessID := c.Param("id")

	allowed, err := h.store.HasBusinessPermission(c.Request.Context(), ownerID, businessID, store.PermissionSettings)
	if err != nil {
		serverError(c, err)
		return
	}
	if !allowed {
		forbidden(c, "insufficient permission")
		return
	}
	if retentionMutationError(c, h.store.EnsureBusinessMutable(c.Request.Context(), businessID)) {
		return
	}

	days, err := strconv.Atoi(c.Query("older_than_days"))
	if err != nil || days < 0 {
		badRequest(c, "older_than_days must be a non-negative integer")
		return
	}

	res, err := h.retention.CleanupBusiness(c.Request.Context(), businessID, days)
	if retentionMutationError(c, err) {
		return
	}
	c.JSON(http.StatusOK, res)
}
