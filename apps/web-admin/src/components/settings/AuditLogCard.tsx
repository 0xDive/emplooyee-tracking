import { useCallback, useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  listAuditEvents,
  listBusinessEmployees,
  listFormerMembers,
} from "../../api/endpoints";
import type { AuditEvent, Employee } from "../../api/types";
import {
  Alert,
  Button,
  Card,
  Dialog,
  EmptyState,
  SelectMenu,
  Skeleton,
} from "../ds";

const PREVIEW_COUNT = 6;
const PAGE_SIZE = 8;

const ACTION_KEYS: Record<string, string> = {
  "employee.created": "employeeCreated",
  "employee.updated": "employeeUpdated",
  "employee.password_reset": "employeePasswordReset",
  "employee.archived": "employeeArchived",
  "employee.restored": "employeeRestored",
  "device.updated": "deviceUpdated",
  "device.revoked": "deviceRevoked",
  "device.restored": "deviceRestored",
  "member.role_changed": "memberRoleChanged",
  "member.monitoring_changed": "memberMonitoringChanged",
  "member.purged": "memberPurged",
  "member.enrollment_created": "memberEnrollmentCreated",
  "member.enrollment_redeemed": "memberEnrollmentRedeemed",
  "member.blocked": "memberBlocked",
  "member.unblocked": "memberUnblocked",
  "member.removed": "memberRemoved",
  "organization.created": "organizationCreated",
  "organization.renamed": "organizationRenamed",
  "organization.kind_changed": "organizationKindChanged",
  "organization.timezone_changed": "organizationTimezoneChanged",
  "organization.week_start_changed": "organizationWeekStartChanged",
  "organization.archived": "organizationArchived",
  "organization.restored": "organizationRestored",
  "organization.owner_transferred": "organizationOwnerTransferred",
  "organization.deletion_scheduled": "organizationDeletionScheduled",
  "organization.deletion_cancelled": "organizationDeletionCancelled",
  "settings.privacy_rule_created": "privacyRuleCreated",
  "settings.privacy_rule_changed": "privacyRuleChanged",
  "settings.privacy_rule_deleted": "privacyRuleDeleted",
};

function formatTime(timestamp: number): string {
  return new Date(timestamp * 1000).toLocaleString();
}

function shortId(id: string): string {
  if (!id) return "";
  return id.length > 12 ? `${id.slice(0, 8)}…${id.slice(-4)}` : id;
}

function detailsText(event: AuditEvent): string | null {
  const details = event.details ?? {};
  if (typeof details.display_name === "string" && details.display_name) {
    return details.display_name;
  }
  if (typeof details.label === "string" && details.label) {
    return details.label;
  }
  if (typeof details.from === "string" && typeof details.to === "string") {
    return `${details.from} → ${details.to}`;
  }
  if (Array.isArray(details.fields) && details.fields.length > 0) {
    return details.fields
      .filter((value): value is string => typeof value === "string")
      .join(", ");
  }
  return null;
}

export function AuditLogCard({ businessId }: { businessId: string }) {
  const { t } = useTranslation("settings");
  const [events, setEvents] = useState<AuditEvent[]>([]);
  const [people, setPeople] = useState<Employee[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [userFilter, setUserFilter] = useState("");
  const [actionFilter, setActionFilter] = useState("");
  const [page, setPage] = useState(0);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [auditResponse, activeResponse, formerResponse] = await Promise.all([
        listAuditEvents(businessId, 200),
        listBusinessEmployees(businessId),
        listFormerMembers(businessId),
      ]);
      setEvents(auditResponse.events);
      const byId = new Map<string, Employee>();
      for (const person of [...activeResponse.employees, ...formerResponse.employees]) {
        byId.set(person.id, person);
      }
      setPeople([...byId.values()]);
    } catch {
      setError(t("audit.loadFailed"));
    } finally {
      setLoading(false);
    }
  }, [businessId, t]);

  useEffect(() => {
    void load();
  }, [load]);

  const peopleById = useMemo(
    () => new Map(people.map((person) => [person.id, person])),
    [people],
  );

  const rows = useMemo(
    () =>
      events.map((event) => {
        const title = t(`audit.actions.${ACTION_KEYS[event.action] ?? "unknown"}`);
        const details = detailsText(event);
        const actor = peopleById.get(event.actor_user_id);
        const target =
          event.target_type === "member" || event.target_type === "employee"
            ? peopleById.get(event.target_id)
            : undefined;
        return {
          event,
          title,
          details,
          actorName:
            actor?.display_name ||
            t("audit.unknownUser", { id: shortId(event.actor_user_id) }),
          targetName: target?.display_name || "",
        };
      }),
    [events, peopleById, t],
  );

  const userOptions = useMemo(() => {
    const relevantIds = new Set<string>();
    for (const event of events) {
      if (event.actor_user_id) relevantIds.add(event.actor_user_id);
      if (
        event.target_id &&
        (event.target_type === "member" || event.target_type === "employee")
      ) {
        relevantIds.add(event.target_id);
      }
    }

    return [
      { value: "", label: t("audit.allUsers") },
      ...[...relevantIds]
        .map((id) => ({
          value: id,
          label:
            peopleById.get(id)?.display_name ||
            t("audit.unknownUser", { id: shortId(id) }),
        }))
        .sort((left, right) => left.label.localeCompare(right.label)),
    ];
  }, [events, peopleById, t]);

  const actionOptions = useMemo(
    () => [
      { value: "", label: t("audit.allActions") },
      ...[...new Set(events.map((event) => event.action))]
        .map((action) => ({
          value: action,
          label: t(`audit.actions.${ACTION_KEYS[action] ?? "unknown"}`),
        }))
        .sort((left, right) => left.label.localeCompare(right.label)),
    ],
    [events, t],
  );

  const filteredRows = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    return rows.filter(({ event, title, details, actorName, targetName }) => {
      if (
        userFilter &&
        event.actor_user_id !== userFilter &&
        event.target_id !== userFilter
      ) {
        return false;
      }
      if (actionFilter && event.action !== actionFilter) return false;
      if (!query) return true;

      return [
        title,
        details || "",
        actorName,
        targetName,
        event.action,
        event.target_type,
        event.target_id,
      ]
        .join(" ")
        .toLocaleLowerCase()
        .includes(query);
    });
  }, [actionFilter, rows, search, userFilter]);

  useEffect(() => {
    setPage(0);
  }, [actionFilter, search, userFilter]);

  const pageCount = Math.max(1, Math.ceil(filteredRows.length / PAGE_SIZE));
  const safePage = Math.min(page, pageCount - 1);
  const pageRows = filteredRows.slice(
    safePage * PAGE_SIZE,
    safePage * PAGE_SIZE + PAGE_SIZE,
  );

  function renderRow({
    event,
    title,
    details,
    actorName,
    targetName,
  }: (typeof rows)[number]) {
    const targetType = t(`audit.targets.${event.target_type}`, {
      defaultValue: event.target_type,
    });
    const targetText =
      targetName ||
      (event.target_id ? `${targetType} ${shortId(event.target_id)}` : targetType);

    return (
      <div className="settings-audit-row" key={event.id}>
        <div>
          <div className="settings-audit-row__title">{title}</div>
          <div className="settings-audit-row__meta">
            {t("audit.byUser", { name: actorName })}
            {details ? ` · ${details}` : ""}
            {targetText ? ` · ${targetText}` : ""}
          </div>
        </div>
        <time>{formatTime(event.created_at)}</time>
      </div>
    );
  }

  return (
    <>
      <Card>
        <div className="settings-audit-head">
          <div>
            <h2 className="report-card__title">{t("audit.title")}</h2>
            <p className="report-card__subtitle">{t("audit.desc")}</p>
          </div>
          <Button
            variant="secondary"
            size="sm"
            disabled={loading}
            onClick={() => setOpen(true)}
          >
            {t("audit.open")}
          </Button>
        </div>

        {loading && (
          <div className="settings-audit-list" aria-hidden>
            {Array.from({ length: 4 }, (_, index) => (
              <div className="settings-audit-row" key={index}>
                <div>
                  <Skeleton width="52%" height={14} />
                  <div className="settings-audit-skeleton-gap">
                    <Skeleton width="72%" height={12} />
                  </div>
                </div>
                <Skeleton width={112} height={12} />
              </div>
            ))}
          </div>
        )}

        {error && <Alert tone="danger">{error}</Alert>}

        {!loading && !error && rows.length === 0 && (
          <EmptyState
            title={t("audit.empty")}
            description={t("audit.v1.emptyDescription")}
          />
        )}

        {!loading && !error && rows.length > 0 && (
          <>
            <div className="settings-audit-list settings-audit-list--preview">
              {rows.slice(0, PREVIEW_COUNT).map(renderRow)}
            </div>
            {rows.length > PREVIEW_COUNT && (
              <div className="settings-audit-preview-footer">
                <Button variant="ghost" size="sm" onClick={() => setOpen(true)}>
                  {t("audit.showAll", { count: rows.length })}
                </Button>
              </div>
            )}
          </>
        )}
      </Card>

      {open && (
        <Dialog
          title={t("audit.dialogTitle")}
          size="workspace"
          onClose={() => setOpen(false)}
          footer={
            <Button variant="primary" onClick={() => setOpen(false)}>
              {t("audit.close")}
            </Button>
          }
        >
          <div className="settings-audit-modal">
            <div className="settings-audit-filters">
              <input
                type="search"
                className="ds-input settings-audit-search"
                value={search}
                placeholder={t("audit.searchPlaceholder")}
                aria-label={t("audit.search")}
                onChange={(event) => setSearch(event.currentTarget.value)}
              />
              <SelectMenu
                id="audit-user-filter"
                value={userFilter}
                ariaLabel={t("audit.userFilter")}
                options={userOptions}
                menuWidth={280}
                onChange={setUserFilter}
              />
              <SelectMenu
                id="audit-action-filter"
                value={actionFilter}
                ariaLabel={t("audit.actionFilter")}
                options={actionOptions}
                menuWidth={320}
                onChange={setActionFilter}
              />
              <Button variant="secondary" disabled={loading} onClick={() => void load()}>
                {t("audit.refresh")}
              </Button>
            </div>

            <div className="settings-audit-results-meta">
              {t("audit.results", { count: filteredRows.length })}
            </div>

            {filteredRows.length === 0 ? (
              <EmptyState
                title={t("audit.noMatches")}
                description={t("audit.noMatchesDescription")}
              />
            ) : (
              <div className="settings-audit-list settings-audit-list--modal">
                {pageRows.map(renderRow)}
              </div>
            )}

            {filteredRows.length > 0 && (
              <div className="settings-audit-pager">
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={safePage === 0}
                  onClick={() => setPage((current) => Math.max(0, current - 1))}
                >
                  {t("audit.previous")}
                </Button>
                <span>
                  {t("audit.page", {
                    current: safePage + 1,
                    total: pageCount,
                  })}
                </span>
                <Button
                  variant="secondary"
                  size="sm"
                  disabled={safePage >= pageCount - 1}
                  onClick={() =>
                    setPage((current) => Math.min(pageCount - 1, current + 1))
                  }
                >
                  {t("audit.next")}
                </Button>
              </div>
            )}
          </div>
        </Dialog>
      )}
    </>
  );
}
