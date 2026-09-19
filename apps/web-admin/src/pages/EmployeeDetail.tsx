import { useCallback, useEffect, useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  listBusinessEmployees,
  listFormerMembers,
  reportActivity,
  reportBrowser,
  reportEmployees,
  reportKeystrokes,
  reportScreenshots,
} from "../api/endpoints";
import type {
  ActivityResponse,
  BrowserVisit,
  Employee,
  KeystrokeBucket,
  ReportEmployee,
  ScreenshotMeta,
} from "../api/types";
import { ActivityPanel } from "../components/reports/ActivityPanel";
import { BrowserPanel } from "../components/reports/BrowserPanel";
import { KeystrokePanel } from "../components/reports/KeystrokePanel";
import { ScreenshotGallery } from "../components/reports/ScreenshotGallery";
import { DevicesCard } from "../components/employee/DevicesCard";
import {
  Alert,
  Badge,
  Card,
  EmptyState,
  Skeleton,
} from "../components/ds";
import {
  dayRangeToUnix,
  fmtDuration,
  fmtRelative,
  isoDate,
  isoDateInTimeZone,
} from "../format";
import { useBusinesses } from "../useBusinesses";
import { memberTerms } from "../terms";
import { useAuth } from "../auth/AuthContext";
import { useDetailHeader } from "../detailHeader";
import { canManageDevices } from "../rbac";
import "../theme/employee-detail-v1.css";

type Tab = "overview" | "activity" | "screenshots" | "browser" | "devices";

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (!parts.length) return "?";
  if (parts.length === 1) return parts[0][0]?.toUpperCase() || "?";
  return `${parts[0][0] || ""}${parts[parts.length - 1][0] || ""}`.toUpperCase();
}

type Presence = "active" | "idle" | "offline" | "blocked" | "removed";

function presence(employee: Employee | null, report: ReportEmployee | null): Presence {
  if (employee?.status === "removed") return "removed";
  if (employee?.status === "blocked" || (employee && !employee.active)) return "blocked";
  const lastSeen = employee?.last_seen ?? report?.last_seen ?? null;
  if (!lastSeen) return "offline";
  const age = Math.max(0, Date.now() / 1000 - lastSeen);
  if (age < 5 * 60) return "active";
  if (age < 30 * 60) return "idle";
  return "offline";
}

function DetailSkeleton() {
  return (
    <div className="dashboard-loading" aria-hidden>
      <Skeleton width="100%" height={90} />
      <div className="employee-summary">
        {Array.from({ length: 4 }, (_, index) => (
          <Card className="employee-summary-card" key={index}>
            <Skeleton width="54%" height={14} />
            <Skeleton width="62%" height={30} />
            <Skeleton width="42%" height={12} />
          </Card>
        ))}
      </div>
      <Card>
        <Skeleton width="100%" height={280} />
      </Card>
    </div>
  );
}

export function EmployeeDetail() {
  const { t } = useTranslation("dashboard");
  const { id = "" } = useParams();
  const [params] = useSearchParams();
  const businessId = params.get("business");
  const requestedFormer = params.get("former") === "1";
  const { businesses } = useBusinesses();
  const { user } = useAuth();
  const { setTitle } = useDetailHeader();

  const business = businesses.find((item) => item.id === businessId);
  const terms = memberTerms(business?.kind);
  const isFormer = requestedFormer || false;
  const organizationReadOnly = Boolean(
    business?.archived_at || business?.deletion_scheduled_at,
  );
  const mayViewDevices = !isFormer && canManageDevices(business?.role);
  const mayManageDevices = mayViewDevices && !organizationReadOnly;

  const [mode, setMode] = useState<"day" | "range">("day");
  const [day, setDay] = useState(() => isoDate(new Date()));
  const [from, setFrom] = useState(() => isoDate(new Date()));
  const [to, setTo] = useState(() => isoDate(new Date()));
  const [tab, setTab] = useState<Tab>("overview");

  const [employee, setEmployee] = useState<ReportEmployee | null>(null);
  const [liveEmployee, setLiveEmployee] = useState<Employee | null>(null);
  const [activity, setActivity] = useState<ActivityResponse | null>(null);
  const [keystrokes, setKeystrokes] = useState<KeystrokeBucket[] | null>(null);
  const [visits, setVisits] = useState<BrowserVisit[] | null>(null);
  const [shots, setShots] = useState<ScreenshotMeta[] | null>(null);

  const [identityLoading, setIdentityLoading] = useState(true);
  const [reportsLoading, setReportsLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!businessId || !id) {
      setIdentityLoading(false);
      return;
    }

    let cancelled = false;
    setIdentityLoading(true);

    const identityRequest = requestedFormer
      ? listFormerMembers(businessId).then((former) => ({
          report: null as ReportEmployee | null,
          live: former.employees.find((item) => item.id === id) ?? null,
        }))
      : Promise.all([
          reportEmployees(businessId),
          listBusinessEmployees(businessId),
        ]).then(([report, live]) => ({
          report: report.employees.find((item) => item.id === id) ?? null,
          live: live.employees.find((item) => item.id === id) ?? null,
        }));

    identityRequest
      .then(({ report, live }) => {
        if (cancelled) return;
        setEmployee(report);
        setLiveEmployee(live);
      })
      .catch(() => {
        if (!cancelled) setError(t("detail.errorIdentity"));
      })
      .finally(() => {
        if (!cancelled) setIdentityLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [businessId, id, requestedFormer, t]);

  useEffect(() => {
    setTitle(employee?.display_name ?? liveEmployee?.display_name ?? null);
    return () => setTitle(null);
  }, [employee, liveEmployee, setTitle]);

  const loadReports = useCallback(async () => {
    if (!id || !businessId) return;

    const [fromDate, toDate] = mode === "day" ? [day, day] : [from, to];
    const range = dayRangeToUnix(
      fromDate,
      toDate,
      business?.timezone || "UTC",
    );

    if (range.from > range.to) {
      setError(t("detail.errorStartAfterEnd"));
      return;
    }

    setReportsLoading(true);
    setError(null);

    try {
      const [nextActivity, nextKeystrokes, nextBrowser, nextScreenshots] =
        await Promise.all([
          reportActivity(businessId, id, range.from, range.to),
          reportKeystrokes(businessId, id, range.from, range.to),
          reportBrowser(businessId, id, range.from, range.to),
          reportScreenshots(businessId, id, range.from, range.to),
        ]);

      setActivity(nextActivity);
      setKeystrokes(nextKeystrokes.buckets);
      setVisits(nextBrowser.visits);
      setShots(nextScreenshots.screenshots);
    } catch {
      setError(t("detail.errorRange"));
    } finally {
      setReportsLoading(false);
    }
  }, [business?.timezone, businessId, day, from, id, mode, t, to]);

  useEffect(() => {
    loadReports();
  }, [loadReports]);

  const today = business?.timezone
    ? isoDateInTimeZone(new Date(), business.timezone)
    : isoDate(new Date());

  useEffect(() => {
    if (!business?.timezone) return;
    const orgToday = isoDateInTimeZone(new Date(), business.timezone);
    setDay(orgToday);
    setFrom(orgToday);
    setTo(orgToday);
  }, [business?.timezone]);

  const summary = useMemo(() => {
    const activeSeconds =
      activity?.breakdown.reduce((sum, item) => sum + item.duration_s, 0) ?? 0;
    const top = activity?.breakdown
      ? [...activity.breakdown].sort((a, b) => b.duration_s - a.duration_s)[0]
      : undefined;
    const keypresses =
      keystrokes?.reduce((sum, bucket) => sum + bucket.count, 0) ?? 0;
    const topShare =
      top && activeSeconds > 0
        ? Math.round((top.duration_s / activeSeconds) * 100)
        : null;

    return {
      activeSeconds,
      topApp: top?.app_name ?? "—",
      topShare,
      keypresses,
      screenshots: shots?.length ?? 0,
    };
  }, [activity, keystrokes, shots]);

  const name = employee?.display_name ?? liveEmployee?.display_name ?? terms.one;
  const login =
    employee?.email ||
    employee?.username ||
    liveEmployee?.email ||
    liveEmployee?.username ||
    "—";
  const isSelf = employee?.role === "owner" || employee?.id === user?.id;
  const state = presence(liveEmployee, employee);
  const statusLabel =
    state === "removed"
      ? t("employees.lifecycle.removed")
      : t(`employees.status.${state}`);
  const lastSeen = liveEmployee?.last_seen ?? employee?.last_seen ?? null;

  const role = liveEmployee?.role ?? employee?.role;
  const roleLabel =
    role === "owner"
      ? t("detail.v1.owner")
      : role
        ? t(`employees.roles.${role}`)
        : terms.one;

  const tabs: Tab[] = mayViewDevices
    ? ["overview", "activity", "screenshots", "browser", "devices"]
    : ["overview", "activity", "screenshots", "browser"];

  if (!businessId) {
    return (
      <div className="employee-detail">
        <Link className="employee-detail__back" to="/employees">
          ← {terms.many}
        </Link>
        <Alert tone="info">{t("detail.noBusinessContext")}</Alert>
      </div>
    );
  }

  return (
    <div className="employee-detail">
      <Link className="employee-detail__back" to="/employees">
        ← {terms.many}
      </Link>

      {identityLoading ? (
        <DetailSkeleton />
      ) : !employee && !liveEmployee ? (
        <EmptyState
          title={t("detail.v1.notFound")}
          description={t("detail.v1.notFoundDescription")}
        />
      ) : (
        <>
          <section className="employee-entity">
            <span className="employee-entity__avatar">
              {initials(name)}
              <span
                className={`employee-entity__dot employee-entity__dot--${state}`}
              />
            </span>

            <div className="employee-entity__identity">
              <div className="employee-entity__name">
                {name}
                {isSelf && <Badge tone="brand">{t("dashboard.selfBadge")}</Badge>}
              </div>
              <div className="employee-entity__meta">
                <span>{login}</span>
                <span className="employee-entity__meta-sep">·</span>
                <span>{roleLabel}</span>
                <span className="employee-entity__meta-sep">·</span>
                <span>{statusLabel}</span>
                <span className="employee-entity__meta-sep">·</span>
                <span>{fmtRelative(lastSeen)}</span>
              </div>
            </div>

            <div className="employee-entity__current">
              <div className="employee-entity__current-label">
                {t("detail.v1.currentApplication")}
              </div>
              <div className="employee-entity__current-app">
                {liveEmployee?.current_app || "—"}
              </div>
            </div>
          </section>

          <section className="employee-rangebar">
            <div className="employee-rangebar__mode" role="tablist">
              <button
                type="button"
                role="tab"
                aria-selected={mode === "day"}
                className={`employee-rangebar__mode-btn${mode === "day" ? " is-active" : ""}`}
                onClick={() => setMode("day")}
              >
                {t("detail.singleDay")}
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={mode === "range"}
                className={`employee-rangebar__mode-btn${mode === "range" ? " is-active" : ""}`}
                onClick={() => setMode("range")}
              >
                {t("detail.dateRange")}
              </button>
            </div>

            <div className="employee-rangebar__dates">
              {mode === "day" ? (
                <label className="employee-date">
                  <span className="employee-date__label">{t("detail.day")}</span>
                  <input
                    className="ds-input"
                    type="date"
                    value={day}
                    max={today}
                    onChange={(event) => setDay(event.target.value)}
                  />
                </label>
              ) : (
                <>
                  <label className="employee-date">
                    <span className="employee-date__label">{t("detail.from")}</span>
                    <input
                      className="ds-input"
                      type="date"
                      value={from}
                      max={to}
                      onChange={(event) => setFrom(event.target.value)}
                    />
                  </label>
                  <label className="employee-date">
                    <span className="employee-date__label">{t("detail.to")}</span>
                    <input
                      className="ds-input"
                      type="date"
                      value={to}
                      min={from}
                      max={today}
                      onChange={(event) => setTo(event.target.value)}
                    />
                  </label>
                </>
              )}
            </div>
          </section>

          {error && (
            <div className="employee-detail__alert">
              <Alert tone="danger">{error}</Alert>
            </div>
          )}

          <div className="employee-summary">
            <Card className="employee-summary-card">
              <div className="employee-summary-card__label">
                {mode === "day"
                  ? t("detail.summary.activeTime")
                  : t("detail.summary.activeTimeRange")}
              </div>
              <div className="employee-summary-card__value ds-num">
                {fmtDuration(summary.activeSeconds)}
              </div>
              <div className="employee-summary-card__note">
                {mode === "day" ? t("detail.singleDay") : t("detail.dateRange")}
              </div>
            </Card>

            <Card className="employee-summary-card">
              <div className="employee-summary-card__label">
                {t("detail.summary.topApp")}
              </div>
              <div className="employee-summary-card__value">
                {summary.topApp}
              </div>
              <div className="employee-summary-card__note">
                {summary.topShare === null
                  ? t("detail.v1.noShare")
                  : t("detail.v1.shareOfTime", { value: summary.topShare })}
              </div>
            </Card>

            <Card className="employee-summary-card">
              <div className="employee-summary-card__label">
                {t("detail.summary.keypresses")}
              </div>
              <div className="employee-summary-card__value ds-num">
                {summary.keypresses.toLocaleString()}
              </div>
              <div className="employee-summary-card__note">
                {t("detail.v1.countsOnly")}
              </div>
            </Card>

            <Card className="employee-summary-card">
              <div className="employee-summary-card__label">
                {t("detail.summary.screenshots")}
              </div>
              <div className="employee-summary-card__value ds-num">
                {summary.screenshots.toLocaleString()}
              </div>
              <div className="employee-summary-card__note">
                {mode === "day" ? t("detail.singleDay") : t("detail.dateRange")}
              </div>
            </Card>
          </div>

          <div className="employee-tabs" role="tablist">
            {tabs.map((item) => (
              <button
                key={item}
                type="button"
                role="tab"
                aria-selected={tab === item}
                className={`employee-tab${tab === item ? " is-active" : ""}`}
                onClick={() => setTab(item)}
              >
                {t(`detail.tabs.${item}`)}
              </button>
            ))}
          </div>

          {reportsLoading && tab !== "devices" ? (
            <Card>
              <Skeleton width="100%" height={260} />
            </Card>
          ) : (
            <>
              {tab === "overview" && (
                <div className="employee-overview-grid">
                  <Card>
                    <div className="report-card__head">
                      <h2 className="report-card__title">
                        {t("detail.v1.currentState")}
                      </h2>
                    </div>
                    <div className="employee-overview-current">
                      <div className="employee-overview-line">
                        <span className="employee-overview-line__label">
                          {t("detail.v1.status")}
                        </span>
                        <span className="employee-overview-line__value">
                          {statusLabel}
                        </span>
                      </div>
                      <div className="employee-overview-line">
                        <span className="employee-overview-line__label">
                          {t("detail.v1.lastSeen")}
                        </span>
                        <span className="employee-overview-line__value">
                          {fmtRelative(lastSeen)}
                        </span>
                      </div>
                      <div className="employee-overview-line">
                        <span className="employee-overview-line__label">
                          {t("detail.v1.currentApplication")}
                        </span>
                        <span className="employee-overview-line__value">
                          {liveEmployee?.current_app || "—"}
                        </span>
                      </div>
                      <div className="employee-overview-line">
                        <span className="employee-overview-line__label">
                          {t("detail.v1.currentWindow")}
                        </span>
                        <span className="employee-overview-line__value">
                          {liveEmployee?.current_window || "—"}
                        </span>
                      </div>
                    </div>
                  </Card>

                  <Card>
                    <div className="report-card__head">
                      <h2 className="report-card__title">
                        {t("detail.v1.periodSummary")}
                      </h2>
                    </div>
                    <div className="employee-overview-current">
                      <div className="employee-overview-line">
                        <span className="employee-overview-line__label">
                          {t("detail.summary.activeTime")}
                        </span>
                        <span className="employee-overview-line__value ds-num">
                          {fmtDuration(summary.activeSeconds)}
                        </span>
                      </div>
                      <div className="employee-overview-line">
                        <span className="employee-overview-line__label">
                          {t("detail.summary.topApp")}
                        </span>
                        <span className="employee-overview-line__value">
                          {summary.topApp}
                        </span>
                      </div>
                      <div className="employee-overview-line">
                        <span className="employee-overview-line__label">
                          {t("detail.summary.keypresses")}
                        </span>
                        <span className="employee-overview-line__value ds-num">
                          {summary.keypresses.toLocaleString()}
                        </span>
                      </div>
                      <div className="employee-overview-line">
                        <span className="employee-overview-line__label">
                          {t("detail.summary.screenshots")}
                        </span>
                        <span className="employee-overview-line__value ds-num">
                          {summary.screenshots}
                        </span>
                      </div>
                    </div>
                  </Card>
                </div>
              )}

              {tab === "activity" && (
                <div className="employee-report-stack">
                  {activity ? (
                    <ActivityPanel data={activity} />
                  ) : (
                    <EmptyState title={t("detail.v1.noActivityData")} />
                  )}
                  {keystrokes ? (
                    <KeystrokePanel buckets={keystrokes} />
                  ) : (
                    <EmptyState title={t("detail.v1.noKeyboardData")} />
                  )}
                </div>
              )}

              {tab === "screenshots" && (
                <Card>
                  {shots ? (
                    <ScreenshotGallery shots={shots} businessId={businessId} />
                  ) : (
                    <EmptyState title={t("detail.v1.noScreenshotData")} />
                  )}
                </Card>
              )}

              {tab === "browser" &&
                (visits ? (
                  <BrowserPanel visits={visits} />
                ) : (
                  <EmptyState title={t("detail.v1.noBrowserData")} />
                ))}

              {tab === "devices" && mayViewDevices && (
                <DevicesCard
                  employee={liveEmployee}
                  employeeId={id}
                  businessId={businessId}
                  canChange={mayManageDevices}
                />
              )}
            </>
          )}
        </>
      )}
    </div>
  );
}
