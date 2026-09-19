package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type BusinessRole string

const (
	RoleOwner    BusinessRole = "owner"
	RoleAdmin    BusinessRole = "admin"
	RoleManager  BusinessRole = "manager"
	RoleEmployee BusinessRole = "employee"
)

type Capability string

const (
	CapabilityReportsView         Capability = "reports.view"
	CapabilityMembersView         Capability = "members.view"
	CapabilityMembersManage       Capability = "members.manage"
	CapabilityMembersPurge        Capability = "members.purge"
	CapabilityDevicesView         Capability = "devices.view"
	CapabilityDevicesManage       Capability = "devices.manage"
	CapabilitySettingsView        Capability = "settings.view"
	CapabilitySettingsManage      Capability = "settings.manage"
	CapabilityAuditView           Capability = "audit.view"
	CapabilityRolesManage         Capability = "roles.manage"
	CapabilityOrganizationManage  Capability = "organization.manage"
	CapabilityOrganizationTransfer Capability = "organization.transfer"
	CapabilityOrganizationDelete  Capability = "organization.delete"
)

// BusinessPermission remains an alias while existing call sites migrate to the
// capability vocabulary.
type BusinessPermission = Capability

const (
	PermissionReports         = CapabilityReportsView
	PermissionManageEmployees = CapabilityMembersManage
	PermissionManageDevices   = CapabilityDevicesManage
	PermissionSettings        = CapabilitySettingsManage
	PermissionAudit           = CapabilityAuditView
	PermissionManageRoles     = CapabilityRolesManage
)

type Membership struct {
	BusinessID        string       `json:"business_id"`
	BusinessName      string       `json:"business_name"`
	Role              BusinessRole `json:"role"`
	Status            string       `json:"status"`
	MonitoringEnabled bool         `json:"monitoring_enabled"`
}

type BusinessAccess struct {
	Business Business     `json:"business"`
	Role     BusinessRole `json:"role"`
}

type LoginOrganization struct {
	BusinessID   string       `json:"business_id"`
	BusinessName string       `json:"business_name"`
	Kind         string       `json:"kind"`
	Role         BusinessRole `json:"role"`
	Status       string       `json:"status"`
}

func (s *Store) LoginOrganizations(ctx context.Context, userID string) ([]LoginOrganization, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.name, b.kind, m.role, m.status
		  FROM memberships m
		  JOIN businesses b ON b.id = m.business_id
		 WHERE m.user_id = $1
		   AND m.status IN ('active','blocked')
		 ORDER BY b.name, b.id`,
		userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []LoginOrganization{}
	for rows.Next() {
		var item LoginOrganization
		if err := rows.Scan(
			&item.BusinessID, &item.BusinessName, &item.Kind, &item.Role, &item.Status,
		); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func ValidBusinessRole(role BusinessRole) bool {
	switch role {
	case RoleOwner, RoleAdmin, RoleManager, RoleEmployee:
		return true
	default:
		return false
	}
}

func roleAllows(role BusinessRole, capability Capability) bool {
	switch role {
	case RoleOwner:
		return true
	case RoleAdmin:
		switch capability {
		case CapabilityReportsView, CapabilityMembersView, CapabilityMembersManage,
			CapabilityDevicesView, CapabilityDevicesManage, CapabilitySettingsView,
			CapabilitySettingsManage, CapabilityAuditView, CapabilityOrganizationManage:
			return true
		}
	case RoleManager:
		return capability == CapabilityReportsView || capability == CapabilityMembersView
	}
	return false
}

func (s *Store) MembershipRole(ctx context.Context, userID, businessID string) (BusinessRole, error) {
	var role BusinessRole
	var status string
	err := s.pool.QueryRow(ctx,
		`SELECT role, status FROM memberships WHERE user_id = $1 AND business_id = $2`,
		userID, businessID,
	).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if status != "active" {
		return role, ErrForbidden
	}
	return role, nil
}

func (s *Store) MembershipMonitoringEnabled(ctx context.Context, userID, businessID string) (bool, error) {
	var enabled bool
	var status string
	var archived, deletionPending bool
	err := s.pool.QueryRow(ctx, `
		SELECT m.monitoring_enabled, m.status,
		       b.archived_at IS NOT NULL,
		       b.deletion_scheduled_at IS NOT NULL
		  FROM memberships m
		  JOIN businesses b ON b.id = m.business_id
		 WHERE m.user_id = $1 AND m.business_id = $2`,
		userID, businessID,
	).Scan(&enabled, &status, &archived, &deletionPending)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil {
		return false, err
	}
	if deletionPending {
		return false, ErrOrganizationDeletionPending
	}
	if archived {
		return false, ErrOrganizationArchived
	}
	switch status {
	case MemberStatusBlocked:
		return false, ErrMemberBlocked
	case MemberStatusRemoved:
		return false, ErrMemberRemoved
	case MemberStatusActive:
		return enabled, nil
	default:
		return false, ErrMembershipUnavailable
	}
}

func membershipRoleTx(ctx context.Context, tx pgx.Tx, userID, businessID string) (BusinessRole, error) {
	var role BusinessRole
	var status string
	err := tx.QueryRow(ctx,
		`SELECT role, status FROM memberships WHERE user_id = $1 AND business_id = $2`,
		userID, businessID,
	).Scan(&role, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if status != "active" {
		return role, ErrForbidden
	}
	return role, nil
}

func (s *Store) HasBusinessPermission(ctx context.Context, userID, businessID string, permission BusinessPermission) (bool, error) {
	role, err := s.MembershipRole(ctx, userID, businessID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return roleAllows(role, permission), nil
}

func requireBusinessPermissionTx(ctx context.Context, tx pgx.Tx, userID, businessID string, permission BusinessPermission) (BusinessRole, error) {
	role, err := membershipRoleTx(ctx, tx, userID, businessID)
	if errors.Is(err, ErrNotFound) {
		return "", ErrForbidden
	}
	if err != nil {
		return "", err
	}
	if !roleAllows(role, permission) {
		return role, ErrForbidden
	}
	return role, nil
}

func (s *Store) MembershipsForUser(ctx context.Context, userID string) ([]Membership, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT m.business_id, b.name, m.role, m.status, m.monitoring_enabled
		  FROM memberships m
		  JOIN businesses b ON b.id = m.business_id
		 WHERE m.user_id = $1
		 ORDER BY b.created_at, b.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Membership{}
	for rows.Next() {
		var m Membership
		if err := rows.Scan(&m.BusinessID, &m.BusinessName, &m.Role, &m.Status, &m.MonitoringEnabled); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListBusinessesForConsole returns businesses visible in the administrative web
// console. Employees intentionally do not get a console business list.
func (s *Store) ListBusinessesForConsole(ctx context.Context, userID string) ([]BusinessAccess, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT b.id, b.name, b.kind, b.owner_user_id, b.timezone, b.week_starts_on,
		       b.default_member_monitoring_enabled,
		       b.collect_app_activity, b.collect_window_titles, b.collect_screenshots,
		       b.collect_browser_activity, b.collect_keystroke_counts,
		       b.screenshot_retention_days, b.screenshot_interval_s, b.screenshot_capture_scope,
		       b.idle_threshold_s, b.allow_employee_override, b.screenshot_mode, b.screenshot_skip_apps,
		       b.activity_retention_days, b.browser_retention_days, b.keystroke_retention_days,
		       b.audit_retention_days, b.device_limit, b.enrollment_token_ttl_s,
		       b.archived_at, b.deletion_scheduled_at, b.created_at, b.updated_at,
		       m.role
		  FROM memberships m
		  JOIN businesses b ON b.id = m.business_id
		 WHERE m.user_id = $1 AND m.status = 'active' AND m.role IN ('owner','admin','manager')
		 ORDER BY b.created_at, b.name`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BusinessAccess{}
	for rows.Next() {
		var a BusinessAccess
		if err := rows.Scan(
			&a.Business.ID, &a.Business.Name, &a.Business.Kind, &a.Business.OwnerUserID,
			&a.Business.Timezone, &a.Business.WeekStartsOn,
			&a.Business.DefaultMemberMonitoringEnabled,
			&a.Business.CollectAppActivity, &a.Business.CollectWindowTitles, &a.Business.CollectScreenshots,
			&a.Business.CollectBrowserActivity, &a.Business.CollectKeystrokeCounts,
			&a.Business.ScreenshotRetentionDays, &a.Business.ScreenshotIntervalS, &a.Business.ScreenshotCaptureScope,
			&a.Business.IdleThresholdS, &a.Business.AllowEmployeeOverride,
			&a.Business.ScreenshotMode, &a.Business.ScreenshotSkipApps,
			&a.Business.ActivityRetentionDays, &a.Business.BrowserRetentionDays, &a.Business.KeystrokeRetentionDays,
			&a.Business.AuditRetentionDays, &a.Business.DeviceLimit, &a.Business.EnrollmentTokenTTLS,
			&a.Business.ArchivedAt, &a.Business.DeletionScheduledAt,
			&a.Business.CreatedAt, &a.Business.UpdatedAt, &a.Role,
		); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// UpdateMembershipRole is owner-only. The business owner cannot demote themselves.
func (s *Store) UpdateMembershipRole(ctx context.Context, actorID, businessID, targetUserID string, role BusinessRole) error {
	if role != RoleAdmin && role != RoleManager && role != RoleEmployee {
		return ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := requireBusinessPermissionTx(ctx, tx, actorID, businessID, PermissionManageRoles); err != nil {
		return err
	}
	if err := lockMutableOrganizationTx(ctx, tx, businessID); err != nil {
		return err
	}

	var current BusinessRole
	err = tx.QueryRow(ctx,
		`SELECT role FROM memberships WHERE user_id = $1 AND business_id = $2 FOR UPDATE`,
		targetUserID, businessID,
	).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if current == RoleOwner {
		return ErrForbidden
	}

	if _, err := tx.Exec(ctx,
		`UPDATE memberships SET role = $1 WHERE user_id = $2 AND business_id = $3`,
		role, targetUserID, businessID,
	); err != nil {
		return err
	}
	if err := insertAuditTx(ctx, tx, businessID, actorID, "member.role_changed", "member", targetUserID, map[string]any{
		"from": string(current),
		"to":   string(role),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// UpdateMembershipMonitoring toggles collection for a member without changing
// their account role. Owners may change any non-owner member. Admins may change
// managers/employees but never the owner or a peer admin.
func (s *Store) UpdateMembershipMonitoring(ctx context.Context, actorID, businessID, targetUserID string, enabled bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	actorRole, err := requireBusinessPermissionTx(ctx, tx, actorID, businessID, PermissionManageEmployees)
	if err != nil {
		return err
	}
	if err := lockMutableOrganizationTx(ctx, tx, businessID); err != nil {
		return err
	}

	var targetRole BusinessRole
	var current bool
	err = tx.QueryRow(ctx,
		`SELECT role, monitoring_enabled FROM memberships WHERE user_id = $1 AND business_id = $2 FOR UPDATE`,
		targetUserID, businessID,
	).Scan(&targetRole, &current)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if targetRole == RoleOwner || (actorRole == RoleAdmin && targetRole == RoleAdmin) {
		return ErrForbidden
	}
	if current == enabled {
		return tx.Commit(ctx)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE memberships SET monitoring_enabled = $1 WHERE user_id = $2 AND business_id = $3`,
		enabled, targetUserID, businessID,
	); err != nil {
		return err
	}
	if err := insertAuditTx(ctx, tx, businessID, actorID, "member.monitoring_changed", "member", targetUserID, map[string]any{
		"enabled": enabled,
		"role":    string(targetRole),
	}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
