-- +goose Up
-- Product Foundation v1. This migration is intentionally additive so existing
-- self-hosted installations can upgrade without resetting data.

-- Organization identity and operational defaults.
ALTER TABLE businesses
    DROP CONSTRAINT IF EXISTS businesses_kind_check;

ALTER TABLE businesses
    ADD CONSTRAINT businesses_kind_check
    CHECK (kind IN ('team', 'family', 'other'));

ALTER TABLE businesses
    ADD COLUMN timezone text NOT NULL DEFAULT 'UTC',
    ADD COLUMN week_starts_on smallint,
    ADD COLUMN default_member_monitoring_enabled boolean NOT NULL DEFAULT true,
    ADD COLUMN collect_app_activity boolean NOT NULL DEFAULT true,
    ADD COLUMN collect_window_titles boolean NOT NULL DEFAULT true,
    ADD COLUMN collect_screenshots boolean NOT NULL DEFAULT true,
    ADD COLUMN collect_browser_activity boolean NOT NULL DEFAULT true,
    ADD COLUMN collect_keystroke_counts boolean NOT NULL DEFAULT true,
    ADD COLUMN screenshot_capture_scope text NOT NULL DEFAULT 'active_window',
    ADD COLUMN activity_retention_days integer NOT NULL DEFAULT 180,
    ADD COLUMN browser_retention_days integer NOT NULL DEFAULT 90,
    ADD COLUMN keystroke_retention_days integer NOT NULL DEFAULT 90,
    ADD COLUMN audit_retention_days integer,
    ADD COLUMN device_limit integer,
    ADD COLUMN enrollment_token_ttl_s integer NOT NULL DEFAULT 86400,
    ADD COLUMN archived_at timestamptz,
    ADD COLUMN deletion_scheduled_at timestamptz,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE businesses
    ADD CONSTRAINT businesses_week_starts_on_check
        CHECK (week_starts_on IS NULL OR week_starts_on BETWEEN 0 AND 6),
    ADD CONSTRAINT businesses_screenshot_capture_scope_check
        CHECK (screenshot_capture_scope IN ('active_window', 'active_display', 'all_displays')),
    ADD CONSTRAINT businesses_activity_retention_check
        CHECK (activity_retention_days > 0),
    ADD CONSTRAINT businesses_browser_retention_check
        CHECK (browser_retention_days > 0),
    ADD CONSTRAINT businesses_keystroke_retention_check
        CHECK (keystroke_retention_days > 0),
    ADD CONSTRAINT businesses_audit_retention_check
        CHECK (audit_retention_days IS NULL OR audit_retention_days > 0),
    ADD CONSTRAINT businesses_device_limit_check
        CHECK (device_limit IS NULL OR device_limit > 0),
    ADD CONSTRAINT businesses_enrollment_ttl_check
        CHECK (enrollment_token_ttl_s BETWEEN 300 AND 604800);

-- Preserve the old screenshot-mode meaning while moving toward explicit scope.
UPDATE businesses
SET screenshot_capture_scope = CASE
    WHEN screenshot_mode = 'normal' THEN 'active_display'
    ELSE 'active_window'
END;

-- Managed members cannot weaken organization policy locally. Older desktop builds
-- still receive this compatibility field, but it is always false after upgrade.
UPDATE businesses SET allow_employee_override = false;

-- Organization-scoped member lifecycle.
ALTER TABLE memberships
    ADD COLUMN status text NOT NULL DEFAULT 'active',
    ADD COLUMN blocked_at timestamptz,
    ADD COLUMN removed_at timestamptz,
    ADD COLUMN updated_at timestamptz NOT NULL DEFAULT now();

ALTER TABLE memberships
    ADD CONSTRAINT memberships_status_check
    CHECK (status IN ('active', 'blocked', 'removed'));

CREATE INDEX idx_memberships_business_status
    ON memberships(business_id, status, role);

-- A managed desktop installation belongs to one organization. Existing devices
-- are backfilled only when the user's organization is unambiguous.
ALTER TABLE devices
    ADD COLUMN business_id uuid REFERENCES businesses(id) ON DELETE CASCADE;

UPDATE devices d
SET business_id = only_membership.business_id
FROM (
    SELECT user_id, min(business_id::text)::uuid AS business_id
    FROM memberships
    WHERE status = 'active'
    GROUP BY user_id
    HAVING count(*) = 1
) only_membership
WHERE d.user_id = only_membership.user_id
  AND d.business_id IS NULL;

CREATE INDEX idx_devices_business_user
    ON devices(business_id, user_id, revoked_at, last_seen_at DESC);

-- Multi-display captures share a group id but remain separate screenshot rows.
ALTER TABLE screenshots
    ADD COLUMN capture_group_id uuid;

CREATE INDEX idx_screenshots_capture_group
    ON screenshots(capture_group_id)
    WHERE capture_group_id IS NOT NULL;

-- Privacy rules supersede the long-term array-only skip list.
CREATE TABLE privacy_rules (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    business_id uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    kind        text NOT NULL CHECK (kind IN ('app', 'window_title')),
    match_type  text NOT NULL CHECK (match_type IN ('exact', 'contains')),
    pattern     text NOT NULL CHECK (length(btrim(pattern)) > 0 AND length(pattern) <= 200),
    enabled     boolean NOT NULL DEFAULT true,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_privacy_rules_business
    ON privacy_rules(business_id, enabled, kind);

-- Convert existing app exclusions into equivalent exact app rules.
INSERT INTO privacy_rules (business_id, kind, match_type, pattern)
SELECT b.id, 'app', 'exact', app
FROM businesses b
CROSS JOIN LATERAL unnest(b.screenshot_skip_apps) AS app
WHERE btrim(app) <> '';

-- First-class refresh sessions. Runtime migration to this table is performed in
-- application code; existing JWT refresh tokens remain valid during compatibility.
CREATE TABLE auth_sessions (
    id                    uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id               uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    refresh_token_hash    text NOT NULL UNIQUE,
    client_type           text NOT NULL DEFAULT 'web'
        CHECK (client_type IN ('web', 'desktop')),
    client_label          text NOT NULL DEFAULT '',
    auth_version_at_issue integer NOT NULL,
    created_at            timestamptz NOT NULL DEFAULT now(),
    last_used_at          timestamptz NOT NULL DEFAULT now(),
    expires_at            timestamptz NOT NULL,
    revoked_at            timestamptz
);

CREATE INDEX idx_auth_sessions_user_active
    ON auth_sessions(user_id, last_used_at DESC)
    WHERE revoked_at IS NULL;

CREATE TABLE security_events (
    id            bigserial PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_user_id uuid REFERENCES users(id) ON DELETE SET NULL,
    action        text NOT NULL,
    details       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_security_events_user_created
    ON security_events(user_id, created_at DESC);

-- TOTP MFA and one-time recovery codes.
CREATE TABLE user_mfa (
    user_id               uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    totp_secret_encrypted bytea NOT NULL,
    enabled_at            timestamptz,
    created_at            timestamptz NOT NULL DEFAULT now(),
    updated_at            timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE mfa_recovery_codes (
    id         uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    code_hash  text NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, code_hash)
);

CREATE INDEX idx_mfa_recovery_codes_user_unused
    ON mfa_recovery_codes(user_id, created_at)
    WHERE used_at IS NULL;

-- Background export jobs provide a safe path before destructive operations.
CREATE TABLE organization_exports (
    id            uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    business_id   uuid NOT NULL REFERENCES businesses(id) ON DELETE CASCADE,
    requested_by  uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    kind          text NOT NULL
        CHECK (kind IN ('activity_csv', 'activity_json', 'browser_csv', 'browser_json',
                        'keystrokes_csv', 'keystrokes_json', 'audit_csv', 'audit_json',
                        'screenshots_archive', 'full')),
    status        text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'running', 'ready', 'failed', 'expired')),
    file_path     text,
    error_code    text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    completed_at  timestamptz,
    expires_at    timestamptz
);

CREATE INDEX idx_organization_exports_business_created
    ON organization_exports(business_id, created_at DESC);

-- +goose Down
DROP TABLE organization_exports;
DROP TABLE mfa_recovery_codes;
DROP TABLE user_mfa;
DROP TABLE security_events;
DROP TABLE auth_sessions;
DROP TABLE privacy_rules;

DROP INDEX IF EXISTS idx_screenshots_capture_group;
ALTER TABLE screenshots DROP COLUMN capture_group_id;

DROP INDEX IF EXISTS idx_devices_business_user;
ALTER TABLE devices DROP COLUMN business_id;

DROP INDEX IF EXISTS idx_memberships_business_status;
ALTER TABLE memberships
    DROP CONSTRAINT IF EXISTS memberships_status_check,
    DROP COLUMN updated_at,
    DROP COLUMN removed_at,
    DROP COLUMN blocked_at,
    DROP COLUMN status;

ALTER TABLE businesses
    DROP CONSTRAINT IF EXISTS businesses_enrollment_ttl_check,
    DROP CONSTRAINT IF EXISTS businesses_device_limit_check,
    DROP CONSTRAINT IF EXISTS businesses_audit_retention_check,
    DROP CONSTRAINT IF EXISTS businesses_keystroke_retention_check,
    DROP CONSTRAINT IF EXISTS businesses_browser_retention_check,
    DROP CONSTRAINT IF EXISTS businesses_activity_retention_check,
    DROP CONSTRAINT IF EXISTS businesses_screenshot_capture_scope_check,
    DROP CONSTRAINT IF EXISTS businesses_week_starts_on_check,
    DROP COLUMN updated_at,
    DROP COLUMN deletion_scheduled_at,
    DROP COLUMN archived_at,
    DROP COLUMN enrollment_token_ttl_s,
    DROP COLUMN device_limit,
    DROP COLUMN audit_retention_days,
    DROP COLUMN keystroke_retention_days,
    DROP COLUMN browser_retention_days,
    DROP COLUMN activity_retention_days,
    DROP COLUMN screenshot_capture_scope,
    DROP COLUMN collect_keystroke_counts,
    DROP COLUMN collect_browser_activity,
    DROP COLUMN collect_screenshots,
    DROP COLUMN collect_window_titles,
    DROP COLUMN collect_app_activity,
    DROP COLUMN default_member_monitoring_enabled,
    DROP COLUMN week_starts_on,
    DROP COLUMN timezone;

ALTER TABLE businesses
    DROP CONSTRAINT IF EXISTS businesses_kind_check;
ALTER TABLE businesses
    ADD CONSTRAINT businesses_kind_check
    CHECK (kind IN ('team', 'family'));
