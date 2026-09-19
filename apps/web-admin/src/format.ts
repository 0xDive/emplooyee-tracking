// Small formatting helpers. Locale-aware via the active i18n language + Intl.
// Tabular figures handle alignment in CSS (.num).
import i18n, { activeLocale } from "./i18n";

export function fmtDuration(seconds: number): string {
  if (!seconds || seconds < 0) return "0m";
  const h = Math.floor(seconds / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  if (h > 0) return m > 0 ? `${h}h ${m}m` : `${h}h`;
  if (m > 0) return `${m}m`;
  return `${seconds}s`;
}

export function fmtRelative(unixSeconds: number | null): string {
  if (!unixSeconds) return i18n.t("time.never");
  const diff = Date.now() / 1000 - unixSeconds;
  if (diff < 60) return i18n.t("time.justNow");
  if (diff < 3600) return i18n.t("time.minutesAgo", { count: Math.floor(diff / 60) });
  if (diff < 86400) return i18n.t("time.hoursAgo", { count: Math.floor(diff / 3600) });
  if (diff < 7 * 86400) return i18n.t("time.daysAgo", { count: Math.floor(diff / 86400) });
  return new Date(unixSeconds * 1000).toLocaleDateString(activeLocale());
}

export function fmtTime(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toLocaleString(activeLocale(), {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / 1024 / 1024).toFixed(1)} MB`;
}

function datePartsInTimeZone(date: Date, timeZone: string) {
  const parts = new Intl.DateTimeFormat("en-US", {
    timeZone,
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
    hourCycle: "h23",
  }).formatToParts(date);
  const values = Object.fromEntries(parts.map((part) => [part.type, part.value]));
  return {
    year: Number(values.year),
    month: Number(values.month),
    day: Number(values.day),
    hour: Number(values.hour),
    minute: Number(values.minute),
    second: Number(values.second),
  };
}

function offsetAt(timestampMs: number, timeZone: string): number {
  const p = datePartsInTimeZone(new Date(timestampMs), timeZone);
  const localAsUtc = Date.UTC(p.year, p.month - 1, p.day, p.hour, p.minute, p.second);
  return localAsUtc - Math.floor(timestampMs / 1000) * 1000;
}

function zonedMidnightMs(dateText: string, timeZone: string): number {
  const [year, month, day] = dateText.split("-").map(Number);
  const utcMidnight = Date.UTC(year, month - 1, day, 0, 0, 0);
  let candidate = utcMidnight - offsetAt(utcMidnight, timeZone);
  candidate = utcMidnight - offsetAt(candidate, timeZone);
  return candidate;
}

function nextIsoDate(dateText: string): string {
  const [year, month, day] = dateText.split("-").map(Number);
  const next = new Date(Date.UTC(year, month - 1, day + 1));
  return `${next.getUTCFullYear()}-${String(next.getUTCMonth() + 1).padStart(2, "0")}-${String(
    next.getUTCDate(),
  ).padStart(2, "0")}`;
}

// Unix-second bounds for organization-local calendar dates. The end is exclusive,
// so DST days may legitimately contain 23 or 25 hours.
export function dayRangeToUnix(
  fromDate: string,
  toDate: string,
  timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
): { from: number; to: number } {
  const from = Math.floor(zonedMidnightMs(fromDate, timeZone) / 1000);
  const to = Math.floor(zonedMidnightMs(nextIsoDate(toDate), timeZone) / 1000);
  return { from, to };
}

export function isoDateInTimeZone(
  d: Date,
  timeZone = Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
): string {
  const p = datePartsInTimeZone(d, timeZone);
  return `${p.year}-${String(p.month).padStart(2, "0")}-${String(p.day).padStart(2, "0")}`;
}

export function isoDate(d: Date): string {
  return isoDateInTimeZone(d);
}

export function daysAgoIso(n: number): string {
  const d = new Date();
  d.setDate(d.getDate() - n);
  return isoDate(d);
}
