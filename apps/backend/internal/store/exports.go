package store

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	ExportActivityCSV       = "activity_csv"
	ExportActivityJSON      = "activity_json"
	ExportBrowserCSV        = "browser_csv"
	ExportBrowserJSON       = "browser_json"
	ExportKeystrokesCSV     = "keystrokes_csv"
	ExportKeystrokesJSON    = "keystrokes_json"
	ExportAuditCSV          = "audit_csv"
	ExportAuditJSON         = "audit_json"
	ExportScreenshotsArchive = "screenshots_archive"
	ExportFull              = "full"
)

func ValidOrganizationExportKind(kind string) bool {
	switch kind {
	case ExportActivityCSV, ExportActivityJSON,
		ExportBrowserCSV, ExportBrowserJSON,
		ExportKeystrokesCSV, ExportKeystrokesJSON,
		ExportAuditCSV, ExportAuditJSON,
		ExportScreenshotsArchive, ExportFull:
		return true
	default:
		return false
	}
}

type OrganizationExport struct {
	ID           string     `json:"id"`
	BusinessID   string     `json:"business_id"`
	RequestedBy  string     `json:"requested_by"`
	Kind         string     `json:"kind"`
	Status       string     `json:"status"`
	FilePath     *string    `json:"-"`
	ErrorCode    *string    `json:"error_code"`
	CreatedAt    time.Time  `json:"created_at"`
	CompletedAt  *time.Time `json:"completed_at"`
	ExpiresAt    *time.Time `json:"expires_at"`
}

const organizationExportColumns = `
	id::text, business_id::text, requested_by::text, kind, status,
	file_path, error_code, created_at, completed_at, expires_at`

func scanOrganizationExport(row scanner) (OrganizationExport, error) {
	var job OrganizationExport
	err := row.Scan(
		&job.ID, &job.BusinessID, &job.RequestedBy, &job.Kind, &job.Status,
		&job.FilePath, &job.ErrorCode, &job.CreatedAt, &job.CompletedAt, &job.ExpiresAt,
	)
	return job, err
}

func (s *Store) CreateOrganizationExport(
	ctx context.Context,
	actorID, businessID, kind string,
) (OrganizationExport, error) {
	if !ValidOrganizationExportKind(kind) {
		return OrganizationExport{}, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OrganizationExport{}, err
	}
	defer tx.Rollback(ctx)

	if _, err := requireBusinessPermissionTx(
		ctx, tx, actorID, businessID, CapabilitySettingsManage,
	); err != nil {
		return OrganizationExport{}, err
	}

	job, err := scanOrganizationExport(tx.QueryRow(ctx, `
		INSERT INTO organization_exports (business_id, requested_by, kind)
		VALUES ($1, $2, $3)
		RETURNING `+organizationExportColumns,
		businessID, actorID, kind,
	))
	if err != nil {
		return OrganizationExport{}, err
	}
	if err := insertAuditTx(
		ctx, tx, businessID, actorID, "data.export_requested", "organization_export", job.ID,
		map[string]any{"kind": kind},
	); err != nil {
		return OrganizationExport{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return OrganizationExport{}, err
	}
	return job, nil
}

func (s *Store) ListOrganizationExports(
	ctx context.Context,
	actorID, businessID string,
	limit int,
) ([]OrganizationExport, error) {
	if err := s.BusinessPermissionOrForbidden(
		ctx, actorID, businessID, CapabilitySettingsManage,
	); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+organizationExportColumns+`
		  FROM organization_exports
		 WHERE business_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2`,
		businessID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []OrganizationExport{}
	for rows.Next() {
		job, err := scanOrganizationExport(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}

func (s *Store) OrganizationExportForActor(
	ctx context.Context,
	actorID, businessID, exportID string,
) (OrganizationExport, error) {
	if err := s.BusinessPermissionOrForbidden(
		ctx, actorID, businessID, CapabilitySettingsManage,
	); err != nil {
		return OrganizationExport{}, err
	}
	job, err := scanOrganizationExport(s.pool.QueryRow(ctx, `
		SELECT `+organizationExportColumns+`
		  FROM organization_exports
		 WHERE id = $1 AND business_id = $2`,
		exportID, businessID,
	))
	if errors.Is(err, pgx.ErrNoRows) {
		return OrganizationExport{}, ErrNotFound
	}
	return job, err
}

func (s *Store) RequeueRunningOrganizationExports(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE organization_exports
		   SET status = 'pending', error_code = NULL
		 WHERE status = 'running'`)
	return err
}

func (s *Store) ClaimNextOrganizationExport(ctx context.Context) (*OrganizationExport, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	job, err := scanOrganizationExport(tx.QueryRow(ctx, `
		SELECT `+organizationExportColumns+`
		  FROM organization_exports
		 WHERE status = 'pending'
		 ORDER BY created_at
		 FOR UPDATE SKIP LOCKED
		 LIMIT 1`))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE organization_exports
		   SET status = 'running', error_code = NULL
		 WHERE id = $1`, job.ID); err != nil {
		return nil, err
	}
	job.Status = "running"
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *Store) CompleteOrganizationExport(
	ctx context.Context,
	exportID, filePath string,
	expiresAt time.Time,
) error {
	ct, err := s.pool.Exec(ctx, `
		UPDATE organization_exports
		   SET status = 'ready',
		       file_path = $2,
		       error_code = NULL,
		       completed_at = now(),
		       expires_at = $3
		 WHERE id = $1
		   AND status = 'running'`,
		exportID, filePath, expiresAt,
	)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) FailOrganizationExport(ctx context.Context, exportID, errorCode string) error {
	if errorCode == "" {
		errorCode = "generation_failed"
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE organization_exports
		   SET status = 'failed',
		       error_code = $2,
		       completed_at = now()
		 WHERE id = $1`,
		exportID, errorCode,
	)
	return err
}

func (s *Store) ExpireOrganizationExports(ctx context.Context) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		UPDATE organization_exports
		   SET status = 'expired'
		 WHERE status = 'ready'
		   AND expires_at IS NOT NULL
		   AND expires_at <= now()
		RETURNING COALESCE(file_path, '')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return nil, err
		}
		if path != "" {
			paths = append(paths, path)
		}
	}
	return paths, rows.Err()
}

type ExportMemberRow struct {
	UserID            string       `json:"user_id"`
	Email             string       `json:"email,omitempty"`
	Username          string       `json:"username,omitempty"`
	DisplayName       string       `json:"display_name"`
	Role              BusinessRole `json:"role"`
	Status            string       `json:"status"`
	MonitoringEnabled bool         `json:"monitoring_enabled"`
	CreatedAt         time.Time    `json:"created_at"`
}

func (s *Store) ExportMembers(ctx context.Context, businessID string) ([]ExportMemberRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT u.id::text, COALESCE(u.email, ''), COALESCE(u.username, ''),
		       u.display_name, m.role, m.status, m.monitoring_enabled, m.created_at
		  FROM memberships m
		  JOIN users u ON u.id = m.user_id
		 WHERE m.business_id = $1
		 ORDER BY m.created_at, u.display_name`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExportMemberRow{}
	for rows.Next() {
		var row ExportMemberRow
		if err := rows.Scan(
			&row.UserID, &row.Email, &row.Username, &row.DisplayName,
			&row.Role, &row.Status, &row.MonitoringEnabled, &row.CreatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type ExportActivityRow struct {
	ClientUUID  string  `json:"client_uuid"`
	UserID      string  `json:"user_id"`
	DeviceID    string  `json:"device_id"`
	Ts          int64   `json:"ts"`
	AppName     string  `json:"app_name"`
	WindowTitle *string `json:"window_title"`
	DurationS   int     `json:"duration_s"`
}

func (s *Store) ExportActivity(ctx context.Context, businessID string) ([]ExportActivityRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT client_uuid::text, user_id::text, device_id::text, ts,
		       app_name, window_title, duration_s
		  FROM activity_samples
		 WHERE business_id = $1
		 ORDER BY ts, id`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExportActivityRow{}
	for rows.Next() {
		var row ExportActivityRow
		if err := rows.Scan(
			&row.ClientUUID, &row.UserID, &row.DeviceID, &row.Ts,
			&row.AppName, &row.WindowTitle, &row.DurationS,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type ExportBrowserRow struct {
	ClientUUID string  `json:"client_uuid"`
	UserID     string  `json:"user_id"`
	DeviceID   string  `json:"device_id"`
	Ts         int64   `json:"ts"`
	URL        string  `json:"url"`
	PageTitle  *string `json:"page_title"`
	Browser    *string `json:"browser"`
	DurationS  int     `json:"duration_s"`
}

func (s *Store) ExportBrowser(ctx context.Context, businessID string) ([]ExportBrowserRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT client_uuid::text, user_id::text, device_id::text, ts,
		       url, page_title, browser, duration_s
		  FROM browser_visits
		 WHERE business_id = $1
		 ORDER BY ts, id`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExportBrowserRow{}
	for rows.Next() {
		var row ExportBrowserRow
		if err := rows.Scan(
			&row.ClientUUID, &row.UserID, &row.DeviceID, &row.Ts,
			&row.URL, &row.PageTitle, &row.Browser, &row.DurationS,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type ExportKeystrokeRow struct {
	ClientUUID string `json:"client_uuid"`
	UserID     string `json:"user_id"`
	DeviceID   string `json:"device_id"`
	TsBucket   int64  `json:"ts_bucket"`
	Count      int    `json:"count"`
}

func (s *Store) ExportKeystrokes(ctx context.Context, businessID string) ([]ExportKeystrokeRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT client_uuid::text, user_id::text, device_id::text, ts_bucket, count
		  FROM keystroke_buckets
		 WHERE business_id = $1
		 ORDER BY ts_bucket, id`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExportKeystrokeRow{}
	for rows.Next() {
		var row ExportKeystrokeRow
		if err := rows.Scan(
			&row.ClientUUID, &row.UserID, &row.DeviceID,
			&row.TsBucket, &row.Count,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ExportAudit(ctx context.Context, businessID string) ([]AuditEvent, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, business_id::text, actor_user_id::text, action, target_type,
		       COALESCE(target_id, ''), details,
		       extract(epoch FROM created_at)::bigint
		  FROM audit_events
		 WHERE business_id = $1
		 ORDER BY created_at, id`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEvent{}
	for rows.Next() {
		var row AuditEvent
		var raw []byte
		if err := rows.Scan(
			&row.ID, &row.BusinessID, &row.ActorUserID, &row.Action,
			&row.TargetType, &row.TargetID, &raw, &row.CreatedAt,
		); err != nil {
			return nil, err
		}
		row.Details = map[string]any{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &row.Details); err != nil {
				return nil, err
			}
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

type ExportScreenshotRow struct {
	ClientUUID     string  `json:"client_uuid"`
	UserID         string  `json:"user_id"`
	DeviceID       string  `json:"device_id"`
	Ts             int64   `json:"ts"`
	ByteSize       int     `json:"byte_size"`
	Width          *int    `json:"width"`
	Height         *int    `json:"height"`
	DisplayID      *int    `json:"display_id"`
	CaptureGroupID *string `json:"capture_group_id"`
	FilePath       string  `json:"-"`
}

func (s *Store) ExportScreenshots(ctx context.Context, businessID string) ([]ExportScreenshotRow, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT client_uuid::text, user_id::text, device_id::text, ts, byte_size,
		       width, height, display_id, capture_group_id::text, file_path
		  FROM screenshots
		 WHERE business_id = $1
		 ORDER BY ts, id`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ExportScreenshotRow{}
	for rows.Next() {
		var row ExportScreenshotRow
		if err := rows.Scan(
			&row.ClientUUID, &row.UserID, &row.DeviceID, &row.Ts, &row.ByteSize,
			&row.Width, &row.Height, &row.DisplayID, &row.CaptureGroupID, &row.FilePath,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ExportOrganization(ctx context.Context, businessID string) (Business, error) {
	return getBusiness(ctx, s.pool, businessID)
}

func (s *Store) ExportPrivacyRules(ctx context.Context, businessID string) ([]PrivacyRule, error) {
	return s.privacyRulesForBusiness(ctx, businessID)
}

func (s *Store) ExportDevices(ctx context.Context, businessID string) ([]Device, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+deviceColumns+`
		  FROM devices d
		 WHERE d.business_id = $1
		 ORDER BY d.first_seen_at, d.id`, businessID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Device{}
	for rows.Next() {
		row, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}
