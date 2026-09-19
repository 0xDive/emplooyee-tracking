package handlers

import (
	"net/http"

	"actilens/backend/internal/obs"

	"github.com/getsentry/sentry-go"
	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"
)

const (
	ErrCodeValidation                 = "validation_error"
	ErrCodeAuthenticationRequired     = "authentication_required"
	ErrCodeReauthRequired             = "reauth_required"
	ErrCodeMFARequired                = "mfa_required"
	ErrCodePermissionDenied           = "permission_denied"
	ErrCodeNotFound                   = "not_found"
	ErrCodeConflict                   = "conflict"
	ErrCodeMemberBlocked              = "member_blocked"
	ErrCodeMemberRemoved              = "member_removed"
	ErrCodeOrganizationArchived       = "organization_archived"
	ErrCodeOrganizationDeletionPending = "organization_deletion_pending"
	ErrCodeOrganizationRequired       = "organization_required"
	ErrCodeDeviceLimitReached         = "device_limit_reached"
	ErrCodeDeviceRevoked              = "device_revoked"
	ErrCodeIdentifierTaken            = "identifier_taken"
	ErrCodeSessionRevoked             = "session_revoked"
	ErrCodeRateLimited                = "rate_limited"
	ErrCodeInternal                   = "internal_error"
)

func apiError(c *gin.Context, status int, code, msg string, details gin.H) {
	body := gin.H{
		"error": msg, // compatibility for older web/desktop clients
		"code":  code,
	}
	if details != nil {
		body["details"] = details
	}
	c.JSON(status, body)
}

func badRequest(c *gin.Context, msg string) {
	apiError(c, http.StatusBadRequest, ErrCodeValidation, msg, nil)
}

func unauthorized(c *gin.Context, msg string) {
	apiError(c, http.StatusUnauthorized, ErrCodeAuthenticationRequired, msg, nil)
}

func forbidden(c *gin.Context, msg string) {
	apiError(c, http.StatusForbidden, ErrCodePermissionDenied, msg, nil)
}

func notFound(c *gin.Context, msg string) {
	apiError(c, http.StatusNotFound, ErrCodeNotFound, msg, nil)
}

func conflict(c *gin.Context, code, msg string) {
	if code == "" {
		code = ErrCodeConflict
	}
	apiError(c, http.StatusConflict, code, msg, nil)
}

// serverError logs the real error, reports it to Sentry (with request scope when
// available), and returns an opaque 500 to the client. Sentry is a no-op when unset.
func serverError(c *gin.Context, err error) {
	obs.Error("internal error", "err", err, "path", c.FullPath(), "method", c.Request.Method)
	if hub := sentrygin.GetHubFromContext(c); hub != nil {
		hub.CaptureException(err)
	} else {
		sentry.CaptureException(err)
	}
	apiError(c, http.StatusInternalServerError, ErrCodeInternal, "internal error", nil)
}
