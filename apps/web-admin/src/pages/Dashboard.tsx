import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Link, useNavigate } from "react-router-dom";
import { Trans, useTranslation } from "react-i18next";
import {
  getDeviceHealth,
  listBusinessEmployees,
  reportEmployees,
} from "../api/endpoints";
import type {
  DeviceHealthSummary,
  Employee,
  ReportEmployee,
} from "../api/types";
import { Alert, Card, EmptyState, Skeleton } from "../components/ds";
import { fmtRelative } from "../format";
import { useBusinesses } from "../useBusinesses";
import { memberTerms } from "../terms";
import { useAuth } from "../auth/AuthContext";
import "../theme/dashboard-v1.css";

function Icon({
  children,
  size = 18,
}: {
  children: ReactNode;
  size?: number;
}) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      {children}
    </svg>
  );
}

const ClockIcon = () => (
  <Icon>
    <circle cx="12" cy="12" r="9" />
    <path d="M12 7v5l3 2" />
  </Icon>
);

const UsersIcon = () => (
  <Icon>
    <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" />
    <circle cx="9" cy="7" r="4" />
    <path d="M18 8a3 3 0 0 1 0 6" />
  </Icon>
);

const TargetIcon = () => (
  <Icon>
    <circle cx="12" cy="12" r="9" />
    <circle cx="12" cy="12" r="5" />
    <circle cx="12" cy="12" r="1.5" />
  </Icon>
);

const CameraIcon = () => (
  <Icon>
    <path d="M7 7h2l1.2-2h3.6L15 7h2a3 3 0 0 1 3 3v6a3 3 0 0 1-3 3H7a3 3 0 0 1-3-3v-6a3 3 0 0 1 3-3Z" />
    <circle cx="12" cy="13" r="3" />
  </Icon>
);

const ArrowUpIcon = () => (
  <Icon size={13}>
    <path d="m7 14 5-5 5 5" />
  </Icon>
);

const ArrowDownIcon = () => (
  <Icon size={13}>
    <path d="m7 10 5 5 5-5" />
  </Icon>
);

const ArrowRightIcon = () => (
  <Icon size={15}>
    <path d="M5 12h14" />
    <path d="m14 7 5 5-5 5" />
  </Icon>
);

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (!parts.length) return "?";
  if (parts.length === 1) return parts[0][0]?.toUpperCase() || "?";
  return `${parts[0][0] || ""}${parts[parts.length - 1][0] || ""}`.toUpperCase();
}

function fmtClock(seconds: number): string {
  const value = Math.max(0, Math.floor(seconds));
  const hours = Math.floor(value / 3600);
  const minutes = Math.floor((value % 3600) / 60);
  return `${hours}:${String(minutes).padStart(2, "0")}`;
}

type Status = "active" | "idle" | "offline" | "blocked";

function memberStatus(
  lastSeen: number | null | undefined,
  membershipStatus?: string,
): Status {
  if (membershipStatus === "blocked") return "blocked";
  if (!lastSeen) return "offline";
  const age = Date.now() / 1000 - lastSeen;
  if (age < 5 * 60) return "active";
  if (age < 30 * 60) return "idle";
  return "offline";
}

type Delta = { text: string; positive: boolean } | null;

function percentDelta(today: number, yesterday: number): Delta {
  if (yesterday <= 0) return null;
  const value = Math.round(((today - yesterday) / yesterday) * 100);
  return { text: `${Math.abs(value)}%`, positive: value >= 0 };
}

function countDelta(today: number, yesterday: number): Delta {
  const value = today - yesterday;
  if (value === 0) return null;
  return {
    text: `${value > 0 ? "+" : "−"}${Math.abs(value)}`,
    positive: value > 0,
  };
}

function KpiCard({
  icon,
  label,
  value,
  unit,
  delta,
  note,
}: {
  icon: ReactNode;
  label: string;
  value: ReactNode;
  unit?: string;
  delta?: Delta;
  note?: string;
}) {
  return (
    <Card className="dashboard-kpi">
      <div>
        <div className="dashboard-kpi__top">
          <span className="dashboard-kpi__label">{label}</span>
          <span className="dashboard-kpi__icon">{icon}</span>
        </div>
        <div className="dashboard-kpi__value ds-num">
          {value}
          {unit && <span className="dashboard-kpi__unit">{unit}</span>}
        </div>
      </div>
      <div className="dashboard-kpi__foot">
        {delta && (
          <span
            className={`dashboard-delta dashboard-delta--${delta.positive ? "positive" : "negative"}`}
          >
            {delta.positive ? <ArrowUpIcon /> : <ArrowDownIcon />}
            {delta.text}
          </span>
        )}
        {note && <span>{note}</span>}
      </div>
    </Card>
  );
}

function DashboardSkeleton() {
  return (
    <div className="dashboard-loading" aria-hidden>
      <div className="dashboard-loading__kpis">
        {Array.from({ length: 4 }, (_, index) => (
          <Card className="dashboard-kpi" key={index}>
            <Skeleton width="56%" height={16} />
            <Skeleton width="42%" height={34} />
            <Skeleton width="68%" height={13} />
          </Card>
        ))}
      </div>
      <div className="dashboard-grid">
        <Card className="dashboard-panel dashboard-panel--activity">
          <Skeleton width={180} height={20} />
          <div className="dashboard-skeleton-list">
            {Array.from({ length: 5 }, (_, index) => (
              <Skeleton key={index} width="100%" height={20} />
            ))}
          </div>
        </Card>
        <Card className="dashboard-panel dashboard-panel--status">
          <Skeleton width={140} height={20} />
          <div className="dashboard-skeleton-list dashboard-skeleton-list--compact">
            {Array.from({ length: 3 }, (_, index) => (
              <Skeleton key={index} width="100%" height={42} />
            ))}
          </div>
        </Card>
      </div>
    </div>
  );
}

export function Dashboard() {
  const { t } = useTranslation("dashboard");
  const navigate = useNavigate();
  const { user } = useAuth();
  const { businesses, selected, selectedId, loading: businessLoading } = useBusinesses();
  const terms = memberTerms(selected?.kind);

  const [rows, setRows] = useState<ReportEmployee[]>([]);
  const [liveEmployees, setLiveEmployees] = useState<Employee[]>([]);
  const [deviceHealth, setDeviceHealth] = useState<DeviceHealthSummary | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!selectedId) {
      setRows([]);
      setLiveEmployees([]);
      setDeviceHealth(null);
      return;
    }

    let cancelled = false;
    setLoading(true);
    setError(null);

    const healthRequest =
      selected?.role === "owner" || selected?.role === "admin"
        ? getDeviceHealth(selectedId).catch(() => null)
        : Promise.resolve(null);

    Promise.all([
      reportEmployees(selectedId),
      listBusinessEmployees(selectedId),
      healthRequest,
    ])
      .then(([report, live, health]) => {
        if (cancelled) return;
        setRows(report.employees);
        setLiveEmployees(live.employees);
        setDeviceHealth(health);
      })
      .catch(() => {
        if (!cancelled) setError(t("dashboard.errorRoster"));
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
    };
  }, [selected?.role, selectedId, t]);

  const metrics = useMemo(() => {
    const totalToday = rows.reduce((sum, employee) => sum + (employee.active_today_s || 0), 0);
    const totalYesterday = rows.reduce(
      (sum, employee) => sum + (employee.active_yesterday_s || 0),
      0,
    );
    const screenshotsToday = rows.reduce(
      (sum, employee) => sum + (employee.screenshots_today || 0),
      0,
    );
    const screenshotsYesterday = rows.reduce(
      (sum, employee) => sum + (employee.screenshots_yesterday || 0),
      0,
    );

    const focusValues = rows
      .map((employee) => employee.focus_pct_today)
      .filter((value): value is number => value !== null);
    const averageFocus = focusValues.length
      ? Math.round(focusValues.reduce((sum, value) => sum + value, 0) / focusValues.length)
      : null;

    const statuses = rows.reduce(
      (acc, employee) => {
        acc[memberStatus(employee.last_seen, employee.status)] += 1;
        return acc;
      },
      { active: 0, idle: 0, offline: 0, blocked: 0 } as Record<Status, number>,
    );

    return {
      totalToday,
      totalYesterday,
      screenshotsToday,
      screenshotsYesterday,
      averageFocus,
      statuses,
    };
  }, [rows]);

  const activityRanking = useMemo(
    () =>
      [...rows]
        .sort((a, b) => b.active_today_s - a.active_today_s)
        .slice(0, 7),
    [rows],
  );

  const maxActivity = Math.max(
    1,
    ...activityRanking.map((employee) => employee.active_today_s),
  );

  const currentApps = useMemo(() => {
    const counts = new Map<string, number>();
    for (const employee of liveEmployees) {
      const status = memberStatus(employee.last_seen, employee.status);
      const app = employee.current_app?.trim();
      if (!app || status === "offline" || status === "blocked") continue;
      counts.set(app, (counts.get(app) || 0) + 1);
    }
    return [...counts.entries()]
      .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
      .slice(0, 6);
  }, [liveEmployees]);

  const recentMembers = useMemo(
    () =>
      [...rows]
        .filter((employee) => employee.last_seen)
        .sort((a, b) => (b.last_seen || 0) - (a.last_seen || 0))
        .slice(0, 6),
    [rows],
  );

  return (
    <div className="dashboard-v1">
      {(businessLoading || loading) && <DashboardSkeleton />}

      {!businessLoading && businesses.length === 0 && (
        <EmptyState
          title={t("dashboard.v1.noOrganizationTitle")}
          description={
            <Trans
              i18nKey="dashboard.noBusinesses"
              t={t}
              values={{ members: terms.many, member: terms.lowerOne }}
              components={[<Link to="/employees" />]}
            />
          }
        />
      )}

      {error && <Alert tone="danger">{error}</Alert>}

      {!loading && deviceHealth && deviceHealth.outdated > 0 && (
        <div className="dashboard-device-health">
          <Alert tone="warning">
            {t("dashboard.v1.deviceHealthWarning", {
              count: deviceHealth.outdated,
              version: deviceHealth.recommended_version || t("dashboard.v1.recommendedRelease"),
            })}
          </Alert>
        </div>
      )}

      {!loading && !error && selectedId && rows.length === 0 && (
        <EmptyState
          title={t("dashboard.v1.noActivityTitle")}
          description={t("dashboard.noActivity", { members: terms.lowerMany })}
        />
      )}

      {!loading && rows.length > 0 && (
        <>
          <div className="dashboard-kpis">
            <KpiCard
              icon={<UsersIcon />}
              label={t("dashboard.v1.activeNow")}
              value={metrics.statuses.active}
              note={t("dashboard.v1.ofTotal", { count: rows.length })}
            />
            <KpiCard
              icon={<ClockIcon />}
              label={t("dashboard.v1.recordedToday")}
              value={fmtClock(metrics.totalToday)}
              delta={percentDelta(metrics.totalToday, metrics.totalYesterday)}
              note={
                metrics.totalYesterday > 0
                  ? t("dashboard.vsYesterday")
                  : t("dashboard.todayLabel")
              }
            />
            <KpiCard
              icon={<TargetIcon />}
              label={t("dashboard.v1.averageFocus")}
              value={metrics.averageFocus ?? "—"}
              unit={metrics.averageFocus === null ? undefined : "%"}
              note={t("dashboard.todayLabel")}
            />
            <KpiCard
              icon={<CameraIcon />}
              label={t("dashboard.v1.screenshotsToday")}
              value={metrics.screenshotsToday}
              delta={countDelta(
                metrics.screenshotsToday,
                metrics.screenshotsYesterday,
              )}
              note={t("dashboard.todayLabel")}
            />
          </div>

          <div className="dashboard-grid">
            <Card className="dashboard-panel dashboard-panel--activity">
              <div className="dashboard-panel__head">
                <div>
                  <h2 className="dashboard-panel__title">
                    {t("dashboard.v1.activityTitle")}
                  </h2>
                  <p className="dashboard-panel__subtitle">
                    {t("dashboard.v1.activitySubtitle")}
                  </p>
                </div>
              </div>

              <div className="dashboard-activity-list">
                {activityRanking.map((employee) => (
                  <div className="dashboard-activity-row" key={employee.id}>
                    <span className="dashboard-activity-name">
                      {employee.display_name}
                    </span>
                    <span className="dashboard-activity-track">
                      <span
                        className="dashboard-activity-fill"
                        style={{
                          width: `${Math.max(
                            2,
                            (employee.active_today_s / maxActivity) * 100,
                          )}%`,
                        }}
                      />
                    </span>
                    <span className="dashboard-activity-value ds-num">
                      {fmtClock(employee.active_today_s)}
                    </span>
                  </div>
                ))}
              </div>
            </Card>

            <Card className="dashboard-panel dashboard-panel--status">
              <div className="dashboard-panel__head">
                <div>
                  <h2 className="dashboard-panel__title">
                    {t("dashboard.v1.statusTitle")}
                  </h2>
                  <p className="dashboard-panel__subtitle">
                    {t("dashboard.v1.statusSubtitle")}
                  </p>
                </div>
              </div>
              <div className="dashboard-status-list">
                {(["active", "idle", "offline", "blocked"] as Status[]).map((status) => (
                  <div className="dashboard-status-row" key={status}>
                    <span
                      className={`dashboard-status-dot dashboard-status-dot--${status}`}
                    />
                    <span className="dashboard-status-label">
                      {t(`dashboard.v1.status.${status}`)}
                    </span>
                    <span className="dashboard-status-count">
                      {metrics.statuses[status]}
                    </span>
                  </div>
                ))}
              </div>
            </Card>

            <Card className="dashboard-panel dashboard-panel--apps">
              <div className="dashboard-panel__head">
                <div>
                  <h2 className="dashboard-panel__title">
                    {t("dashboard.v1.appsTitle")}
                  </h2>
                  <p className="dashboard-panel__subtitle">
                    {t("dashboard.v1.appsSubtitle")}
                  </p>
                </div>
              </div>
              {currentApps.length > 0 ? (
                <div className="dashboard-app-list">
                  {currentApps.map(([app, count]) => (
                    <div className="dashboard-app-row" key={app}>
                      <span className="dashboard-app-mark">
                        {app.slice(0, 1).toUpperCase()}
                      </span>
                      <span className="dashboard-app-name">{app}</span>
                      <span className="dashboard-app-count">
                        {t("dashboard.v1.peopleCount", { count })}
                      </span>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="dashboard-empty-inline">
                  {t("dashboard.v1.appsEmpty")}
                </div>
              )}
            </Card>

            <Card className="dashboard-panel dashboard-panel--recent">
              <div className="dashboard-panel__head">
                <div>
                  <h2 className="dashboard-panel__title">
                    {t("dashboard.v1.recentTitle")}
                  </h2>
                  <p className="dashboard-panel__subtitle">
                    {t("dashboard.v1.recentSubtitle")}
                  </p>
                </div>
              </div>
              <div className="dashboard-recent-list">
                {recentMembers.map((employee) => (
                  <button
                    type="button"
                    className="dashboard-recent-row dashboard-recent-button"
                    key={employee.id}
                    onClick={() =>
                      navigate(`/employees/${employee.id}?business=${selectedId}`)
                    }
                  >
                    <span className="dashboard-recent-avatar">
                      {initials(employee.display_name)}
                    </span>
                    <span className="dashboard-recent-name">
                      {employee.display_name}
                    </span>
                    <span className="dashboard-recent-time">
                      {fmtRelative(employee.last_seen)}
                    </span>
                  </button>
                ))}
              </div>
            </Card>
          </div>

          <div className="ds-table-wrap">
            <table className="ds-table">
              <thead>
                <tr>
                  <th>{t("dashboard.table.name")}</th>
                  <th>{t("dashboard.table.login")}</th>
                  <th>{t("dashboard.table.lastSeen")}</th>
                  <th>{t("dashboard.table.activeToday")}</th>
                  <th>{t("dashboard.table.focus")}</th>
                  <th>{t("dashboard.v1.screenshots")}</th>
                  <th aria-label={t("dashboard.view")} />
                </tr>
              </thead>
              <tbody>
                {rows.map((employee) => {
                  const isSelf = employee.role === "owner" || employee.id === user?.id;
                  const focus = employee.focus_pct_today;
                  const focusClass =
                    focus === null
                      ? ""
                      : focus >= 75
                        ? "dashboard-focus--good"
                        : focus >= 60
                          ? "dashboard-focus--warn"
                          : "dashboard-focus--low";

                  return (
                    <tr
                      key={employee.id}
                      className="dashboard-team-row"
                      onClick={() =>
                        navigate(
                          `/employees/${employee.id}?business=${selectedId}`,
                        )
                      }
                    >
                      <td>
                        <div className="dashboard-table-person">
                          <span className="dashboard-table-person__avatar">
                            {initials(employee.display_name)}
                          </span>
                          <span className="dashboard-table-person__name">
                            {employee.display_name}
                            {isSelf && (
                              <span className="dashboard-self-badge">
                                {t("dashboard.selfBadge")}
                              </span>
                            )}
                          </span>
                        </div>
                      </td>
                      <td>{employee.email || employee.username || "—"}</td>
                      <td>{fmtRelative(employee.last_seen)}</td>
                      <td className="ds-num">{fmtClock(employee.active_today_s)}</td>
                      <td className={`dashboard-focus ${focusClass}`}>
                        {focus === null ? "—" : `${focus}%`}
                      </td>
                      <td className="ds-num">{employee.screenshots_today}</td>
                      <td>
                        <span className="dashboard-row-arrow">
                          <ArrowRightIcon />
                        </span>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </>
      )}
    </div>
  );
}
