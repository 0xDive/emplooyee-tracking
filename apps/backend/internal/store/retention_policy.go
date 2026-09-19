package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// BusinessRetentionPolicy is the automatic retention configuration for one
// organization. Audit retention is intentionally omitted from automatic cleanup;
// the v1 product keeps audit history indefinitely by default.
type BusinessRetentionPolicy struct {
	ID             string
	ActivityDays   int
	ScreenshotDays *int
	BrowserDays    int
	KeystrokeDays  int
}

// BusinessesWithRetentionPolicies returns every organization's independent
// retention windows. A nil screenshot window means screenshots are kept forever.
func (s *Store) BusinessesWithRetentionPolicies(ctx context.Context) ([]BusinessRetentionPolicy, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, activity_retention_days, screenshot_retention_days,
		       browser_retention_days, keystroke_retention_days
		  FROM businesses
		 WHERE archived_at IS NULL
		   AND deletion_scheduled_at IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []BusinessRetentionPolicy{}
	for rows.Next() {
		var p BusinessRetentionPolicy
		if err := rows.Scan(
			&p.ID,
			&p.ActivityDays,
			&p.ScreenshotDays,
			&p.BrowserDays,
			&p.KeystrokeDays,
		); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// EnsureBusinessMutable rejects destructive retention/cleanup while an organization
// is archived or pending deletion. Read-only previews do not call this helper.
func (s *Store) EnsureBusinessMutable(ctx context.Context, businessID string) error {
	var archived, deletionPending bool
	err := s.pool.QueryRow(ctx, `
		SELECT archived_at IS NOT NULL, deletion_scheduled_at IS NOT NULL
		  FROM businesses
		 WHERE id = $1`, businessID,
	).Scan(&archived, &deletionPending)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if deletionPending {
		return ErrOrganizationDeletionPending
	}
	if archived {
		return ErrOrganizationArchived
	}
	return nil
}

// CountActivityBefore returns managed activity rows that a retention sweep would remove.
func (s *Store) CountActivityBefore(ctx context.Context, businessID string, cutoffTs int64) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM activity_samples WHERE business_id = $1 AND ts < $2`,
		businessID, cutoffTs,
	).Scan(&count)
	return count, err
}

// CountBrowserBefore returns managed browser rows that a retention sweep would remove.
func (s *Store) CountBrowserBefore(ctx context.Context, businessID string, cutoffTs int64) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM browser_visits WHERE business_id = $1 AND ts < $2`,
		businessID, cutoffTs,
	).Scan(&count)
	return count, err
}

// CountKeystrokesBefore returns managed keystroke buckets that a retention sweep would remove.
func (s *Store) CountKeystrokesBefore(ctx context.Context, businessID string, cutoffTs int64) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM keystroke_buckets WHERE business_id = $1 AND ts_bucket < $2`,
		businessID, cutoffTs,
	).Scan(&count)
	return count, err
}

func (s *Store) CountActivityRange(ctx context.Context, businessID string, fromTs, toTs int64) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM activity_samples WHERE business_id = $1 AND ts >= $2 AND ts < $3`,
		businessID, fromTs, toTs,
	).Scan(&count)
	return count, err
}

func (s *Store) CountBrowserRange(ctx context.Context, businessID string, fromTs, toTs int64) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM browser_visits WHERE business_id = $1 AND ts >= $2 AND ts < $3`,
		businessID, fromTs, toTs,
	).Scan(&count)
	return count, err
}

func (s *Store) CountKeystrokesRange(ctx context.Context, businessID string, fromTs, toTs int64) (int64, error) {
	var count int64
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM keystroke_buckets WHERE business_id = $1 AND ts_bucket >= $2 AND ts_bucket < $3`,
		businessID, fromTs, toTs,
	).Scan(&count)
	return count, err
}

func (s *Store) ScreenshotStatsRange(ctx context.Context, businessID string, fromTs, toTs int64) (int64, int64, error) {
	var count, bytes int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(byte_size), 0)
		  FROM screenshots
		 WHERE business_id = $1 AND ts >= $2 AND ts < $3`,
		businessID, fromTs, toTs,
	).Scan(&count, &bytes)
	return count, bytes, err
}

// ScreenshotStatsBefore returns the count and stored bytes that a screenshot
// retention or cleanup operation would remove.
func (s *Store) ScreenshotStatsBefore(ctx context.Context, businessID string, cutoffTs int64) (int64, int64, error) {
	var count, bytes int64
	err := s.pool.QueryRow(ctx, `
		SELECT count(*), COALESCE(sum(byte_size), 0)
		  FROM screenshots
		 WHERE business_id = $1 AND ts < $2`,
		businessID, cutoffTs,
	).Scan(&count, &bytes)
	return count, bytes, err
}

func (s *Store) DeleteActivityRange(ctx context.Context, businessID string, fromTs, toTs int64) (int64, error) {
	if err := s.EnsureBusinessMutable(ctx, businessID); err != nil {
		return 0, err
	}
	ct, err := s.pool.Exec(ctx,
		`DELETE FROM activity_samples WHERE business_id = $1 AND ts >= $2 AND ts < $3`,
		businessID, fromTs, toTs,
	)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

func (s *Store) DeleteBrowserRange(ctx context.Context, businessID string, fromTs, toTs int64) (int64, error) {
	if err := s.EnsureBusinessMutable(ctx, businessID); err != nil {
		return 0, err
	}
	ct, err := s.pool.Exec(ctx,
		`DELETE FROM browser_visits WHERE business_id = $1 AND ts >= $2 AND ts < $3`,
		businessID, fromTs, toTs,
	)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

func (s *Store) DeleteKeystrokesRange(ctx context.Context, businessID string, fromTs, toTs int64) (int64, error) {
	if err := s.EnsureBusinessMutable(ctx, businessID); err != nil {
		return 0, err
	}
	ct, err := s.pool.Exec(ctx,
		`DELETE FROM keystroke_buckets WHERE business_id = $1 AND ts_bucket >= $2 AND ts_bucket < $3`,
		businessID, fromTs, toTs,
	)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

func (s *Store) DeleteActivityBefore(ctx context.Context, businessID string, cutoffTs int64) (int64, error) {
	if err := s.EnsureBusinessMutable(ctx, businessID); err != nil {
		return 0, err
	}
	ct, err := s.pool.Exec(ctx,
		`DELETE FROM activity_samples WHERE business_id = $1 AND ts < $2`,
		businessID, cutoffTs,
	)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

func (s *Store) DeleteBrowserBefore(ctx context.Context, businessID string, cutoffTs int64) (int64, error) {
	if err := s.EnsureBusinessMutable(ctx, businessID); err != nil {
		return 0, err
	}
	ct, err := s.pool.Exec(ctx,
		`DELETE FROM browser_visits WHERE business_id = $1 AND ts < $2`,
		businessID, cutoffTs,
	)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}

func (s *Store) DeleteKeystrokesBefore(ctx context.Context, businessID string, cutoffTs int64) (int64, error) {
	if err := s.EnsureBusinessMutable(ctx, businessID); err != nil {
		return 0, err
	}
	ct, err := s.pool.Exec(ctx,
		`DELETE FROM keystroke_buckets WHERE business_id = $1 AND ts_bucket < $2`,
		businessID, cutoffTs,
	)
	if err != nil {
		return 0, err
	}
	return ct.RowsAffected(), nil
}
