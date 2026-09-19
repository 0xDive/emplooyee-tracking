package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"actilens/backend/internal/privacyapps"

	"github.com/jackc/pgx/v5"
)

// ErrForbidden is returned when a user acts on a business they don't own.
var ErrForbidden = errors.New("forbidden")

// Business is a company/team owned by a user.
type Business struct {
	ID                             string     `json:"id"`
	Name                           string     `json:"name"`
	Kind                           string     `json:"kind"`
	OwnerUserID                    string     `json:"owner_user_id"`
	Timezone                       string     `json:"timezone"`
	WeekStartsOn                   *int       `json:"week_starts_on"`
	DefaultMemberMonitoringEnabled bool       `json:"default_member_monitoring_enabled"`
	CollectAppActivity             bool       `json:"collect_app_activity"`
	CollectWindowTitles            bool       `json:"collect_window_titles"`
	CollectScreenshots             bool       `json:"collect_screenshots"`
	CollectBrowserActivity         bool       `json:"collect_browser_activity"`
	CollectKeystrokeCounts         bool       `json:"collect_keystroke_counts"`
	ScreenshotRetentionDays        *int       `json:"screenshot_retention_days"`
	ScreenshotIntervalS            int        `json:"screenshot_interval_s"`
	ScreenshotCaptureScope         string     `json:"screenshot_capture_scope"`
	IdleThresholdS                 int        `json:"idle_threshold_s"`
	AllowEmployeeOverride          bool       `json:"allow_employee_override"` // compatibility for older agents
	ScreenshotMode                 string     `json:"screenshot_mode"` // compatibility for older agents
	ScreenshotSkipApps             []string   `json:"screenshot_skip_apps"` // compatibility mirror
	ActivityRetentionDays          int        `json:"activity_retention_days"`
	BrowserRetentionDays           int        `json:"browser_retention_days"`
	KeystrokeRetentionDays         int        `json:"keystroke_retention_days"`
	AuditRetentionDays             *int       `json:"audit_retention_days"`
	DeviceLimit                    *int       `json:"device_limit"`
	EnrollmentTokenTTLS            int        `json:"enrollment_token_ttl_s"`
	ArchivedAt                     *time.Time `json:"archived_at"`
	DeletionScheduledAt            *time.Time `json:"deletion_scheduled_at"`
	CreatedAt                      time.Time  `json:"created_at"`
	UpdatedAt                      time.Time  `json:"updated_at"`
}

// businessCols is the column list backing a Business scan (see scanBusiness).
const businessCols = "id, name, kind, owner_user_id, timezone, week_starts_on, default_member_monitoring_enabled, collect_app_activity, collect_window_titles, collect_screenshots, collect_browser_activity, collect_keystroke_counts, screenshot_retention_days, screenshot_interval_s, screenshot_capture_scope, idle_threshold_s, allow_employee_override, screenshot_mode, screenshot_skip_apps, activity_retention_days, browser_retention_days, keystroke_retention_days, audit_retention_days, device_limit, enrollment_token_ttl_s, archived_at, deletion_scheduled_at, created_at, updated_at"

// Employee is a member with the employee role within a business.
type Employee struct {
	ID                string       `json:"id"`
	Email             string       `json:"email"`
	Username          string       `json:"username"`
	DisplayName       string       `json:"display_name"`
	Active            bool         `json:"active"` // global account state
	Role              BusinessRole `json:"role"`
	Status            string       `json:"status"`
	MonitoringEnabled bool         `json:"monitoring_enabled"`
	BlockedAt         *time.Time   `json:"blocked_at"`
	RemovedAt         *time.Time   `json:"removed_at"`
	LastSeen          *int64       `json:"last_seen"`
	CurrentApp        *string      `json:"current_app"`
	CurrentWindow     *string      `json:"current_window"`
}

// CreateBusiness creates a business and the owner membership in one transaction.
// kind is 'team' | 'family'; pass "" to default to 'team'.
func (s *Store) CreateBusiness(ctx context.Context, ownerID, name, kind string) (Business, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Business{}, err
	}
	defer tx.Rollback(ctx)

	biz, err := createBusinessTx(ctx, tx, ownerID, name, kind)
	if err != nil {
		return Business{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Business{}, err
	}
	return biz, nil
}

// scanner is satisfied by pgx.Row and pgx.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanBusiness(s scanner) (Business, error) {
	var b Business
	err := s.Scan(
		&b.ID, &b.Name, &b.Kind, &b.OwnerUserID, &b.Timezone, &b.WeekStartsOn,
		&b.DefaultMemberMonitoringEnabled, &b.CollectAppActivity, &b.CollectWindowTitles,
		&b.CollectScreenshots, &b.CollectBrowserActivity, &b.CollectKeystrokeCounts,
		&b.ScreenshotRetentionDays, &b.ScreenshotIntervalS, &b.ScreenshotCaptureScope,
		&b.IdleThresholdS, &b.AllowEmployeeOverride, &b.ScreenshotMode, &b.ScreenshotSkipApps,
		&b.ActivityRetentionDays, &b.BrowserRetentionDays, &b.KeystrokeRetentionDays,
		&b.AuditRetentionDays, &b.DeviceLimit, &b.EnrollmentTokenTTLS,
		&b.ArchivedAt, &b.DeletionScheduledAt, &b.CreatedAt, &b.UpdatedAt,
	)
	return b, err
}

// ListBusinessesOwnedBy returns the businesses a user owns.
func (s *Store) ListBusinessesOwnedBy(ctx context.Context, ownerID string) ([]Business, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+businessCols+` FROM businesses WHERE owner_user_id = $1 ORDER BY created_at`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Business{}
	for rows.Next() {
		b, err := scanBusiness(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// GetBusiness returns a business by id.
func (s *Store) GetBusiness(ctx context.Context, id string) (Business, error) {
	return getBusiness(ctx, s.pool, id)
}

// CreateEmployee creates an employee account + employee membership. If businessID is
// nil, the employee is placed in the owner's first business, auto-creating a default
// business ("<owner>'s Team") when the owner has none. If businessID is set, the
// caller must own that business. All in one transaction.
func (s *Store) CreateEmployee(ctx context.Context, ownerID string, businessID *string, email, username, passwordHash, displayName string) (Employee, Business, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Employee{}, Business{}, err
	}
	defer tx.Rollback(ctx)

	var biz Business
	if businessID != nil {
		biz, err = getBusiness(ctx, tx, *businessID)
		if err != nil {
			return Employee{}, Business{}, err
		}
		if _, err := requireBusinessPermissionTx(ctx, tx, ownerID, biz.ID, PermissionManageEmployees); err != nil {
			return Employee{}, Business{}, err
		}
	} else {
		biz, err = firstOwnedBusinessTx(ctx, tx, ownerID)
		if errors.Is(err, ErrNotFound) {
			// Skip-business flow: auto-create a default business for this owner.
			// A parent owner gets a 'family' business labelled "...'s Family".
			var ownerName, accountType string
			if err := tx.QueryRow(ctx, `SELECT display_name, account_type FROM users WHERE id = $1`, ownerID).
				Scan(&ownerName, &accountType); err != nil {
				return Employee{}, Business{}, err
			}
			kind, suffix := "team", "'s Team"
			if accountType == "parent" {
				kind, suffix = "family", "'s Family"
			}
			biz, err = createBusinessTx(ctx, tx, ownerID, ownerName+suffix, kind)
		}
		if err != nil {
			return Employee{}, Business{}, err
		}
	}

	if err := lockMutableOrganizationTx(ctx, tx, biz.ID); err != nil {
		return Employee{}, Business{}, err
	}

	// Store NULL (not "") for a missing identifier so unique constraints don't
	// collide across members and the CHECK constraint reads cleanly.
	emailArg := nullableLower(email)
	usernameArg := nullableLower(username)
	var emp Employee
	err = tx.QueryRow(ctx,
		`INSERT INTO users (email, username, password_hash, display_name)
		 VALUES ($1, $2, $3, $4) RETURNING id, COALESCE(email, ''), COALESCE(username, ''), display_name, active`,
		emailArg, usernameArg, passwordHash, displayName,
	).Scan(&emp.ID, &emp.Email, &emp.Username, &emp.DisplayName, &emp.Active)
	if isUniqueViolation(err) {
		return Employee{}, Business{}, ErrConflict
	}
	if err != nil {
		return Employee{}, Business{}, err
	}
	emp.Role = RoleEmployee
	emp.Status = MemberStatusActive
	emp.MonitoringEnabled = biz.DefaultMemberMonitoringEnabled

	if _, err := tx.Exec(ctx,
		`INSERT INTO memberships
		     (user_id, business_id, role, status, monitoring_enabled)
		 VALUES ($1, $2, 'employee', 'active', $3)`,
		emp.ID, biz.ID, biz.DefaultMemberMonitoringEnabled,
	); err != nil {
		return Employee{}, Business{}, err
	}

	if err := insertAuditTx(ctx, tx, biz.ID, ownerID, "employee.created", "employee", emp.ID, map[string]any{
		"display_name": emp.DisplayName,
	}); err != nil {
		return Employee{}, Business{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Employee{}, Business{}, err
	}
	return emp, biz, nil
}

// ListEmployees returns employee members with real presence/current-app data.
func (s *Store) ListEmployees(ctx context.Context, businessID string) ([]Employee, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id, COALESCE(u.email, ''), COALESCE(u.username, ''), u.display_name, u.active,
		       m.role, m.status, m.monitoring_enabled, m.blocked_at, m.removed_at,
		       (SELECT extract(epoch FROM max(d.last_seen_at))::bigint
		          FROM devices d WHERE d.user_id = u.id AND (d.business_id = $1 OR d.business_id IS NULL)),
		       (SELECT a.app_name FROM activity_samples a
		         WHERE a.user_id = u.id AND a.business_id = $1 ORDER BY a.ts DESC LIMIT 1),
		       (SELECT a.window_title FROM activity_samples a
		         WHERE a.user_id = u.id AND a.business_id = $1 ORDER BY a.ts DESC LIMIT 1)
		  FROM memberships m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.business_id = $1
		   AND m.status IN ('active','blocked')
		   AND m.role IN ('owner','admin','manager','employee')
		 ORDER BY CASE m.role WHEN 'owner' THEN 0 WHEN 'admin' THEN 1 WHEN 'manager' THEN 2 ELSE 3 END,
		          CASE m.status WHEN 'active' THEN 0 ELSE 1 END,
		          u.display_name`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Employee{}
	for rows.Next() {
		var e Employee
		if err := rows.Scan(
			&e.ID, &e.Email, &e.Username, &e.DisplayName, &e.Active,
			&e.Role, &e.Status, &e.MonitoringEnabled, &e.BlockedAt, &e.RemovedAt,
			&e.LastSeen, &e.CurrentApp, &e.CurrentWindow,
		); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// settableColumns whitelists the business columns owners may PATCH, guarding the
// dynamic UPDATE against arbitrary column names.
var settableColumns = map[string]bool{
	"default_member_monitoring_enabled": true,
	"collect_app_activity":              true,
	"collect_window_titles":             true,
	"collect_screenshots":               true,
	"collect_browser_activity":          true,
	"collect_keystroke_counts":          true,
	"screenshot_retention_days":         true,
	"screenshot_interval_s":             true,
	"screenshot_capture_scope":          true,
	"idle_threshold_s":                  true,
	"activity_retention_days":           true,
	"browser_retention_days":            true,
	"keystroke_retention_days":          true,
	"audit_retention_days":              true,
	"device_limit":                      true,
	"enrollment_token_ttl_s":            true,
	// Compatibility fields while old desktop clients still exist.
	"allow_employee_override": true,
	"screenshot_mode":         true,
	"screenshot_skip_apps":    true,
}

// UpdateBusinessSettings updates only the provided columns (keys must be in
// settableColumns; values are already typed by the caller). A nil value sets NULL
// (used for "keep screenshots forever").
func (s *Store) UpdateBusinessSettings(ctx context.Context, businessID string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	sets := make([]string, 0, len(fields))
	args := []any{businessID}
	for col, val := range fields {
		if !settableColumns[col] {
			return fmt.Errorf("not a settable column: %s", col)
		}
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
	}
	q := fmt.Sprintf("UPDATE businesses SET %s WHERE id = $1", strings.Join(sets, ", "))
	ct, err := s.pool.Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateBusinessSettingsAudited applies organization policy changes atomically
// with an audit event. Values are restricted by settableColumns and contain no secrets.
func (s *Store) UpdateBusinessSettingsAudited(
	ctx context.Context,
	actorID, businessID string,
	fields map[string]any,
) error {
	if len(fields) == 0 {
		return nil
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilitySettingsManage,
	); err != nil {
		return err
	}

	var archivedAt, deletionScheduledAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT archived_at, deletion_scheduled_at
		  FROM businesses
		 WHERE id = $1
		 FOR UPDATE`, businessID,
	).Scan(&archivedAt, &deletionScheduledAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if deletionScheduledAt != nil {
		return ErrOrganizationDeletionPending
	}
	if archivedAt != nil {
		return ErrOrganizationArchived
	}

	sets := make([]string, 0, len(fields)+1)
	args := []any{businessID}
	fieldNames := make([]string, 0, len(fields))
	values := map[string]any{}
	for col, val := range fields {
		if !settableColumns[col] {
			return fmt.Errorf("not a settable column: %s", col)
		}
		args = append(args, val)
		sets = append(sets, fmt.Sprintf("%s = $%d", col, len(args)))
		fieldNames = append(fieldNames, col)
		values[col] = val
	}
	sets = append(sets, "updated_at = now()")
	q := fmt.Sprintf("UPDATE businesses SET %s WHERE id = $1", strings.Join(sets, ", "))
	ct, err := tx.Exec(ctx, q, args...)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}

	action := "settings.changed"
	for _, name := range fieldNames {
		switch name {
		case "activity_retention_days", "screenshot_retention_days",
			"browser_retention_days", "keystroke_retention_days", "audit_retention_days":
			action = "settings.retention_changed"
		case "device_limit":
			action = "settings.device_limit_changed"
		case "enrollment_token_ttl_s":
			action = "settings.enrollment_changed"
		case "collect_app_activity", "collect_window_titles", "collect_browser_activity",
			"collect_keystroke_counts", "default_member_monitoring_enabled":
			if action == "settings.changed" {
				action = "settings.collection_changed"
			}
		case "collect_screenshots", "screenshot_interval_s", "screenshot_capture_scope",
			"screenshot_mode", "screenshot_skip_apps":
			if action == "settings.changed" {
				action = "settings.screenshot_changed"
			}
		}
	}
	if err := insertAuditTx(ctx, tx, businessID, actorID, action, "organization", businessID, map[string]any{
		"fields": fieldNames,
		"values": values,
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) DefaultMonitoringImpact(
	ctx context.Context,
	actorID, businessID string,
	enabled bool,
) (int64, error) {
	if err := s.BusinessPermissionOrForbidden(
		ctx, actorID, businessID, CapabilitySettingsManage,
	); err != nil {
		return 0, err
	}
	var count int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM memberships
		 WHERE business_id = $1
		   AND status = 'active'
		   AND role <> 'owner'
		   AND monitoring_enabled IS DISTINCT FROM $2`,
		businessID, enabled,
	).Scan(&count)
	return count, err
}

func (s *Store) UpdateDefaultMonitoring(
	ctx context.Context,
	actorID, businessID string,
	enabled, applyExisting bool,
) (int64, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)

	if _, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilitySettingsManage,
	); err != nil {
		return 0, err
	}
	var archivedAt, deletionScheduledAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT archived_at, deletion_scheduled_at
		  FROM businesses
		 WHERE id = $1
		 FOR UPDATE`, businessID,
	).Scan(&archivedAt, &deletionScheduledAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, err
	}
	if deletionScheduledAt != nil {
		return 0, ErrOrganizationDeletionPending
	}
	if archivedAt != nil {
		return 0, ErrOrganizationArchived
	}

	if _, err := tx.Exec(ctx, `
		UPDATE businesses
		   SET default_member_monitoring_enabled = $1, updated_at = now()
		 WHERE id = $2`,
		enabled, businessID,
	); err != nil {
		return 0, err
	}

	var affected int64
	if applyExisting {
		ct, err := tx.Exec(ctx, `
			UPDATE memberships
			   SET monitoring_enabled = $1, updated_at = now()
			 WHERE business_id = $2
			   AND status = 'active'
			   AND role <> 'owner'
			   AND monitoring_enabled IS DISTINCT FROM $1`,
			enabled, businessID,
		)
		if err != nil {
			return 0, err
		}
		affected = ct.RowsAffected()
	}

	if err := insertAuditTx(ctx, tx, businessID, actorID, "settings.default_monitoring_changed", "organization", businessID, map[string]any{
		"enabled":        enabled,
		"apply_existing": applyExisting,
		"affected_count": affected,
	}); err != nil {
		return 0, err
	}
	return affected, tx.Commit(ctx)
}

// CapturePolicy is the org-controlled capture configuration the desktop applies.
type CapturePolicy struct {
	BusinessID                     string   `json:"business_id"`
	Managed                        bool     `json:"managed"`
	Archived                       bool     `json:"archived"`
	DefaultMemberMonitoringEnabled bool     `json:"default_member_monitoring_enabled"`
	CollectAppActivity             bool     `json:"collect_app_activity"`
	CollectWindowTitles            bool     `json:"collect_window_titles"`
	CollectScreenshots             bool     `json:"collect_screenshots"`
	CollectBrowserActivity         bool     `json:"collect_browser_activity"`
	CollectKeystrokeCounts         bool     `json:"collect_keystroke_counts"`
	ScreenshotIntervalS            int      `json:"screenshot_interval_s"`
	ScreenshotCaptureScope         string   `json:"screenshot_capture_scope"`
	IdleThresholdS                 int      `json:"idle_threshold_s"`
	ScreenshotRetentionDays        *int     `json:"screenshot_retention_days"`
	Kind                           string   `json:"kind"`
	ScreenshotMode                 string   `json:"screenshot_mode"` // compatibility
	ScreenshotSkipApps             []string      `json:"screenshot_skip_apps"` // compatibility
	PrivacyRules                   []PrivacyRule `json:"privacy_rules"`
}

func (s *Store) PolicyForUserInBusiness(ctx context.Context, userID, businessID string) (*CapturePolicy, error) {
	var p CapturePolicy
	var status string
	var archived, deletionPending bool
	err := s.pool.QueryRow(ctx, `
		SELECT b.id, m.status,
		       b.archived_at IS NOT NULL,
		       b.deletion_scheduled_at IS NOT NULL,
		       b.default_member_monitoring_enabled,
		       b.collect_app_activity, b.collect_window_titles, b.collect_screenshots,
		       b.collect_browser_activity, b.collect_keystroke_counts,
		       b.screenshot_interval_s, b.screenshot_capture_scope, b.idle_threshold_s,
		       b.screenshot_retention_days, b.kind, b.screenshot_mode, b.screenshot_skip_apps
		  FROM memberships m
		  JOIN businesses b ON b.id = m.business_id
		 WHERE m.user_id = $1 AND m.business_id = $2`,
		userID, businessID,
	).Scan(
		&p.BusinessID, &status, &archived, &deletionPending, &p.DefaultMemberMonitoringEnabled,
		&p.CollectAppActivity, &p.CollectWindowTitles, &p.CollectScreenshots,
		&p.CollectBrowserActivity, &p.CollectKeystrokeCounts,
		&p.ScreenshotIntervalS, &p.ScreenshotCaptureScope, &p.IdleThresholdS,
		&p.ScreenshotRetentionDays, &p.Kind, &p.ScreenshotMode, &p.ScreenshotSkipApps,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status == "blocked" {
		return nil, ErrMemberBlocked
	}
	if status == "removed" {
		return nil, ErrMemberRemoved
	}
	if deletionPending {
		return nil, ErrOrganizationDeletionPending
	}
	if archived {
		return nil, ErrOrganizationArchived
	}
	rules, err := s.privacyRulesForBusiness(ctx, businessID)
	if err != nil {
		return nil, err
	}
	p.PrivacyRules = rules
	p.Managed = true
	return &p, nil
}

// PolicyForUser remains for old clients. Multi-organization users are deliberately
// treated as unmanaged rather than selecting an arbitrary organization.
func (s *Store) PolicyForUser(ctx context.Context, userID string) (*CapturePolicy, error) {
	bizID, err := s.ResolveBusinessForUser(ctx, userID, nil)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.PolicyForUserInBusiness(ctx, userID, bizID)
}

// --- transaction-scoped helpers (work with both *pgxpool.Pool and pgx.Tx) ---

// rowQuerier is satisfied by *pgxpool.Pool and pgx.Tx.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func getBusiness(ctx context.Context, q rowQuerier, id string) (Business, error) {
	b, err := scanBusiness(q.QueryRow(ctx, `SELECT `+businessCols+` FROM businesses WHERE id = $1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Business{}, ErrNotFound
	}
	if err != nil {
		return Business{}, err
	}
	return b, nil
}

func firstOwnedBusinessTx(ctx context.Context, tx pgx.Tx, ownerID string) (Business, error) {
	b, err := scanBusiness(tx.QueryRow(ctx,
		`SELECT `+businessCols+` FROM businesses WHERE owner_user_id = $1 ORDER BY created_at LIMIT 1`, ownerID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Business{}, ErrNotFound
	}
	if err != nil {
		return Business{}, err
	}
	return b, nil
}

func createBusinessTx(ctx context.Context, tx pgx.Tx, ownerID, name, kind string) (Business, error) {
	if kind == "" {
		kind = "team"
	}
	// New businesses start in privacy mode with the skip-list prefilled from the
	// curated sensitive-app rules; owners can add/remove entries afterwards.
	b, err := scanBusiness(tx.QueryRow(ctx,
		`INSERT INTO businesses (name, owner_user_id, kind, screenshot_skip_apps) VALUES ($1, $2, $3, $4) RETURNING `+businessCols,
		name, ownerID, kind, privacyapps.Flat()))
	if err != nil {
		return Business{}, err
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO memberships (user_id, business_id, role) VALUES ($1, $2, 'owner')`,
		ownerID, b.ID,
	); err != nil {
		return Business{}, err
	}
	return b, nil
}
