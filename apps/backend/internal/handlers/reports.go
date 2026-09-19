package handlers

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/filestore"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

type ReportsHandler struct {
	store *store.Store
	files *filestore.Store
}

func NewReportsHandler(s *store.Store, files *filestore.Store) *ReportsHandler {
	return &ReportsHandler{store: s, files: files}
}

// Roster returns the current organization roster. "Today" follows the
// organization timezone instead of the server timezone.
func (h *ReportsHandler) Roster(c *gin.Context) {
	viewerID, _ := auth.UserID(c)
	businessID := strings.TrimSpace(c.Query("business_id"))
	if businessID == "" {
		badRequest(c, "business_id is required")
		return
	}

	role, err := h.store.MembershipRole(c.Request.Context(), viewerID, businessID)
	if err != nil || !storeRoleCanViewReports(role) {
		forbidden(c, "insufficient permission")
		return
	}
	business, err := h.store.GetBusiness(c.Request.Context(), businessID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			notFound(c, "organization not found")
		} else {
			serverError(c, err)
		}
		return
	}
	if (business.ArchivedAt != nil || business.DeletionScheduledAt != nil) &&
		role != store.RoleOwner && role != store.RoleAdmin {
		forbidden(c, "archived organization reports require owner or admin access")
		return
	}

	location, err := time.LoadLocation(business.Timezone)
	if err != nil {
		location = time.UTC
	}
	now := time.Now().In(location)
	localMidnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, location)
	ydayStart := localMidnight.AddDate(0, 0, -1).Unix()
	dayStart := localMidnight.Unix()
	dayEnd := localMidnight.AddDate(0, 0, 1).Unix()

	roster, err := h.store.Roster(
		c.Request.Context(), businessID, ydayStart, dayStart, dayEnd,
	)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"employees": roster})
}

func storeRoleCanViewReports(role store.BusinessRole) bool {
	return role == store.RoleOwner || role == store.RoleAdmin || role == store.RoleManager
}

func (h *ReportsHandler) Activity(c *gin.Context) {
	viewerID, businessID, empID, from, to, ok := h.scope(c)
	if !ok {
		return
	}
	samples, breakdown, err := h.store.ActivityReportInBusiness(
		c.Request.Context(), empID, businessID, from, to,
	)
	if err != nil {
		serverError(c, err)
		return
	}
	_ = viewerID
	c.JSON(http.StatusOK, gin.H{"samples": samples, "breakdown": breakdown})
}

func (h *ReportsHandler) Keystrokes(c *gin.Context) {
	_, businessID, empID, from, to, ok := h.scope(c)
	if !ok {
		return
	}
	buckets, err := h.store.KeystrokesReportInBusiness(
		c.Request.Context(), empID, businessID, from, to,
	)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"buckets": buckets})
}

func (h *ReportsHandler) Browser(c *gin.Context) {
	_, businessID, empID, from, to, ok := h.scope(c)
	if !ok {
		return
	}
	visits, err := h.store.BrowserReportInBusiness(
		c.Request.Context(), empID, businessID, from, to,
	)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"visits": visits})
}

func (h *ReportsHandler) Screenshots(c *gin.Context) {
	_, businessID, empID, from, to, ok := h.scope(c)
	if !ok {
		return
	}
	limit := clampInt(c.Query("limit"), 50, 1, 200)
	offset := clampInt(c.Query("offset"), 0, 0, 1<<31)
	shots, err := h.store.ScreenshotsReportInBusiness(
		c.Request.Context(), empID, businessID, from, to, limit, offset,
	)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"screenshots": shots, "limit": limit, "offset": offset})
}

func (h *ReportsHandler) ScreenshotImage(c *gin.Context) {
	viewerID, _ := auth.UserID(c)
	businessID := strings.TrimSpace(c.Query("business_id"))
	if businessID == "" {
		badRequest(c, "business_id is required")
		return
	}

	relPath, err := h.store.ScreenshotPathInBusiness(
		c.Request.Context(), viewerID, businessID, c.Param("client_uuid"),
	)
	if errors.Is(err, store.ErrNotFound) {
		notFound(c, "not found")
		return
	}
	if err != nil {
		serverError(c, err)
		return
	}

	file, err := h.files.Open(relPath)
	if err != nil {
		notFound(c, "not found")
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		serverError(c, err)
		return
	}
	c.DataFromReader(http.StatusOK, info.Size(), "image/webp", file, nil)
}

// scope validates an explicit business/member pair and the report window.
// Former-member history is available only to Owner/Admin.
func (h *ReportsHandler) scope(
	c *gin.Context,
) (viewerID, businessID, empID string, from, to int64, ok bool) {
	viewerID, _ = auth.UserID(c)
	businessID = strings.TrimSpace(c.Query("business_id"))
	empID = c.Param("id")
	if businessID == "" {
		badRequest(c, "business_id is required")
		return
	}

	if _, err := h.store.ReportAccessInBusiness(
		c.Request.Context(), viewerID, empID, businessID,
	); err != nil {
		if errors.Is(err, store.ErrForbidden) || errors.Is(err, store.ErrNotFound) {
			forbidden(c, "insufficient permission")
		} else {
			serverError(c, err)
		}
		return
	}

	from = parseInt64(c.Query("from"), 0)
	to = parseInt64(c.Query("to"), time.Now().Unix()+1)
	if to <= from {
		badRequest(c, "to must be greater than from")
		return
	}
	ok = true
	return
}

func parseInt64(value string, def int64) int64 {
	if value == "" {
		return def
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return def
	}
	return parsed
}

func clampInt(value string, def, lo, hi int) int {
	n := def
	if value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			n = parsed
		}
	}
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}
