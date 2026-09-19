// Package server wires the router, middleware, and routes together.
package server

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"actilens/backend/internal/auth"
	"actilens/backend/internal/config"
	"actilens/backend/internal/events"
	"actilens/backend/internal/filestore"
	"actilens/backend/internal/handlers"
	"actilens/backend/internal/middleware"
	"actilens/backend/internal/obs"
	"actilens/backend/internal/retention"
	"actilens/backend/internal/store"

	sentrygin "github.com/getsentry/sentry-go/gin"
	"github.com/gin-gonic/gin"
)

// New builds the Gin engine with all routes registered. The store, file store, and
// retention service are shared with the caller (which also runs the retention sweeper).
func New(cfg *config.Config, st *store.Store, files *filestore.Store, ret *retention.Service) *gin.Engine {
	gin.DefaultWriter = obs.Writer()
	gin.DefaultErrorWriter = obs.Writer()

	r := gin.New()
	r.Use(gin.Logger(), gin.Recovery(), middleware.CORS(cfg.AllowedOrigin))

	if cfg.SentryDSN != "" {
		r.Use(sentrygin.New(sentrygin.Options{Repanic: true}))
	}

	r.GET("/healthz", handlers.Health)

	tok := auth.NewManager(cfg.JWTSecret)
	eventBus := events.NewBus()
	eventBus.Subscribe(events.SinkFunc(func(_ context.Context, event events.Event) {
		obs.Info(
			"domain event",
			"type", string(event.Type),
			"organization_id", event.OrganizationID,
			"user_id", event.UserID,
			"target_user_id", event.TargetUserID,
		)
	}))
	authH := handlers.NewAuthHandler(st, tok)
	authH.SetEventPublisher(eventBus)
	ownerH := handlers.NewOwnerHandler(st, cfg.RecommendedDesktopVersion)
	lifecycleH := handlers.NewOrganizationLifecycleHandler(st, tok)
	lifecycleH.SetEventPublisher(eventBus)
	syncH := handlers.NewSyncHandler(st)
	shotH := handlers.NewScreenshotHandler(st, files)
	reportsH := handlers.NewReportsHandler(st, files)
	retentionH := handlers.NewRetentionHandler(st, ret)
	exportH := handlers.NewExportHandler(st, files)
	downloadsH := handlers.NewDownloadsHandler(st, cfg.StaticDir)
	keepaliveH := handlers.NewKeepaliveHandler(cfg.KeepaliveToken)
	purgeH := handlers.NewMemberPurgeHandler(st, files)

	if cfg.StaticDir != "" {
		r.GET("/download/:file", downloadsH.Serve)
	}

	v1 := r.Group("/v1")

	v1.GET("/public/businesses", authH.PublicBusinesses)
	v1.GET("/public/stats/downloads", downloadsH.Stats)
	v1.GET("/public/screenshot-privacy-apps", handlers.PrivacyApps)

	if keepaliveH.Enabled() {
		v1.POST("/keepalive", keepaliveH.Burn)
	}

	a := v1.Group("/auth", middleware.LoginRateLimit())
	a.POST("/register", authH.Register)
	a.POST("/login", authH.Login)
	a.POST("/enroll", authH.Enroll)
	a.POST("/refresh", authH.Refresh)
	a.POST("/mfa/complete", authH.CompleteMFA)

	authed := v1.Group("", tok.Required(), accountGuard(st))
	authed.GET("/me", authH.Me)
	authed.GET("/account", authH.Account)
	authed.PATCH("/account/profile", authH.UpdateOwnDisplayName)
	authed.PATCH("/account/login-identifiers", authH.UpdateOwnLoginIdentifiers)
	authed.POST("/account/password/change", authH.ChangeOwnPassword)
	authed.POST("/account/reauth", authH.Reauth)
	authed.GET("/account/sessions", authH.ListSessions)
	authed.DELETE("/account/sessions/:session_id", authH.RevokeSession)
	authed.POST("/account/sessions/revoke-others", authH.RevokeOtherSessions)
	authed.GET("/account/mfa", authH.MFAState)
	authed.POST("/account/mfa/totp/setup", authH.BeginMFASetup)
	authed.POST("/account/mfa/totp/confirm", authH.ConfirmMFASetup)
	authed.POST("/account/mfa/recovery/regenerate", authH.RegenerateRecoveryCodes)
	authed.POST("/account/mfa/disable", authH.DisableMFA)

	// Membership / RBAC discovery and membership controls.
	authed.GET("/memberships/mine", ownerH.ListMyMemberships)
	authed.GET("/businesses/console", ownerH.ListConsoleBusinesses)
	authed.PATCH("/businesses/:id/members/:user_id/role", ownerH.UpdateMemberRole)
	authed.PATCH("/businesses/:id/members/:user_id/monitoring", ownerH.UpdateMemberMonitoring)
	authed.GET("/businesses/:id/members/former", ownerH.ListFormerMembers)
	authed.POST("/businesses/:id/members/:user_id/block", ownerH.BlockMember)
	authed.POST("/businesses/:id/members/:user_id/unblock", ownerH.UnblockMember)
	authed.POST("/businesses/:id/members/:user_id/remove", ownerH.RemoveMember)
	authed.POST("/businesses/:id/members/:user_id/restore", ownerH.RestoreMember)
	authed.DELETE("/businesses/:id/members/:user_id/purge", purgeH.Purge)
	authed.POST("/businesses/:id/members/:user_id/enrollment-token", ownerH.CreateEnrollmentToken)
	authed.POST("/businesses/:id/members/:user_id/mfa/reset", authH.ResetMemberMFA)
	authed.GET("/businesses/:id/members/:user_id/devices", ownerH.ListMemberDevices)
	authed.PATCH("/businesses/:id/devices/:device_id", ownerH.UpdateOrganizationDevice)
	authed.GET("/businesses/:id/devices/health", ownerH.DeviceHealthSummary)
	authed.PATCH("/businesses/:id/members/:user_id/profile", ownerH.UpdateManagedMemberIdentity)
	authed.POST("/businesses/:id/members/:user_id/reset-password", ownerH.ResetManagedMemberPassword)

	// Business, employee, device and audit management.
	authed.POST("/businesses", ownerH.CreateOrganization)
	authed.GET("/businesses/mine", ownerH.ListMine)
	authed.GET("/businesses/:id", ownerH.GetOrganization)
	authed.PATCH("/businesses/:id", ownerH.UpdateOrganization)
	authed.POST("/businesses/:id/archive", lifecycleH.Archive)
	authed.POST("/businesses/:id/restore", lifecycleH.Restore)
	authed.POST("/businesses/:id/transfer-ownership", lifecycleH.TransferOwnership)
	authed.GET("/businesses/:id/deletion-preview", lifecycleH.DeletionPreview)
	authed.POST("/businesses/:id/schedule-deletion", lifecycleH.ScheduleDeletion)
	authed.POST("/businesses/:id/cancel-deletion", lifecycleH.CancelDeletion)
	authed.POST("/businesses/:id/members", ownerH.CreateOrganizationMember)
	authed.GET("/businesses/:id/employees", ownerH.ListEmployees)
	authed.GET("/businesses/:id/audit", ownerH.ListAuditEvents)
	authed.PATCH("/businesses/:id/settings", ownerH.UpdateSettings)
	authed.GET("/businesses/:id/privacy-rules", ownerH.ListPrivacyRules)
	authed.POST("/businesses/:id/privacy-rules", ownerH.CreatePrivacyRule)
	authed.PATCH("/businesses/:id/privacy-rules/:rule_id", ownerH.UpdatePrivacyRule)
	authed.DELETE("/businesses/:id/privacy-rules/:rule_id", ownerH.DeletePrivacyRule)
	authed.GET("/businesses/:id/settings/default-monitoring-impact", ownerH.DefaultMonitoringImpact)
	authed.POST("/businesses/:id/settings/default-monitoring", ownerH.UpdateDefaultMonitoring)
	authed.GET("/businesses/:id/retention/preview", retentionH.Preview)
	authed.POST("/businesses/:id/data/cleanup", retentionH.CleanupData)
	authed.POST("/businesses/:id/exports", exportH.Create)
	authed.GET("/businesses/:id/exports", exportH.List)
	authed.GET("/businesses/:id/exports/:export_id", exportH.Get)
	authed.GET("/businesses/:id/exports/:export_id/download", exportH.Download)
	authed.POST("/businesses/:id/screenshots/cleanup", retentionH.Cleanup)
	authed.POST("/employees", ownerH.CreateEmployee)
	authed.PATCH("/employees/:id", ownerH.UpdateEmployee)
	authed.POST("/employees/:id/reset-password", ownerH.ResetEmployeePassword)
	authed.DELETE("/employees/:id", ownerH.ArchiveEmployee)
	authed.GET("/employees/:id/devices", ownerH.ListEmployeeDevices)
	authed.PATCH("/devices/:id", ownerH.UpdateDevice)

	// Capture policy for the desktop (employee's org settings).
	authed.GET("/policy", ownerH.Policy)

	// Sync ingest (desktop → backend, one-directional).
	authed.POST("/sync/batch", syncH.Batch)
	authed.POST("/sync/screenshots", shotH.Upload)

	// Reporting.
	authed.GET("/reports/employees", reportsH.Roster)
	authed.GET("/reports/employees/:id/activity", reportsH.Activity)
	authed.GET("/reports/employees/:id/keystrokes", reportsH.Keystrokes)
	authed.GET("/reports/employees/:id/browser", reportsH.Browser)
	authed.GET("/reports/employees/:id/screenshots", reportsH.Screenshots)
	authed.GET("/screenshots/:client_uuid", reportsH.ScreenshotImage)

	if cfg.StaticDir != "" {
		r.NoRoute(staticSite(cfg.StaticDir))
	}

	return r
}

func staticSite(dir string) gin.HandlerFunc {
	rootIndex := filepath.Join(dir, "index.html")
	adminIndex := filepath.Join(dir, "admin", "index.html")
	serve := func(c *gin.Context, file string) {
		if strings.HasSuffix(file, ".html") {
			c.Header("Cache-Control", "no-cache")
		}
		c.File(file)
	}
	return func(c *gin.Context) {
		p := c.Request.URL.Path
		if p == "/healthz" || p == "/v1" || strings.HasPrefix(p, "/v1/") {
			c.JSON(http.StatusNotFound, gin.H{
				"error": "not found",
				"code":  "not_found",
			})
			return
		}
		file := filepath.Join(dir, filepath.Clean("/"+p))
		if fi, err := os.Stat(file); err == nil {
			if !fi.IsDir() {
				serve(c, file)
				return
			}
			if idx := filepath.Join(file, "index.html"); idx != rootIndex {
				if fi2, err2 := os.Stat(idx); err2 == nil && !fi2.IsDir() {
					serve(c, idx)
					return
				}
			}
		}
		if p == "/admin" || strings.HasPrefix(p, "/admin/") {
			serve(c, adminIndex)
			return
		}
		serve(c, rootIndex)
	}
}
