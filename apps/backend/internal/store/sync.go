package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

// ErrAmbiguousBusiness is returned when a user belongs to multiple businesses and
// the sync request didn't say which one the data is for.
var ErrAmbiguousBusiness = errors.New("ambiguous business")

// ErrMembershipUnavailable means the requested organization membership no longer
// exists or collection has been disabled. Sync paths use a shared row lock so an
// owner purge cannot race a final batch into the organization after deletion.
var ErrMembershipUnavailable = errors.New("membership unavailable")

func ensureMembershipCollectableTx(ctx context.Context, tx pgx.Tx, userID, businessID string) error {
	var enabled bool
	var status string
	var archived, deletionPending bool
	err := tx.QueryRow(ctx, `
		SELECT m.monitoring_enabled, m.status,
		       b.archived_at IS NOT NULL,
		       b.deletion_scheduled_at IS NOT NULL
		  FROM memberships m
		  JOIN businesses b ON b.id = m.business_id
		 WHERE m.user_id = $1 AND m.business_id = $2
		 FOR SHARE OF m`, userID, businessID,
	).Scan(&enabled, &status, &archived, &deletionPending)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrMembershipUnavailable
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
	switch status {
	case MemberStatusBlocked:
		return ErrMemberBlocked
	case MemberStatusRemoved:
		return ErrMemberRemoved
	case MemberStatusActive:
	default:
		return ErrMembershipUnavailable
	}
	if !enabled {
		return ErrMembershipUnavailable
	}
	return nil
}

// Row types mirror the desktop's local tables. Nullable columns use pointers.

type ActivityRow struct {
	ClientUUID      string
	Ts              int64
	AppName         string
	WindowTitle     *string
	Pid             *int
	DurationS       int
	ClientUpdatedAt int64
}

type KeystrokeRow struct {
	ClientUUID      string
	TsBucket        int64
	Count           int
	ClientUpdatedAt int64
}

type BrowserRow struct {
	ClientUUID      string
	Ts              int64
	URL             string
	PageTitle       *string
	Browser         *string
	DurationS       int
	ClientUpdatedAt int64
}

func (s *Store) ResolveBusinessForUser(ctx context.Context, userID string, explicit *string) (string, error) {
	if explicit != nil {
		member, err := s.IsMember(ctx, userID, *explicit)
		if err != nil {
			return "", err
		}
		if !member {
			return "", ErrForbidden
		}
		return *explicit, nil
	}

	rows, err := s.pool.Query(ctx, `SELECT business_id FROM memberships WHERE user_id = $1`, userID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(ids) {
	case 0:
		return "", ErrNotFound
	case 1:
		return ids[0], nil
	default:
		return "", ErrAmbiguousBusiness
	}
}

const (
	activityUpsert = `
INSERT INTO activity_samples
  (client_uuid, user_id, business_id, device_id, ts, app_name, window_title, pid, duration_s, client_updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (client_uuid) DO UPDATE SET
  ts = EXCLUDED.ts, app_name = EXCLUDED.app_name, window_title = EXCLUDED.window_title,
  pid = EXCLUDED.pid, duration_s = EXCLUDED.duration_s,
  client_updated_at = EXCLUDED.client_updated_at, received_at = now()`

	keystrokeUpsert = `
INSERT INTO keystroke_buckets
  (client_uuid, user_id, business_id, device_id, ts_bucket, count, client_updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7)
ON CONFLICT (client_uuid) DO UPDATE SET
  ts_bucket = EXCLUDED.ts_bucket, count = EXCLUDED.count,
  client_updated_at = EXCLUDED.client_updated_at, received_at = now()`

	browserUpsert = `
INSERT INTO browser_visits
  (client_uuid, user_id, business_id, device_id, ts, url, page_title, browser, duration_s, client_updated_at)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
ON CONFLICT (client_uuid) DO UPDATE SET
  ts = EXCLUDED.ts, url = EXCLUDED.url, page_title = EXCLUDED.page_title,
  browser = EXCLUDED.browser, duration_s = EXCLUDED.duration_s,
  client_updated_at = EXCLUDED.client_updated_at, received_at = now()`
)

// SyncBatch upserts a batch of activity/keystroke/browser rows for one user+business
// +device in a single transaction. Device ownership/revocation is checked before
// any activity rows are accepted.
func (s *Store) SyncBatch(ctx context.Context, userID, businessID, deviceID string, meta DeviceMetadata,
	act []ActivityRow, ks []KeystrokeRow, br []BrowserRow) error {

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if err := ensureMembershipCollectableTx(ctx, tx, userID, businessID); err != nil {
		return err
	}
	if err := touchDeviceTx(ctx, tx, userID, businessID, deviceID, meta); err != nil {
		return err
	}

	// Defense in depth for older agents: collection policy is enforced at ingest,
	// not trusted to the desktop alone. Disabled optional categories are acknowledged
	// by the handler but are not persisted; activity duration remains mandatory while
	// app/window identity is redacted according to policy.
	var collectApps, collectTitles, collectKeystrokes, collectBrowser bool
	if err := tx.QueryRow(ctx, `
		SELECT collect_app_activity, collect_window_titles,
		       collect_keystroke_counts, collect_browser_activity
		  FROM businesses
		 WHERE id = $1
		 FOR SHARE`, businessID,
	).Scan(&collectApps, &collectTitles, &collectKeystrokes, &collectBrowser); err != nil {
		return err
	}

	batch := &pgx.Batch{}
	for _, a := range act {
		appName, windowTitle, pid := a.AppName, a.WindowTitle, a.Pid
		if !collectApps {
			appName = ""
			pid = nil
		}
		if !collectTitles {
			windowTitle = nil
		}
		batch.Queue(activityUpsert, a.ClientUUID, userID, businessID, deviceID,
			a.Ts, appName, windowTitle, pid, a.DurationS, a.ClientUpdatedAt)
	}
	if collectKeystrokes {
		for _, k := range ks {
			batch.Queue(keystrokeUpsert, k.ClientUUID, userID, businessID, deviceID,
				k.TsBucket, k.Count, k.ClientUpdatedAt)
		}
	}
	if collectBrowser {
		for _, b := range br {
			batch.Queue(browserUpsert, b.ClientUUID, userID, businessID, deviceID,
				b.Ts, b.URL, b.PageTitle, b.Browser, b.DurationS, b.ClientUpdatedAt)
		}
	}

	if batch.Len() > 0 {
		res := tx.SendBatch(ctx, batch)
		for i := 0; i < batch.Len(); i++ {
			if _, err := res.Exec(); err != nil {
				res.Close()
				return err
			}
		}
		if err := res.Close(); err != nil {
			return err
		}
	}

	return tx.Commit(ctx)
}
