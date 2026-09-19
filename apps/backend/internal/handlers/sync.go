package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/obs"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxBatch = 1000

type SyncHandler struct {
	store *store.Store
}

func NewSyncHandler(s *store.Store) *SyncHandler {
	return &SyncHandler{store: s}
}

type activityIn struct {
	ClientUUID  string  `json:"client_uuid"`
	Ts          int64   `json:"ts"`
	AppName     string  `json:"app_name"`
	WindowTitle *string `json:"window_title"`
	Pid         *int    `json:"pid"`
	DurationS   int     `json:"duration_s"`
	UpdatedAt   int64   `json:"updated_at"`
}

type keystrokeIn struct {
	ClientUUID string `json:"client_uuid"`
	TsBucket   int64  `json:"ts_bucket"`
	Count      int    `json:"count"`
	UpdatedAt  int64  `json:"updated_at"`
}

type browserIn struct {
	ClientUUID string  `json:"client_uuid"`
	Ts         int64   `json:"ts"`
	URL        string  `json:"url"`
	PageTitle  *string `json:"page_title"`
	Browser    *string `json:"browser"`
	DurationS  int     `json:"duration_s"`
	UpdatedAt  int64   `json:"updated_at"`
}

type syncBatchReq struct {
	DeviceID       string        `json:"device_id"`
	DeviceLabel    *string       `json:"device_label"`
	DeviceHostname *string       `json:"device_hostname"`
	DevicePlatform *string       `json:"device_platform"`
	DeviceArch     *string       `json:"device_arch"`
	AppVersion     *string       `json:"app_version"`
	BusinessID     *string       `json:"business_id"`
	Activity       []activityIn  `json:"activity"`
	Keystrokes     []keystrokeIn `json:"keystrokes"`
	Browser        []browserIn   `json:"browser"`
}

func (h *SyncHandler) Batch(c *gin.Context) {
	userID, _ := auth.UserID(c)

	var req syncBatchReq
	if err := c.ShouldBindJSON(&req); err != nil {
		badRequest(c, "invalid body")
		return
	}
	if _, err := uuid.Parse(req.DeviceID); err != nil {
		badRequest(c, "device_id must be a uuid")
		return
	}
	if len(req.Activity) > maxBatch || len(req.Keystrokes) > maxBatch || len(req.Browser) > maxBatch {
		badRequest(c, fmt.Sprintf("batch too large (max %d per kind)", maxBatch))
		return
	}

	act := make([]store.ActivityRow, 0, len(req.Activity))
	keys := make([]store.KeystrokeRow, 0, len(req.Keystrokes))
	brs := make([]store.BrowserRow, 0, len(req.Browser))
	accAct := make([]string, 0, len(req.Activity))
	accKeys := make([]string, 0, len(req.Keystrokes))
	accBrowser := make([]string, 0, len(req.Browser))

	for i, a := range req.Activity {
		if err := validUUID(a.ClientUUID); err != nil {
			badRequest(c, fmt.Sprintf("activity[%d]: %v", i, err))
			return
		}
		// app_name may be empty when managed policy keeps mandatory active/idle
		// duration but disables application identity collection.
		act = append(act, store.ActivityRow{
			ClientUUID: a.ClientUUID, Ts: a.Ts, AppName: a.AppName, WindowTitle: a.WindowTitle,
			Pid: a.Pid, DurationS: a.DurationS, ClientUpdatedAt: a.UpdatedAt,
		})
		accAct = append(accAct, a.ClientUUID)
	}
	for i, k := range req.Keystrokes {
		if err := validUUID(k.ClientUUID); err != nil {
			badRequest(c, fmt.Sprintf("keystrokes[%d]: %v", i, err))
			return
		}
		keys = append(keys, store.KeystrokeRow{
			ClientUUID: k.ClientUUID, TsBucket: k.TsBucket, Count: k.Count, ClientUpdatedAt: k.UpdatedAt,
		})
		accKeys = append(accKeys, k.ClientUUID)
	}
	for i, b := range req.Browser {
		if err := validUUID(b.ClientUUID); err != nil {
			badRequest(c, fmt.Sprintf("browser[%d]: %v", i, err))
			return
		}
		if b.URL == "" {
			badRequest(c, fmt.Sprintf("browser[%d]: url required", i))
			return
		}
		brs = append(brs, store.BrowserRow{
			ClientUUID: b.ClientUUID, Ts: b.Ts, URL: b.URL, PageTitle: b.PageTitle,
			Browser: b.Browser, DurationS: b.DurationS, ClientUpdatedAt: b.UpdatedAt,
		})
		accBrowser = append(accBrowser, b.ClientUUID)
	}

	businessID, err := h.store.ResolveBusinessForUser(c.Request.Context(), userID, req.BusinessID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		forbidden(c, "user belongs to no organization")
		return
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "not a member of that organization")
		return
	case errors.Is(err, store.ErrAmbiguousBusiness):
		badRequest(c, "multiple businesses: specify business_id")
		return
	case err != nil:
		serverError(c, err)
		return
	}

	monitoringEnabled, err := h.store.MembershipMonitoringEnabled(c.Request.Context(), userID, businessID)
	switch {
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
	case errors.Is(err, store.ErrNotFound), errors.Is(err, store.ErrMembershipUnavailable):
		forbidden(c, "membership is unavailable")
		return
	case err != nil:
		serverError(c, err)
		return
	}
	if !monitoringEnabled {
		forbidden(c, "monitoring is disabled for this membership")
		return
	}

	meta := store.DeviceMetadata{
		Label:      req.DeviceLabel,
		Hostname:   req.DeviceHostname,
		Platform:   req.DevicePlatform,
		Arch:       req.DeviceArch,
		AppVersion: req.AppVersion,
	}
	if err := h.store.SyncBatch(c.Request.Context(), userID, businessID, req.DeviceID, meta, act, keys, brs); err != nil {
		switch {
		case errors.Is(err, store.ErrMemberBlocked):
			apiError(c, http.StatusForbidden, ErrCodeMemberBlocked, "organization access is suspended", nil)
		case errors.Is(err, store.ErrMemberRemoved):
			apiError(c, http.StatusForbidden, ErrCodeMemberRemoved, "organization membership was removed", nil)
		case errors.Is(err, store.ErrOrganizationArchived):
			apiError(c, http.StatusConflict, ErrCodeOrganizationArchived, "organization is archived", nil)
		case errors.Is(err, store.ErrOrganizationDeletionPending):
			apiError(c, http.StatusConflict, ErrCodeOrganizationDeletionPending, "organization deletion is pending", nil)
		case errors.Is(err, store.ErrMembershipUnavailable):
			apiError(c, http.StatusForbidden, ErrCodePermissionDenied, "monitoring is disabled for this membership", nil)
		case errors.Is(err, store.ErrDeviceRevoked):
			apiError(c, http.StatusForbidden, ErrCodeDeviceRevoked, "this device was revoked by the administrator", nil)
		case errors.Is(err, store.ErrDeviceLimitReached):
			apiError(c, http.StatusConflict, ErrCodeDeviceLimitReached, "device limit reached", nil)
		case errors.Is(err, store.ErrForbidden):
			forbidden(c, "device id belongs to another account or organization")
		default:
			serverError(c, err)
		}
		return
	}

	obs.Info("sync batch accepted",
		"user", userID, "device", req.DeviceID,
		"activity", len(accAct), "keystrokes", len(accKeys), "browser", len(accBrowser))

	c.JSON(http.StatusOK, gin.H{
		"accepted": gin.H{
			"activity":   accAct,
			"keystrokes": accKeys,
			"browser":    accBrowser,
		},
	})
}

func validUUID(s string) error {
	if _, err := uuid.Parse(s); err != nil {
		return errors.New("client_uuid must be a uuid")
	}
	return nil
}
