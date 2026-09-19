package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ErrDeviceRevoked is returned when a desktop that was revoked by an owner
// attempts to sync again with the same device UUID.
var ErrDeviceRevoked = errors.New("device revoked")

// DeviceMetadata is reported by the desktop during sync. Every field is optional
// so older clients remain compatible with the newer backend.
type DeviceMetadata struct {
	Label      *string
	Hostname   *string
	Platform   *string
	Arch       *string
	AppVersion *string
}

// Device is one ActiLens installation associated with a user account.
type Device struct {
	ID         string `json:"id"`
	UserID     string `json:"user_id"`
	BusinessID string `json:"business_id"`
	Label      string `json:"label"`
	Hostname   string `json:"hostname"`
	Platform   string `json:"platform"`
	Arch       string `json:"arch"`
	AppVersion     string `json:"app_version"`
	VersionStatus  string `json:"version_status"`
	FirstSeen  int64  `json:"first_seen"`
	LastSeen   *int64 `json:"last_seen"`
	RevokedAt  *int64 `json:"revoked_at"`
}

// AuditEvent is an owner-visible administrative action. Passwords, tokens and
// screenshot contents are intentionally never written to this table.
type AuditEvent struct {
	ID          int64          `json:"id"`
	BusinessID  string         `json:"business_id"`
	ActorUserID string         `json:"actor_user_id"`
	Action      string         `json:"action"`
	TargetType  string         `json:"target_type"`
	TargetID    string         `json:"target_id"`
	Details     map[string]any `json:"details"`
	CreatedAt   int64          `json:"created_at"`
}

const deviceColumns = `
	d.id,
	d.user_id,
	COALESCE(d.business_id::text, ''),
	COALESCE(d.label, ''),
	COALESCE(d.hostname, ''),
	COALESCE(d.platform, ''),
	COALESCE(d.arch, ''),
	COALESCE(d.app_version, ''),
	extract(epoch FROM d.first_seen_at)::bigint,
	CASE WHEN d.last_seen_at IS NULL THEN NULL ELSE extract(epoch FROM d.last_seen_at)::bigint END,
	CASE WHEN d.revoked_at IS NULL THEN NULL ELSE extract(epoch FROM d.revoked_at)::bigint END`

func scanDevice(row scanner) (Device, error) {
	var d Device
	err := row.Scan(
		&d.ID, &d.UserID, &d.BusinessID, &d.Label, &d.Hostname, &d.Platform, &d.Arch,
		&d.AppVersion, &d.FirstSeen, &d.LastSeen, &d.RevokedAt,
	)
	return d, err
}

func optionalText(v *string) any {
	if v == nil {
		return nil
	}
	s := strings.TrimSpace(*v)
	if s == "" {
		return nil
	}
	return s
}

// touchDeviceTx creates or refreshes a device and binds it to exactly one
// organization. Existing unbound migration-era devices may bind on their first
// explicit sync. A UUID can never move between users or organizations.
func touchDeviceTx(
	ctx context.Context,
	tx pgx.Tx,
	userID, businessID, deviceID string,
	meta DeviceMetadata,
) error {
	updateExisting := func() (bool, error) {
		var existingUser string
		var existingBusiness *string
		var revoked bool
		err := tx.QueryRow(ctx, `
			SELECT user_id, business_id::text, revoked_at IS NOT NULL
			  FROM devices
			 WHERE id = $1
			 FOR UPDATE`,
			deviceID,
		).Scan(&existingUser, &existingBusiness, &revoked)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if existingUser != userID {
			return true, ErrForbidden
		}
		if revoked {
			return true, ErrDeviceRevoked
		}
		if existingBusiness != nil && *existingBusiness != businessID {
			return true, ErrForbidden
		}
		_, err = tx.Exec(ctx, `
			UPDATE devices
			   SET business_id = COALESCE(business_id, $1),
			       label = COALESCE($2, label),
			       hostname = COALESCE($3, hostname),
			       platform = COALESCE($4, platform),
			       arch = COALESCE($5, arch),
			       app_version = COALESCE($6, app_version),
			       last_seen_at = now()
			 WHERE id = $7`,
			businessID,
			optionalText(meta.Label), optionalText(meta.Hostname),
			optionalText(meta.Platform), optionalText(meta.Arch),
			optionalText(meta.AppVersion), deviceID,
		)
		return true, err
	}

	if found, err := updateExisting(); found || err != nil {
		return err
	}

	// Serialize first-seen devices for this organization so concurrent enrollments
	// cannot both pass the configured organization-wide active-device limit.
	var limit *int
	if err := tx.QueryRow(ctx, `
		SELECT device_limit
		  FROM businesses
		 WHERE id = $1
		 FOR UPDATE`,
		businessID,
	).Scan(&limit); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	// Another concurrent sync may have inserted this UUID while we waited.
	if found, err := updateExisting(); found || err != nil {
		return err
	}

	if limit != nil {
		var count int
		if err := tx.QueryRow(ctx, `
			SELECT count(*)
			  FROM devices
			 WHERE business_id = $1
			   AND revoked_at IS NULL`,
			businessID,
		).Scan(&count); err != nil {
			return err
		}
		if count >= *limit {
			return ErrDeviceLimitReached
		}
	}

	_, err := tx.Exec(ctx, `
		INSERT INTO devices
			(id, user_id, business_id, label, hostname, platform, arch, app_version, last_seen_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,now())`,
		deviceID, userID, businessID,
		optionalText(meta.Label), optionalText(meta.Hostname),
		optionalText(meta.Platform), optionalText(meta.Arch), optionalText(meta.AppVersion),
	)
	return err
}

// TouchDevice refreshes last_seen for screenshot-only syncs and enforces
// organization binding, revocation, and device limits.
func (s *Store) TouchDevice(
	ctx context.Context,
	userID, businessID, deviceID string,
	meta DeviceMetadata,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := touchDeviceTx(ctx, tx, userID, businessID, deviceID, meta); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func employeeBusinessOwnedByTx(ctx context.Context, tx pgx.Tx, ownerID, employeeID string) (string, error) {
	var businessID string
	err := tx.QueryRow(ctx, `
		SELECT b.id
		  FROM memberships m
		  JOIN businesses b ON b.id = m.business_id
		 WHERE m.user_id = $1
		   AND m.role = 'employee'
		   AND b.owner_user_id = $2
		 ORDER BY m.created_at
		 LIMIT 1`, employeeID, ownerID).Scan(&businessID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return businessID, err
}

// ListEmployeeDevices returns every installation seen for a member the actor may manage.
func (s *Store) ListEmployeeDevices(ctx context.Context, actorID, employeeID string, businessID ...string) ([]Device, error) {
	var err error
	if len(businessID) > 0 && strings.TrimSpace(businessID[0]) != "" {
		_, err = memberAccessInBusiness(ctx, s.pool, actorID, employeeID, strings.TrimSpace(businessID[0]), PermissionManageDevices)
	} else {
		_, err = s.MemberAccessWithPermission(ctx, actorID, employeeID, PermissionManageDevices)
	}
	if err != nil {
		return nil, err
	}

	query := `SELECT `+deviceColumns+`
		FROM devices d
		WHERE d.user_id = $1`
	args := []any{employeeID}
	if len(businessID) > 0 && strings.TrimSpace(businessID[0]) != "" {
		query += ` AND d.business_id = $2`
		args = append(args, strings.TrimSpace(businessID[0]))
	}
	query += ` ORDER BY d.revoked_at NULLS FIRST, d.last_seen_at DESC NULLS LAST, d.first_seen_at DESC`
	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Device{}
	for rows.Next() {
		d, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// UpdateDevice lets an owner rename, revoke or restore a device belonging to one
// of their employees. Revocation affects subsequent sync and screenshot uploads.
func (s *Store) UpdateDevice(ctx context.Context, actorID, deviceID string, label *string, revoked *bool, businessID ...string) (Device, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Device{}, err
	}
	defer tx.Rollback(ctx)

	var employeeID, currentLabel string
	var currentRevoked bool
	err = tx.QueryRow(ctx, `
		SELECT user_id, COALESCE(label, ''), revoked_at IS NOT NULL
		  FROM devices WHERE id = $1`, deviceID).
		Scan(&employeeID, &currentLabel, &currentRevoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Device{}, ErrNotFound
	}
	if err != nil {
		return Device{}, err
	}

	var access MemberAccess
	if len(businessID) > 0 && strings.TrimSpace(businessID[0]) != "" {
		access, err = memberAccessInBusiness(ctx, tx, actorID, employeeID, strings.TrimSpace(businessID[0]), PermissionManageDevices)
	} else {
		access, err = memberAccessTx(ctx, tx, actorID, employeeID, PermissionManageDevices)
	}
	if err != nil {
		return Device{}, err
	}
	if err := lockMutableOrganizationTx(ctx, tx, access.BusinessID); err != nil {
		return Device{}, err
	}

	nextLabel := currentLabel
	if label != nil {
		nextLabel = strings.TrimSpace(*label)
	}
	nextRevoked := currentRevoked
	if revoked != nil {
		nextRevoked = *revoked
	}

	if currentRevoked && !nextRevoked {
		if access.BusinessID == "" {
			return Device{}, ErrForbidden
		}
		var limit *int
		if err := tx.QueryRow(ctx, `
			SELECT device_limit
			  FROM businesses
			 WHERE id = $1
			 FOR UPDATE`, access.BusinessID,
		).Scan(&limit); err != nil {
			return Device{}, err
		}
		if limit != nil {
			var count int
			if err := tx.QueryRow(ctx, `
				SELECT count(*)
				  FROM devices
				 WHERE business_id = $1
				   AND revoked_at IS NULL
				   AND id <> $2`,
				access.BusinessID, deviceID,
			).Scan(&count); err != nil {
				return Device{}, err
			}
			if count >= *limit {
				return Device{}, ErrDeviceLimitReached
			}
		}
	}

	row := tx.QueryRow(ctx, `UPDATE devices d
		SET label = NULLIF($1, ''),
		    revoked_at = CASE WHEN $2 THEN COALESCE(d.revoked_at, now()) ELSE NULL END
		WHERE d.id = $3
		RETURNING `+deviceColumns, nextLabel, nextRevoked, deviceID)
	d, err := scanDevice(row)
	if err != nil {
		return Device{}, err
	}

	action := "device.updated"
	if revoked != nil && *revoked != currentRevoked {
		if *revoked {
			action = "device.revoked"
		} else {
			action = "device.restored"
		}
	}
	if err := insertAuditTx(ctx, tx, access.BusinessID, actorID, action, "device", deviceID, map[string]any{
		"employee_id": employeeID,
		"label":       nextLabel,
	}); err != nil {
		return Device{}, err
	}

	if err := tx.Commit(ctx); err != nil {
		return Device{}, err
	}
	return d, nil
}

func insertAuditTx(ctx context.Context, tx pgx.Tx, businessID, actorUserID, action, targetType, targetID string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	encoded, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO audit_events (business_id, actor_user_id, action, target_type, target_id, details)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6::jsonb)`,
		businessID, actorUserID, action, targetType, targetID, string(encoded))
	return err
}

// ListAuditEvents returns recent administrative actions for a business.
func (s *Store) ListAuditEvents(ctx context.Context, actorID, businessID string, limit int) ([]AuditEvent, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	if err := s.BusinessPermissionOrForbidden(ctx, actorID, businessID, PermissionAudit); err != nil {
		return nil, err
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id, business_id, actor_user_id, action, target_type,
		       COALESCE(target_id, ''), details,
		       extract(epoch FROM created_at)::bigint
		  FROM audit_events
		 WHERE business_id = $1
		 ORDER BY created_at DESC, id DESC
		 LIMIT $2`, businessID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []AuditEvent{}
	for rows.Next() {
		var ev AuditEvent
		var raw []byte
		if err := rows.Scan(&ev.ID, &ev.BusinessID, &ev.ActorUserID, &ev.Action,
			&ev.TargetType, &ev.TargetID, &raw, &ev.CreatedAt); err != nil {
			return nil, err
		}
		ev.Details = map[string]any{}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &ev.Details); err != nil {
				return nil, err
			}
		}
		out = append(out, ev)
	}
	return out, rows.Err()
}


func (s *Store) ListBusinessActiveDevices(
	ctx context.Context,
	actorID, businessID string,
) ([]Device, error) {
	if err := s.BusinessPermissionOrForbidden(
		ctx, actorID, businessID, CapabilityDevicesView,
	); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+deviceColumns+`
		  FROM devices d
		  JOIN memberships m
		    ON m.user_id = d.user_id
		   AND m.business_id = d.business_id
		 WHERE d.business_id = $1
		   AND d.revoked_at IS NULL
		   AND m.status = 'active'
		 ORDER BY d.last_seen_at DESC NULLS LAST, d.first_seen_at DESC`,
		businessID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Device{}
	for rows.Next() {
		device, err := scanDevice(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, device)
	}
	return out, rows.Err()
}
