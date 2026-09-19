import { fetchAuthenticatedBlob, request } from "./client";
import { tokenStore } from "./tokenStore";
import {
  demoActivity,
  demoBrowser,
  demoBusinesses,
  demoEmployees,
  demoKeystrokes,
  demoRoster,
  demoScreenshots,
  isDemo,
} from "./demo";
import type {
  AccountResponse,
  AccountType,
  ActivityResponse,
  AuthSession,
  AuditEvent,
  AuthResponse,
  BrowserVisit,
  Business,
  BusinessAccess,
  BusinessRole,
  BusinessSettingsPatch,
  CreateEmployeeResponse,
  Device,
  DeviceHealthSummary,
  Employee,
  KeystrokeBucket,
  Membership,
  MFASetupResponse,
  MFAState,
  OrganizationExport,
  OrganizationExportKind,
  OrganizationDeletionPreview,
  OrganizationPatch,
  PrivacyAppCategory,
  PrivacyRule,
  PrivacyRuleKind,
  PrivacyRuleMatchType,
  RecoveryCodesResponse,
  PublicBusiness,
  ReportEmployee,
  ScreenshotsResponse,
  Tokens,
  User,
} from "./types";

function webClientLabel(): string {
  if (typeof navigator === "undefined") return "Web browser";
  const value = navigator.userAgent || "Web browser";
  return value.slice(0, 180);
}

// ---------- public ----------
export function listPublicBusinesses() {
  return request<{ businesses: PublicBusiness[] }>("/v1/public/businesses", {
    auth: false,
  });
}

// ---------- auth ----------
export async function login(identifier: string, password: string, business_id?: string) {
  const res = await request<AuthResponse>("/v1/auth/login", {
    method: "POST",
    auth: false,
    body: {
      identifier,
      password,
      business_id,
      client_type: "web",
      client_label: webClientLabel(),
    },
  });
  tokenStore.setSession(res.tokens, res.user);
  return res;
}

export async function register(
  identifier: string,
  password: string,
  display_name: string,
  account_type: AccountType = "manager",
) {
  const isEmail = identifier.includes("@");
  const res = await request<AuthResponse>("/v1/auth/register", {
    method: "POST",
    auth: false,
    body: {
      email: isEmail ? identifier : undefined,
      username: isEmail ? undefined : identifier.toLowerCase(),
      password,
      display_name,
      account_type,
      client_type: "web",
      client_label: webClientLabel(),
    },
  });
  tokenStore.setSession(res.tokens, res.user);
  return res;
}

export function refresh(refresh_token: string) {
  return request<Tokens>("/v1/auth/refresh", {
    method: "POST",
    auth: false,
    body: { refresh_token },
  });
}

export async function completeMFA(challengeToken: string, code: string) {
  const res = await request<AuthResponse>("/v1/auth/mfa/complete", {
    method: "POST",
    auth: false,
    body: {
      challenge_token: challengeToken,
      code,
      client_type: "web",
      client_label: webClientLabel(),
    },
  });
  tokenStore.setSession(res.tokens, res.user);
  return res;
}

export function getMe() {
  return request<User>("/v1/me");
}

export function listMyMemberships() {
  return request<{ memberships: Membership[] }>("/v1/memberships/mine");
}

// ---------- account & security ----------
export function getAccount() {
  return request<AccountResponse>("/v1/account");
}

export function updateOwnDisplayName(display_name: string) {
  return request<{ user: User }>("/v1/account/profile", {
    method: "PATCH",
    body: { display_name },
  });
}

export function updateOwnLoginIdentifiers(input: {
  current_password: string;
  email: string;
  username: string;
}) {
  return request<{ user: User; reauth_required: boolean }>("/v1/account/login-identifiers", {
    method: "PATCH",
    body: input,
  });
}

export function changeOwnPassword(current_password: string, new_password: string) {
  return request<{ status: string; reauth_required: boolean }>("/v1/account/password/change", {
    method: "POST",
    body: { current_password, new_password },
  });
}

export function createReauthGrant(current_password: string, code = "") {
  return request<{ reauth_token: string; expires_in: number }>("/v1/account/reauth", {
    method: "POST",
    body: { current_password, code },
  });
}

export function listAuthSessions() {
  return request<{ sessions: AuthSession[] }>("/v1/account/sessions");
}

export function revokeAuthSession(sessionId: string) {
  return request<{ status: string; reauth_required: boolean }>(
    `/v1/account/sessions/${sessionId}`,
    { method: "DELETE" },
  );
}

export function revokeOtherAuthSessions() {
  return request<{ status: string; count: number }>("/v1/account/sessions/revoke-others", {
    method: "POST",
  });
}

export function getMFAState() {
  return request<MFAState>("/v1/account/mfa");
}

export function beginMFASetup(current_password: string) {
  return request<MFASetupResponse>("/v1/account/mfa/totp/setup", {
    method: "POST",
    body: { current_password },
  });
}

export function confirmMFASetup(code: string) {
  return request<{ status: string; recovery_codes: string[] }>("/v1/account/mfa/totp/confirm", {
    method: "POST",
    body: { code },
  });
}

export function regenerateRecoveryCodes(current_password: string, code: string) {
  return request<RecoveryCodesResponse>("/v1/account/mfa/recovery/regenerate", {
    method: "POST",
    body: { current_password, code },
  });
}

export function disableMFA(current_password: string, code: string) {
  return request<{ status: string; reauth_required: boolean }>("/v1/account/mfa/disable", {
    method: "POST",
    body: { current_password, code },
  });
}

export function resetMemberMFA(businessId: string, userId: string) {
  return request<{ status: string }>(
    `/v1/businesses/${businessId}/members/${userId}/mfa/reset`,
    { method: "POST" },
  );
}

// ---------- businesses ----------
export function createBusiness(
  name: string,
  input: { kind?: Business["kind"]; timezone?: string; week_starts_on?: number | null } = {},
) {
  return request<Business>("/v1/businesses", {
    method: "POST",
    body: { name, ...input },
  });
}

export function getOrganization(id: string) {
  return request<BusinessAccess>(`/v1/businesses/${id}`);
}

export function updateOrganization(id: string, patch: OrganizationPatch) {
  return request<Business>(`/v1/businesses/${id}`, {
    method: "PATCH",
    body: patch,
  });
}

export function archiveOrganization(id: string) {
  return request<{ business: Business }>(`/v1/businesses/${id}/archive`, {
    method: "POST",
  });
}

export function restoreOrganization(id: string) {
  return request<{ business: Business }>(`/v1/businesses/${id}/restore`, {
    method: "POST",
  });
}

export function transferOrganizationOwnership(
  id: string,
  targetUserId: string,
  reauthToken: string,
) {
  return request<{ status: string; reauth_required: boolean }>(
    `/v1/businesses/${id}/transfer-ownership`,
    {
      method: "POST",
      body: { target_user_id: targetUserId, reauth_token: reauthToken },
    },
  );
}

export function organizationDeletionPreview(id: string) {
  return request<OrganizationDeletionPreview>(
    `/v1/businesses/${id}/deletion-preview`,
  );
}

export function scheduleOrganizationDeletion(
  id: string,
  reauthToken: string,
  confirmationName: string,
) {
  return request<{ business: Business }>(`/v1/businesses/${id}/schedule-deletion`, {
    method: "POST",
    body: {
      reauth_token: reauthToken,
      confirmation_name: confirmationName,
    },
  });
}

export function cancelOrganizationDeletion(id: string) {
  return request<{ business: Business }>(`/v1/businesses/${id}/cancel-deletion`, {
    method: "POST",
  });
}

export function listMyBusinesses() {
  if (isDemo()) return Promise.resolve({ businesses: demoBusinesses });
  return request<{ businesses: Business[] }>("/v1/businesses/mine");
}

export function listConsoleBusinesses() {
  if (isDemo()) {
    return Promise.resolve({
      businesses: demoBusinesses.map((business) => ({ business, role: "owner" as BusinessRole })),
    });
  }
  return request<{ businesses: BusinessAccess[] }>("/v1/businesses/console");
}

export function updateMemberRole(businessId: string, userId: string, role: Exclude<BusinessRole, "owner">) {
  return request<{ status: string; role: BusinessRole }>(
    `/v1/businesses/${businessId}/members/${userId}/role`,
    { method: "PATCH", body: { role } },
  );
}

export function updateMemberMonitoring(businessId: string, userId: string, enabled: boolean) {
  return request<{ status: string; monitoring_enabled: boolean }>(
    `/v1/businesses/${businessId}/members/${userId}/monitoring`,
    { method: "PATCH", body: { enabled } },
  );
}

export function updateBusinessSettings(
  id: string,
  patch: BusinessSettingsPatch,
  confirmRetentionReduction = false,
) {
  return request<{ status: string }>(`/v1/businesses/${id}/settings`, {
    method: "PATCH",
    body: {
      ...patch,
      ...(confirmRetentionReduction
        ? { confirm_retention_reduction: true }
        : {}),
    },
  });
}

export type RetentionDataClass =
  | "activity"
  | "screenshots"
  | "browser"
  | "keystrokes";

export interface RetentionPreview {
  data_class: RetentionDataClass;
  days?: number;
  from?: number;
  to?: number;
  affected_count: number;
  bytes_freed: number;
}

export function previewRetention(
  id: string,
  dataClass: RetentionDataClass,
  days: number,
) {
  return request<RetentionPreview>(`/v1/businesses/${id}/retention/preview`, {
    query: { class: dataClass, days },
  });
}

export function previewCleanupRange(
  id: string,
  dataClass: RetentionDataClass,
  from: number,
  to: number,
) {
  return request<RetentionPreview>(`/v1/businesses/${id}/retention/preview`, {
    query: { class: dataClass, from, to },
  });
}

export function defaultMonitoringImpact(id: string, enabled: boolean) {
  return request<{ affected_count: number }>(
    `/v1/businesses/${id}/settings/default-monitoring-impact`,
    { query: { enabled: enabled ? "true" : "false" } },
  );
}

export function updateDefaultMonitoring(
  id: string,
  enabled: boolean,
  applyExisting: boolean,
) {
  return request<{ status: string; affected_count: number }>(
    `/v1/businesses/${id}/settings/default-monitoring`,
    {
      method: "POST",
      body: { enabled, apply_existing: applyExisting },
    },
  );
}

export function getPrivacyApps() {
  return request<{ categories: PrivacyAppCategory[] }>("/v1/public/screenshot-privacy-apps");
}

export function listPrivacyRules(businessId: string) {
  return request<{ rules: PrivacyRule[] }>(`/v1/businesses/${businessId}/privacy-rules`);
}

export function createPrivacyRule(
  businessId: string,
  input: { kind: PrivacyRuleKind; match_type: PrivacyRuleMatchType; pattern: string },
) {
  return request<{ rule: PrivacyRule }>(`/v1/businesses/${businessId}/privacy-rules`, {
    method: "POST",
    body: input,
  });
}

export function updatePrivacyRule(
  businessId: string,
  ruleId: string,
  patch: Partial<Pick<PrivacyRule, "kind" | "match_type" | "pattern" | "enabled">>,
) {
  return request<{ rule: PrivacyRule }>(
    `/v1/businesses/${businessId}/privacy-rules/${ruleId}`,
    { method: "PATCH", body: patch },
  );
}

export function deletePrivacyRule(businessId: string, ruleId: string) {
  return request<{ status: string }>(
    `/v1/businesses/${businessId}/privacy-rules/${ruleId}`,
    { method: "DELETE" },
  );
}

export function cleanupScreenshots(id: string, olderThanDays: number) {
  return request<{ deleted_count: number; bytes_freed: number }>(
    `/v1/businesses/${id}/screenshots/cleanup`,
    { method: "POST", query: { older_than_days: olderThanDays } },
  );
}

export interface CleanupClassResult {
  data_class: RetentionDataClass;
  deleted_count: number;
  bytes_freed: number;
}

export interface CleanupResult {
  results: CleanupClassResult[];
  deleted_count: number;
  bytes_freed: number;
}

export type CleanupWindow =
  | { older_than_days: number; from?: never; to?: never }
  | { from: number; to: number; older_than_days?: never };

export function cleanupData(
  id: string,
  dataClasses: RetentionDataClass[],
  window: CleanupWindow,
) {
  return request<CleanupResult>(`/v1/businesses/${id}/data/cleanup`, {
    method: "POST",
    body: {
      data_classes: dataClasses,
      ...window,
    },
  });
}

export function createOrganizationExport(
  businessId: string,
  kind: OrganizationExportKind,
) {
  return request<{ export: OrganizationExport }>(
    `/v1/businesses/${businessId}/exports`,
    { method: "POST", body: { kind } },
  );
}

export function listOrganizationExports(businessId: string, limit = 25) {
  return request<{ exports: OrganizationExport[] }>(
    `/v1/businesses/${businessId}/exports`,
    { query: { limit } },
  );
}

export function getOrganizationExport(businessId: string, exportId: string) {
  return request<{ export: OrganizationExport }>(
    `/v1/businesses/${businessId}/exports/${exportId}`,
  );
}

export function downloadOrganizationExport(businessId: string, exportId: string) {
  return fetchAuthenticatedBlob(
    `/v1/businesses/${businessId}/exports/${exportId}/download`,
  );
}

export function listAuditEvents(businessId: string, limit = 100) {
  return request<{ events: AuditEvent[] }>(`/v1/businesses/${businessId}/audit`, {
    query: { limit },
  });
}

// ---------- employees ----------
export function createEmployee(input: {
  email?: string;
  username?: string;
  password: string;
  display_name: string;
  business_id: string;
}) {
  const { business_id, ...body } = input;
  return request<CreateEmployeeResponse>(
    `/v1/businesses/${business_id}/members`,
    { method: "POST", body },
  );
}

export function listBusinessEmployees(businessId: string) {
  if (isDemo()) return Promise.resolve({ employees: demoEmployees() });
  return request<{ employees: Employee[] }>(`/v1/businesses/${businessId}/employees`);
}

export function listFormerMembers(businessId: string) {
  if (isDemo()) return Promise.resolve({ employees: [] as Employee[] });
  return request<{ employees: Employee[] }>(`/v1/businesses/${businessId}/members/former`);
}

export function blockMember(businessId: string, userId: string) {
  return request<{ status: string }>(
    `/v1/businesses/${businessId}/members/${userId}/block`,
    { method: "POST" },
  );
}

export function unblockMember(businessId: string, userId: string) {
  return request<{ status: string }>(
    `/v1/businesses/${businessId}/members/${userId}/unblock`,
    { method: "POST" },
  );
}

export function removeMember(businessId: string, userId: string) {
  return request<{ status: string }>(
    `/v1/businesses/${businessId}/members/${userId}/remove`,
    { method: "POST" },
  );
}

export function restoreMember(
  businessId: string,
  userId: string,
  monitoringEnabled: boolean,
) {
  return request<{ status: string }>(
    `/v1/businesses/${businessId}/members/${userId}/restore`,
    { method: "POST", body: { monitoring_enabled: monitoringEnabled } },
  );
}

export function updateEmployee(id: string, patch: {
  email?: string;
  username?: string;
  display_name?: string;
  active?: boolean;
}) {
  return request<{ employee: Employee }>(`/v1/employees/${id}`, { method: "PATCH", body: patch });
}

export function resetEmployeePassword(id: string, password: string) {
  return request<{ status: string }>(`/v1/employees/${id}/reset-password`, {
    method: "POST", body: { password },
  });
}

export function updateManagedMemberIdentity(
  businessId: string,
  userId: string,
  patch: { email?: string; username?: string; display_name?: string },
) {
  return request<{ employee: Employee }>(
    `/v1/businesses/${businessId}/members/${userId}/profile`,
    { method: "PATCH", body: patch },
  );
}

export function resetManagedMemberPassword(
  businessId: string,
  userId: string,
  password: string,
) {
  return request<{ status: string }>(
    `/v1/businesses/${businessId}/members/${userId}/reset-password`,
    { method: "POST", body: { password } },
  );
}

export function archiveEmployee(id: string) {
  return request<{ status: string }>(`/v1/employees/${id}`, { method: "DELETE" });
}

export interface PermanentMemberPurgeResult {
  activity_deleted: number;
  keystrokes_deleted: number;
  browser_deleted: number;
  screenshots_deleted: number;
  enrollments_deleted: number;
  account_tombstoned: boolean;
}

export function permanentlyDeleteMember(businessId: string, userId: string) {
  return request<{ status: string; bytes_freed: number; result: PermanentMemberPurgeResult }>(
    `/v1/businesses/${businessId}/members/${userId}/purge`,
    { method: "DELETE" },
  );
}

export function listEmployeeDevices(employeeId: string, businessId: string) {
  return request<{ devices: Device[] }>(
    `/v1/businesses/${businessId}/members/${employeeId}/devices`,
  );
}

export function updateDevice(
  id: string,
  patch: { label?: string; revoked?: boolean },
  businessId: string,
) {
  return request<{ device: Device }>(`/v1/businesses/${businessId}/devices/${id}`, {
    method: "PATCH",
    body: patch,
  });
}

export function getDeviceHealth(businessId: string) {
  return request<DeviceHealthSummary>(`/v1/businesses/${businessId}/devices/health`);
}

// ---------- reports ----------
export function reportEmployees(businessId: string) {
  if (isDemo()) return Promise.resolve({ employees: demoRoster() });
  return request<{ employees: ReportEmployee[] }>("/v1/reports/employees", {
    query: { business_id: businessId },
  });
}

export function reportActivity(
  businessId: string,
  employeeId: string,
  from: number,
  to: number,
) {
  if (isDemo()) return Promise.resolve(demoActivity(employeeId));
  return request<ActivityResponse>(`/v1/reports/employees/${employeeId}/activity`, {
    query: { business_id: businessId, from, to },
  });
}

export function reportKeystrokes(
  businessId: string,
  employeeId: string,
  from: number,
  to: number,
) {
  if (isDemo()) return Promise.resolve(demoKeystrokes(employeeId));
  return request<{ buckets: KeystrokeBucket[] }>(
    `/v1/reports/employees/${employeeId}/keystrokes`,
    { query: { business_id: businessId, from, to } },
  );
}

export function reportBrowser(
  businessId: string,
  employeeId: string,
  from: number,
  to: number,
) {
  if (isDemo()) return Promise.resolve(demoBrowser(employeeId));
  return request<{ visits: BrowserVisit[] }>(`/v1/reports/employees/${employeeId}/browser`, {
    query: { business_id: businessId, from, to },
  });
}

export function reportScreenshots(
  businessId: string,
  employeeId: string,
  from: number,
  to: number,
  limit = 60,
  offset = 0,
) {
  if (isDemo()) return Promise.resolve(demoScreenshots(employeeId));
  return request<ScreenshotsResponse>(`/v1/reports/employees/${employeeId}/screenshots`, {
    query: { business_id: businessId, from, to, limit, offset },
  });
}
