export interface PublicBusiness {
  business_id: string;
  name: string;
  owner_name: string;
}

export type AccountType = "manager" | "parent";
export type BusinessRole = "owner" | "admin" | "manager" | "employee";

export interface User {
  id: string;
  email: string;
  username?: string;
  display_name: string;
  account_type: AccountType;
}

export interface Tokens {
  access_token: string;
  refresh_token: string;
  expires_in: number;
  session_id?: string;
}

export interface AuthResponse {
  user: User;
  tokens: Tokens;
  business_id?: string;
}

export type BusinessKind = "team" | "family" | "other";
export type ScreenshotMode = "privacy" | "normal";
export type ScreenshotCaptureScope = "active_window" | "active_display" | "all_displays";
export type MembershipStatus = "active" | "blocked" | "removed";

export interface PrivacyAppCategory {
  key: string;
  apps: string[];
}

export type PrivacyRuleKind = "app" | "window_title";
export type PrivacyRuleMatchType = "exact" | "contains";

export interface PrivacyRule {
  id: string;
  business_id: string;
  kind: PrivacyRuleKind;
  match_type: PrivacyRuleMatchType;
  pattern: string;
  enabled: boolean;
}

export interface Business {
  id: string;
  name: string;
  kind: BusinessKind;
  owner_user_id: string;
  timezone: string;
  week_starts_on: number | null;
  default_member_monitoring_enabled: boolean;
  collect_app_activity: boolean;
  collect_window_titles: boolean;
  collect_screenshots: boolean;
  collect_browser_activity: boolean;
  collect_keystroke_counts: boolean;
  screenshot_retention_days: number | null;
  screenshot_interval_s: number;
  screenshot_capture_scope: ScreenshotCaptureScope;
  idle_threshold_s: number;
  allow_employee_override: boolean;
  screenshot_mode: string;
  screenshot_skip_apps: string[];
  activity_retention_days: number;
  browser_retention_days: number;
  keystroke_retention_days: number;
  audit_retention_days: number | null;
  device_limit: number | null;
  enrollment_token_ttl_s: number;
  archived_at: string | null;
  deletion_scheduled_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface BusinessAccess {
  business: Business;
  role: BusinessRole;
}

export interface Membership {
  business_id: string;
  business_name: string;
  role: BusinessRole;
  status: MembershipStatus;
  monitoring_enabled: boolean;
}

export interface BusinessSettingsPatch {
  default_member_monitoring_enabled?: boolean;
  collect_app_activity?: boolean;
  collect_window_titles?: boolean;
  collect_screenshots?: boolean;
  collect_browser_activity?: boolean;
  collect_keystroke_counts?: boolean;
  screenshot_retention_days?: number | null;
  screenshot_interval_s?: number;
  screenshot_capture_scope?: ScreenshotCaptureScope;
  idle_threshold_s?: number;
  activity_retention_days?: number;
  browser_retention_days?: number;
  keystroke_retention_days?: number;
  audit_retention_days?: number | null;
  device_limit?: number | null;
  enrollment_token_ttl_s?: number;
  allow_employee_override?: boolean;
  screenshot_mode?: ScreenshotMode;
  screenshot_skip_apps?: string[];
}

export interface Employee {
  id: string;
  email: string;
  username?: string;
  display_name: string;
  active: boolean;
  role?: BusinessRole;
  status?: MembershipStatus;
  monitoring_enabled?: boolean;
  blocked_at?: string | null;
  removed_at?: string | null;
  last_seen?: number | null;
  current_app?: string | null;
  current_window?: string | null;
}

export interface Device {
  id: string;
  user_id: string;
  label: string;
  hostname: string;
  platform: string;
  arch: string;
  app_version: string;
  version_status: "current" | "outdated" | "unknown";
  first_seen: number;
  last_seen: number | null;
  revoked_at: number | null;
}

export interface DeviceHealthSummary {
  total: number;
  current: number;
  outdated: number;
  unknown: number;
  recommended_version: string;
}

export interface AuditEvent {
  id: number;
  business_id: string;
  actor_user_id: string;
  action: string;
  target_type: string;
  target_id: string;
  details: Record<string, unknown>;
  created_at: number;
}

export type OrganizationExportKind =
  | "activity_csv"
  | "activity_json"
  | "browser_csv"
  | "browser_json"
  | "keystrokes_csv"
  | "keystrokes_json"
  | "audit_csv"
  | "audit_json"
  | "screenshots_archive"
  | "full";

export type OrganizationExportStatus =
  | "pending"
  | "running"
  | "ready"
  | "failed"
  | "expired";

export interface OrganizationExport {
  id: string;
  business_id: string;
  requested_by: string;
  kind: OrganizationExportKind;
  status: OrganizationExportStatus;
  error_code: string | null;
  created_at: string;
  completed_at: string | null;
  expires_at: string | null;
}

export interface CreateEmployeeResponse {
  employee: Employee;
  business: Business;
}

export interface ReportEmployee {
  id: string;
  email: string;
  username?: string;
  display_name: string;
  role?: BusinessRole;
  status?: MembershipStatus;
  last_seen: number | null;
  active_today_s: number;
  active_yesterday_s: number;
  screenshots_today: number;
  screenshots_yesterday: number;
  focus_pct_today: number | null;
}

export interface ActivitySample {
  ts: number;
  app_name: string;
  window_title: string;
  duration_s: number;
}

export interface AppBreakdown {
  app_name: string;
  duration_s: number;
}

export interface ActivityResponse {
  samples: ActivitySample[];
  breakdown: AppBreakdown[];
}

export interface KeystrokeBucket {
  ts_bucket: number;
  count: number;
}

export interface BrowserVisit {
  ts: number;
  url: string;
  page_title: string;
  browser: string;
  duration_s: number;
}

export interface ScreenshotMeta {
  client_uuid: string;
  ts: number;
  byte_size: number;
  width: number;
  height: number;
  display_id: number | null;
  capture_group_id: string | null;
}

export interface ScreenshotsResponse {
  screenshots: ScreenshotMeta[];
  limit: number;
  offset: number;
}

export class ApiError extends Error {
  status: number;
  code: string | null;
  details: unknown;
  body: unknown;

  constructor(
    status: number,
    message: string,
    body: unknown,
    code: string | null = null,
    details: unknown = null,
  ) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
    this.body = body;
  }
}


export interface AuthSession {
  id: string;
  client_type: "web" | "desktop";
  client_label: string;
  created_at: string;
  last_used_at: string;
  expires_at: string;
  revoked_at: string | null;
  current: boolean;
}

export interface MFAState {
  enabled: boolean;
  enabled_at: string | null;
}

export interface AccountResponse {
  user: User;
  mfa: MFAState;
}

export interface MFASetupResponse {
  secret: string;
  otpauth_uri: string;
}

export interface RecoveryCodesResponse {
  recovery_codes: string[];
}

export interface OrganizationPatch {
  name?: string;
  kind?: BusinessKind;
  timezone?: string;
  week_starts_on?: number | null;
}

export interface OrganizationDeletionPreview {
  members: number;
  devices: number;
  activity: number;
  browser: number;
  keystrokes: number;
  screenshots: number;
  screenshot_bytes: number;
  exports: number;
}
