package handlers

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/filestore"
	"actilens/backend/internal/store"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const maxScreenshotBytes = 200 * 1024

type ScreenshotHandler struct {
	store *store.Store
	files *filestore.Store
}

func NewScreenshotHandler(s *store.Store, files *filestore.Store) *ScreenshotHandler {
	return &ScreenshotHandler{store: s, files: files}
}

func (h *ScreenshotHandler) Upload(c *gin.Context) {
	userID, _ := auth.UserID(c)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxScreenshotBytes+16*1024)

	clientUUID := c.PostForm("client_uuid")
	deviceID := c.PostForm("device_id")
	if _, err := uuid.Parse(clientUUID); err != nil {
		badRequest(c, "client_uuid must be a uuid")
		return
	}
	if _, err := uuid.Parse(deviceID); err != nil {
		badRequest(c, "device_id must be a uuid")
		return
	}
	ts, err := strconv.ParseInt(c.PostForm("ts"), 10, 64)
	if err != nil {
		badRequest(c, "ts must be an integer")
		return
	}
	updatedAt, err := strconv.ParseInt(c.PostForm("updated_at"), 10, 64)
	if err != nil {
		badRequest(c, "updated_at must be an integer")
		return
	}
	var businessID *string
	if v := c.PostForm("business_id"); v != "" {
		businessID = &v
	}
	var captureGroupID *string
	if v := strings.TrimSpace(c.PostForm("capture_group_id")); v != "" {
		if _, err := uuid.Parse(v); err != nil {
			badRequest(c, "capture_group_id must be a uuid")
			return
		}
		captureGroupID = &v
	}

	fileHeader, err := c.FormFile("image")
	if err != nil {
		badRequest(c, "image file part is required")
		return
	}
	if fileHeader.Size > maxScreenshotBytes {
		badRequest(c, "image exceeds size limit")
		return
	}
	f, err := fileHeader.Open()
	if err != nil {
		serverError(c, err)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxScreenshotBytes+1))
	if err != nil {
		serverError(c, err)
		return
	}
	if len(data) > maxScreenshotBytes {
		badRequest(c, "image exceeds size limit")
		return
	}
	if !strings.HasPrefix(http.DetectContentType(data), "image/") {
		badRequest(c, "uploaded file is not an image")
		return
	}

	bizID, err := h.resolveBusiness(c, userID, businessID)
	if err != nil {
		return
	}

	monitoringEnabled, err := h.store.MembershipMonitoringEnabled(c.Request.Context(), userID, bizID)
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

	// Screenshot uploads must obey the same device revocation policy as batch sync.
	if err := h.store.TouchDevice(c.Request.Context(), userID, bizID, deviceID, store.DeviceMetadata{}); err != nil {
		switch {
		case errors.Is(err, store.ErrDeviceRevoked):
			apiError(c, http.StatusForbidden, ErrCodeDeviceRevoked, "this device was revoked by the administrator", nil)
		case errors.Is(err, store.ErrDeviceLimitReached):
			apiError(c, http.StatusConflict, ErrCodeDeviceLimitReached, "device limit reached", nil)
		case errors.Is(err, store.ErrForbidden):
			forbidden(c, "device id belongs to another account")
		default:
			serverError(c, err)
		}
		return
	}

	// Old desktop versions may still upload after screenshots are disabled. Treat
	// such a row as acknowledged-but-discarded so it is never stored and the old
	// agent does not retry forever.
	policy, err := h.store.PolicyForUserInBusiness(c.Request.Context(), userID, bizID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrMemberBlocked):
			apiError(c, http.StatusForbidden, ErrCodeMemberBlocked, "organization access is suspended", nil)
		case errors.Is(err, store.ErrMemberRemoved):
			apiError(c, http.StatusForbidden, ErrCodeMemberRemoved, "organization membership was removed", nil)
		default:
			serverError(c, err)
		}
		return
	}
	if policy != nil && !policy.CollectScreenshots {
		c.JSON(http.StatusOK, gin.H{"accepted": []string{clientUUID}})
		return
	}

	// Write the file first, then record metadata so a row never points at a
	// missing file. If metadata cannot commit, the just-written blob is removed
	// immediately so purge races cannot leave orphan files behind.
	relPath, err := h.files.Write(bizID, userID, ts, clientUUID, data)
	if err != nil {
		serverError(c, err)
		return
	}

	row := store.ScreenshotRow{
		ClientUUID:      clientUUID,
		DeviceID:        deviceID,
		Ts:              ts,
		FilePath:        relPath,
		ByteSize:        len(data),
		Width:           optInt(c.PostForm("width")),
		Height:          optInt(c.PostForm("height")),
		DisplayID:       optInt(c.PostForm("display_id")),
		CaptureGroupID:  captureGroupID,
		ClientUpdatedAt: updatedAt,
	}
	if err := h.store.UpsertScreenshot(c.Request.Context(), userID, bizID, row); err != nil {
		if cleanupErr := h.files.Remove(relPath); cleanupErr != nil {
			serverError(c, cleanupErr)
			return
		}
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
		case errors.Is(err, store.ErrCollectionDisabled):
			// Policy may have changed after the pre-write check. The blob was already
			// removed above; acknowledge the client UUID so an old agent does not retry.
			c.JSON(http.StatusOK, gin.H{"accepted": []string{clientUUID}})
		default:
			serverError(c, err)
		}
		return
	}

	c.JSON(http.StatusOK, gin.H{"accepted": []string{clientUUID}})
}

func (h *ScreenshotHandler) resolveBusiness(c *gin.Context, userID string, explicit *string) (string, error) {
	bizID, err := h.store.ResolveBusinessForUser(c.Request.Context(), userID, explicit)
	switch {
	case errors.Is(err, store.ErrNotFound):
		forbidden(c, "user belongs to no organization")
	case errors.Is(err, store.ErrForbidden):
		forbidden(c, "not a member of that organization")
	case errors.Is(err, store.ErrAmbiguousBusiness):
		badRequest(c, "multiple businesses: specify business_id")
	case err != nil:
		serverError(c, err)
	}
	return bizID, err
}

func optInt(s string) *int {
	if s == "" {
		return nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return nil
	}
	return &v
}
