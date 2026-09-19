package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
)

// OwnerHandler serves business + employee management for owners.
type OwnerHandler struct {
	store                     *store.Store
	recommendedDesktopVersion string
}

// NewOwnerHandler wires the owner handler. The optional recommended version keeps
// tests and older construction sites source-compatible.
func NewOwnerHandler(s *store.Store, recommendedVersion ...string) *OwnerHandler {
	version := ""
	if len(recommendedVersion) > 0 {
		version = strings.TrimSpace(recommendedVersion[0])
	}
	return &OwnerHandler{store: s, recommendedDesktopVersion: version}
}

func compareSemver(left, right string) (int, bool) {
	parse := func(value string) ([3]int, bool) {
		var out [3]int
		value = strings.TrimPrefix(strings.TrimSpace(value), "v")
		if i := strings.IndexAny(value, "-+"); i >= 0 {
			value = value[:i]
		}
		parts := strings.Split(value, ".")
		if len(parts) < 2 || len(parts) > 3 {
			return out, false
		}
		for i := 0; i < len(parts); i++ {
			n, err := strconv.Atoi(parts[i])
			if err != nil || n < 0 {
				return out, false
			}
			out[i] = n
		}
		return out, true
	}
	a, okA := parse(left)
	b, okB := parse(right)
	if !okA || !okB {
		return 0, false
	}
	for i := 0; i < len(a); i++ {
		if a[i] < b[i] {
			return -1, true
		}
		if a[i] > b[i] {
			return 1, true
		}
	}
	return 0, true
}

func (h *OwnerHandler) deviceVersionStatus(version string) string {
	if h.recommendedDesktopVersion == "" || strings.TrimSpace(version) == "" {
		return "unknown"
	}
	cmp, ok := compareSemver(version, h.recommendedDesktopVersion)
	if !ok {
		return "unknown"
	}
	if cmp < 0 {
		return "outdated"
	}
	return "current"
}

func (h *OwnerHandler) annotateDeviceVersions(devices []store.Device) {
	for i := range devices {
		devices[i].VersionStatus = h.deviceVersionStatus(devices[i].AppVersion)
	}
}

type createBusinessReq struct {
	Name string `json:"name"`
}

// CreateBusiness creates a business owned by the caller.
func (h *OwnerHandler) CreateBusiness(c *gin.Context) {
	userID, _ := auth.UserID(c)
	var req createBusinessReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		badRequest(c, "name is required")
		return
	}
	// kind mirrors the owner's persona: a parent gets a 'family' business.
	kind := "team"
	if u, err := h.store.GetUserByID(c.Request.Context(), userID); err == nil && u.AccountType == "parent" {
		kind = "family"
	}
	biz, err := h.store.CreateBusiness(c.Request.Context(), userID, req.Name, kind)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusCreated, biz)
}

// ListMine returns the businesses the caller owns.
func (h *OwnerHandler) ListMine(c *gin.Context) {
	userID, _ := auth.UserID(c)
	list, err := h.store.ListBusinessesOwnedBy(c.Request.Context(), userID)
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"businesses": list})
}

type createEmployeeReq struct {
	Email       string  `json:"email"`    // optional if username is set
	Username    string  `json:"username"` // optional if email is set
	Password    string  `json:"password"`
	DisplayName string  `json:"display_name"`
	BusinessID  *string `json:"business_id"` // optional: omit to use/auto-create the owner's business
}

// usernameRe constrains member usernames: lowercase letters, digits, underscores.
var usernameRe = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

// CreateEmployee creates a pre-provisioned employee account. With no business_id the
// owner's first business is used, auto-creating one if the owner has none.
func (h *OwnerHandler) CreateEmployee(c *gin.Context) {
	h.createEmployee(c, nil)
}

// CreateOrganizationMember is the canonical explicitly-scoped member creation
// endpoint. The legacy /employees endpoint remains temporarily for compatibility
// but also requires business_id and never chooses/creates an organization implicitly.
func (h *OwnerHandler) CreateOrganizationMember(c *gin.Context) {
	businessID := strings.TrimSpace(c.Param("id"))
	h.createEmployee(c, &businessID)
}

func (h *OwnerHandler) createEmployee(c *gin.Context, scopedBusinessID *string) {
	userID, _ := auth.UserID(c)
	var req createEmployeeReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if scopedBusinessID != nil {
		req.BusinessID = scopedBusinessID
	}
	if req.BusinessID == nil || strings.TrimSpace(*req.BusinessID) == "" {
		badRequest(c, "business_id is required")
		return
	}
	businessID := strings.TrimSpace(*req.BusinessID)
	req.BusinessID = &businessID

	req.Email = strings.TrimSpace(req.Email)
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	req.DisplayName = strings.TrimSpace(req.DisplayName)
	if req.DisplayName == "" {
		badRequest(c, "display_name is required")
		return
	}
	if req.Email == "" && req.Username == "" {
		badRequest(c, "an email or username is required")
		return
	}
	if req.Username != "" && !usernameRe.MatchString(req.Username) {
		badRequest(c, "username must be 3-32 chars: lowercase letters, digits, underscores")
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
	emp, biz, err := h.store.CreateEmployee(
		c.Request.Context(), userID, req.BusinessID,
		req.Email, req.Username, hash, req.DisplayName,
	)
	switch {
	case errors.Is(err, store.ErrConflict):
		apiError(c, http.StatusConflict, ErrCodeIdentifierTaken, "that email or username is already taken", nil)
	case errors.Is(err, store.ErrNotFound):
		notFound(c, "organization not found")
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "insufficient permission")
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
	case err != nil:
		serverError(c, err)
	default:
		c.JSON(http.StatusCreated, gin.H{"employee": emp, "business": biz})
	}
}

// ListEmployees returns the roster of a business the caller owns.
func (h *OwnerHandler) ListEmployees(c *gin.Context) {
	if !h.requireBusinessPermission(c, c.Param("id"), store.PermissionReports) {
		return
	}
	list, err := h.store.ListEmployees(c.Request.Context(), c.Param("id"))
	if err != nil {
		serverError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"employees": list})
}

// UpdateSettings updates a business's capture policy. Only the keys present in the
// body are changed; screenshot_retention_days accepts null ("keep forever").
func (h *OwnerHandler) UpdateSettings(c *gin.Context) {
	actorID, _ := auth.UserID(c)
	businessID := c.Param("id")
	if !h.requireBusinessPermission(c, businessID, store.CapabilitySettingsManage) {
		return
	}

	var body map[string]json.RawMessage
	if err := c.ShouldBindJSON(&body); err != nil {
		badRequest(c, "invalid body")
		return
	}
	fields := map[string]any{}

	for _, key := range []string{
		"collect_app_activity",
		"collect_window_titles",
		"collect_screenshots",
		"collect_browser_activity",
		"collect_keystroke_counts",
	} {
		if raw, ok := body[key]; ok {
			var value bool
			if json.Unmarshal(raw, &value) != nil {
				badRequest(c, key+" must be a boolean")
				return
			}
			fields[key] = value
		}
	}

	if raw, ok := body["screenshot_capture_scope"]; ok {
		var value string
		if json.Unmarshal(raw, &value) != nil {
			badRequest(c, "screenshot_capture_scope must be a string")
			return
		}
		switch value {
		case "active_window", "active_display", "all_displays":
			fields["screenshot_capture_scope"] = value
			// Compatibility for older agents until the desktop migration is complete.
			if value == "active_window" {
				fields["screenshot_mode"] = "privacy"
			} else {
				fields["screenshot_mode"] = "normal"
			}
		default:
			badRequest(c, "invalid screenshot_capture_scope")
			return
		}
	}

	for key, bounds := range map[string][2]int{
		"screenshot_interval_s":  {30, 86400},
		"idle_threshold_s":       {30, 3600},
		"activity_retention_days": {1, 3650},
		"browser_retention_days":  {1, 3650},
		"keystroke_retention_days": {1, 3650},
		"enrollment_token_ttl_s": {300, 604800},
	} {
		if raw, ok := body[key]; ok {
			var value int
			if json.Unmarshal(raw, &value) != nil || value < bounds[0] || value > bounds[1] {
				badRequest(c, key+" is outside the allowed range")
				return
			}
			fields[key] = value
		}
	}

	for _, key := range []string{"screenshot_retention_days", "audit_retention_days", "device_limit"} {
		raw, ok := body[key]
		if !ok {
			continue
		}
		if string(raw) == "null" {
			fields[key] = nil
			continue
		}
		var value int
		if json.Unmarshal(raw, &value) != nil || value <= 0 {
			badRequest(c, key+" must be a positive integer or null")
			return
		}
		if key == "device_limit" && value > 1000 {
			badRequest(c, "device_limit must be at most 1000 or null")
			return
		}
		if key != "device_limit" && value > 3650 {
			badRequest(c, key+" must be at most 3650 days or null")
			return
		}
		fields[key] = value
	}

	// Legacy privacy-app compatibility stays writable until the rule UI is migrated.
	if raw, ok := body["screenshot_skip_apps"]; ok {
		var apps []string
		if json.Unmarshal(raw, &apps) != nil || len(apps) > 100 {
			badRequest(c, "screenshot_skip_apps must be an array of up to 100 app names")
			return
		}
		cleaned := make([]string, 0, len(apps))
		for _, app := range apps {
			app = strings.TrimSpace(app)
			if app == "" || len(app) > 200 {
				badRequest(c, "screenshot_skip_apps entries must be non-empty names up to 200 characters")
				return
			}
			cleaned = append(cleaned, app)
		}
		fields["screenshot_skip_apps"] = cleaned
	}

	confirmRetentionReduction := false
	if raw, ok := body["confirm_retention_reduction"]; ok {
		if json.Unmarshal(raw, &confirmRetentionReduction) != nil {
			badRequest(c, "confirm_retention_reduction must be a boolean")
			return
		}
	}
	if hasRetentionFields(fields) {
		current, err := h.store.GetBusiness(c.Request.Context(), businessID)
		if err != nil {
			if errors.Is(err, store.ErrNotFound) {
				notFound(c, "organization not found")
			} else {
				serverError(c, err)
			}
			return
		}
		if retentionReductionRequested(fields, current) && !confirmRetentionReduction {
			apiError(
				c,
				http.StatusConflict,
				"retention_confirmation_required",
				"reducing retention requires an impact preview and explicit confirmation",
				nil,
			)
			return
		}
	}

	err := h.store.UpdateBusinessSettingsAudited(
		c.Request.Context(), actorID, businessID, fields,
	)
	switch {
	case err == nil:
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
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

func hasRetentionFields(fields map[string]any) bool {
	for _, key := range []string{
		"activity_retention_days",
		"screenshot_retention_days",
		"browser_retention_days",
		"keystroke_retention_days",
		"audit_retention_days",
	} {
		if _, ok := fields[key]; ok {
			return true
		}
	}
	return false
}

func retentionReductionRequested(fields map[string]any, current store.Business) bool {
	for key, currentDays := range map[string]int{
		"activity_retention_days":  current.ActivityRetentionDays,
		"browser_retention_days":   current.BrowserRetentionDays,
		"keystroke_retention_days": current.KeystrokeRetentionDays,
	} {
		if raw, ok := fields[key]; ok {
			if next, ok := raw.(int); ok && next < currentDays {
				return true
			}
		}
	}

	for key, currentDays := range map[string]*int{
		"screenshot_retention_days": current.ScreenshotRetentionDays,
		"audit_retention_days":      current.AuditRetentionDays,
	} {
		raw, ok := fields[key]
		if !ok || raw == nil {
			continue // finite -> forever is an increase, not a destructive reduction
		}
		next, ok := raw.(int)
		if !ok {
			continue
		}
		if currentDays == nil || next < *currentDays {
			return true
		}
	}
	return false
}

// Policy returns the capture policy for the authenticated user's business, or
// {managed:false} when the user has no single business (standalone → local defaults).
func (h *OwnerHandler) Policy(c *gin.Context) {
	userID, _ := auth.UserID(c)
	businessID := strings.TrimSpace(c.Query("business_id"))

	var (
		p   *store.CapturePolicy
		err error
	)
	if businessID != "" {
		p, err = h.store.PolicyForUserInBusiness(c.Request.Context(), userID, businessID)
	} else {
		p, err = h.store.PolicyForUser(c.Request.Context(), userID)
	}
	switch {
	case errors.Is(err, store.ErrAmbiguousBusiness):
		c.JSON(http.StatusOK, gin.H{"managed": false, "requires_business_id": true})
		return
	case errors.Is(err, store.ErrMemberBlocked):
		apiError(c, http.StatusForbidden, ErrCodeMemberBlocked, "organization access is suspended", nil)
		return
	case errors.Is(err, store.ErrMemberRemoved):
		apiError(c, http.StatusForbidden, ErrCodeMemberRemoved, "organization membership was removed", nil)
		return
	case errors.Is(err, store.ErrOrganizationArchived):
		apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
		return
	case errors.Is(err, store.ErrOrganizationDeletionPending):
		apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
		return
	case err != nil:
		serverError(c, err)
		return
	}
	if p == nil {
		c.JSON(http.StatusOK, gin.H{"managed": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"managed":                           true,
		"business_id":                       p.BusinessID,
		"archived":                          p.Archived,
		"default_member_monitoring_enabled": p.DefaultMemberMonitoringEnabled,
		"collect_app_activity":               p.CollectAppActivity,
		"collect_window_titles":              p.CollectWindowTitles,
		"collect_screenshots":                p.CollectScreenshots,
		"collect_browser_activity":           p.CollectBrowserActivity,
		"collect_keystroke_counts":           p.CollectKeystrokeCounts,
		"screenshot_interval_s":              p.ScreenshotIntervalS,
		"screenshot_capture_scope":           p.ScreenshotCaptureScope,
		"idle_threshold_s":                   p.IdleThresholdS,
		"screenshot_retention_days":          p.ScreenshotRetentionDays,
		"kind":                               p.Kind,
		// compatibility for old desktop builds
		"allow_employee_override": false,
		"screenshot_mode":         p.ScreenshotMode,
		"screenshot_skip_apps":    p.ScreenshotSkipApps,
		"privacy_rules":           p.PrivacyRules,
	})
}

// requireBusinessPermission enforces the server-side RBAC matrix for a business.
func (h *OwnerHandler) requireBusinessPermission(c *gin.Context, businessID string, permission store.BusinessPermission) bool {
	userID, _ := auth.UserID(c)
	ok, err := h.store.HasBusinessPermission(c.Request.Context(), userID, businessID, permission)
	if err != nil {
		serverError(c, err)
		return false
	}
	if !ok {
		forbidden(c, "insufficient permission")
		return false
	}
	return true
}
