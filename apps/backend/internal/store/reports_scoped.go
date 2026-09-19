package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

type ReportAccess struct {
	ViewerRole   BusinessRole
	TargetStatus string
	Archived     bool
}

func (s *Store) ReportAccessInBusiness(
	ctx context.Context,
	viewerID, targetID, businessID string,
) (ReportAccess, error) {
	var access ReportAccess
	var deletionPending bool
	err := s.pool.QueryRow(ctx, `
		SELECT viewer.role, target.status,
		       b.archived_at IS NOT NULL,
		       b.deletion_scheduled_at IS NOT NULL
		  FROM memberships viewer
		  JOIN memberships target
		    ON target.business_id = viewer.business_id
		  JOIN businesses b ON b.id = viewer.business_id
		 WHERE viewer.user_id = $1
		   AND target.user_id = $2
		   AND viewer.business_id = $3
		   AND viewer.status = 'active'`,
		viewerID, targetID, businessID,
	).Scan(&access.ViewerRole, &access.TargetStatus, &access.Archived, &deletionPending)
	if errors.Is(err, pgx.ErrNoRows) {
		return ReportAccess{}, ErrForbidden
	}
	if err != nil {
		return ReportAccess{}, err
	}
	if deletionPending {
		access.Archived = true
	}
	if !roleAllows(access.ViewerRole, CapabilityReportsView) {
		return ReportAccess{}, ErrForbidden
	}
	if access.TargetStatus == MemberStatusRemoved &&
		access.ViewerRole != RoleOwner && access.ViewerRole != RoleAdmin {
		return ReportAccess{}, ErrForbidden
	}
	if access.Archived && access.ViewerRole != RoleOwner && access.ViewerRole != RoleAdmin {
		return ReportAccess{}, ErrForbidden
	}
	switch access.TargetStatus {
	case MemberStatusActive, MemberStatusBlocked, MemberStatusRemoved:
	default:
		return ReportAccess{}, ErrForbidden
	}
	return access, nil
}

func (s *Store) ActivityReportInBusiness(
	ctx context.Context,
	employeeID, businessID string,
	from, to int64,
) ([]ActivitySample, []AppBreakdown, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ts, app_name, window_title, duration_s
		  FROM activity_samples
		 WHERE user_id = $1 AND business_id = $2 AND ts >= $3 AND ts < $4
		 ORDER BY ts`,
		employeeID, businessID, from, to,
	)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	samples := []ActivitySample{}
	for rows.Next() {
		var row ActivitySample
		if err := rows.Scan(&row.Ts, &row.AppName, &row.WindowTitle, &row.DurationS); err != nil {
			return nil, nil, err
		}
		samples = append(samples, row)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}

	breakdownRows, err := s.pool.Query(ctx, `
		SELECT app_name, sum(duration_s)
		  FROM activity_samples
		 WHERE user_id = $1 AND business_id = $2 AND ts >= $3 AND ts < $4
		 GROUP BY app_name
		 ORDER BY sum(duration_s) DESC`,
		employeeID, businessID, from, to,
	)
	if err != nil {
		return nil, nil, err
	}
	defer breakdownRows.Close()

	breakdown := []AppBreakdown{}
	for breakdownRows.Next() {
		var row AppBreakdown
		if err := breakdownRows.Scan(&row.AppName, &row.DurationS); err != nil {
			return nil, nil, err
		}
		breakdown = append(breakdown, row)
	}
	return samples, breakdown, breakdownRows.Err()
}

func (s *Store) KeystrokesReportInBusiness(
	ctx context.Context,
	employeeID, businessID string,
	from, to int64,
) ([]KeystrokeBucket, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ts_bucket, count
		  FROM keystroke_buckets
		 WHERE user_id = $1 AND business_id = $2
		   AND ts_bucket >= $3 AND ts_bucket < $4
		 ORDER BY ts_bucket`,
		employeeID, businessID, from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []KeystrokeBucket{}
	for rows.Next() {
		var row KeystrokeBucket
		if err := rows.Scan(&row.TsBucket, &row.Count); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) BrowserReportInBusiness(
	ctx context.Context,
	employeeID, businessID string,
	from, to int64,
) ([]BrowserVisit, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT ts, url, page_title, browser, duration_s
		  FROM browser_visits
		 WHERE user_id = $1 AND business_id = $2 AND ts >= $3 AND ts < $4
		 ORDER BY ts DESC`,
		employeeID, businessID, from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BrowserVisit{}
	for rows.Next() {
		var row BrowserVisit
		if err := rows.Scan(&row.Ts, &row.URL, &row.PageTitle, &row.Browser, &row.DurationS); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ScreenshotsReportInBusiness(
	ctx context.Context,
	employeeID, businessID string,
	from, to int64,
	limit, offset int,
) ([]ScreenshotMeta, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT client_uuid, ts, byte_size, width, height, display_id, capture_group_id
		  FROM screenshots
		 WHERE user_id = $1 AND business_id = $2 AND ts >= $3 AND ts < $4
		 ORDER BY ts DESC LIMIT $5 OFFSET $6`,
		employeeID, businessID, from, to, limit, offset,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ScreenshotMeta{}
	for rows.Next() {
		var row ScreenshotMeta
		if err := rows.Scan(
			&row.ClientUUID, &row.Ts, &row.ByteSize, &row.Width, &row.Height,
			&row.DisplayID, &row.CaptureGroupID,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func (s *Store) ScreenshotPathInBusiness(
	ctx context.Context,
	viewerID, businessID, clientUUID string,
) (string, error) {
	var path, targetUserID string
	err := s.pool.QueryRow(ctx, `
		SELECT file_path, user_id
		  FROM screenshots
		 WHERE client_uuid = $1 AND business_id = $2`,
		clientUUID, businessID,
	).Scan(&path, &targetUserID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if _, err := s.ReportAccessInBusiness(ctx, viewerID, targetUserID, businessID); err != nil {
		if errors.Is(err, ErrForbidden) {
			return "", ErrNotFound
		}
		return "", err
	}
	return path, nil
}
