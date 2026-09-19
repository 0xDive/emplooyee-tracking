import i18n from "./i18n";

const STABLE_API_CODES = [
  "validation_error",
  "authentication_required",
  "reauth_required",
  "mfa_required",
  "permission_denied",
  "not_found",
  "conflict",
  "member_blocked",
  "member_removed",
  "organization_archived",
  "organization_deletion_pending",
  "device_limit_reached",
  "device_revoked",
  "identifier_taken",
  "session_revoked",
  "rate_limited",
  "internal_error",
] as const;

export function stableApiErrorCode(error: unknown): string | null {
  const raw = String(error);
  for (const code of STABLE_API_CODES) {
    if (raw.includes(code)) return code;
  }
  return null;
}

export function localizedApiError(error: unknown, fallback?: string): string {
  const code = stableApiErrorCode(error);
  if (code) {
    const key = `errors.${code}`;
    if (i18n.exists(key, { ns: "common" })) {
      return i18n.t(key, { ns: "common" });
    }
  }
  return fallback ?? String(error);
}
