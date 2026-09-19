import { useEffect, useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
  cleanupData,
  previewCleanupRange,
  previewRetention,
  updateBusinessSettings,
  type RetentionDataClass,
  type RetentionPreview,
} from "../api/endpoints";
import {
  ApiError,
  type BusinessSettingsPatch,
} from "../api/types";
import {
  Alert,
  Button,
  Card,
  Dialog,
  EmptyState,
  Skeleton,
} from "../components/ds";
import { useToast } from "../components/ToastProvider";
import { AuditLogCard } from "../components/settings/AuditLogCard";
import { OrganizationSettingsCard } from "../components/settings/OrganizationSettingsCard";
import { OrganizationLifecycleCard } from "../components/settings/OrganizationLifecycleCard";
import { MonitoringSettingsCard } from "../components/settings/MonitoringSettingsCard";
import { ScreenshotPolicyCard } from "../components/settings/ScreenshotPolicyCard";
import { DeviceEnrollmentSettingsCard } from "../components/settings/DeviceEnrollmentSettingsCard";
import { ExportSettingsCard } from "../components/settings/ExportSettingsCard";
import { PrivacyRulesDialog } from "../components/settings/PrivacyRulesDialog";
import { useBusinesses } from "../useBusinesses";
import { canManageSettings } from "../rbac";
import { dayRangeToUnix, isoDateInTimeZone } from "../format";
import "../theme/settings-v1.css";

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}

const CLEANUP_PRESETS = [7, 14, 30, 90];

const CLEANUP_DATA_CLASSES: RetentionDataClass[] = [
  "activity",
  "screenshots",
  "browser",
  "keystrokes",
];

type RetentionField =
  | "activity_retention_days"
  | "screenshot_retention_days"
  | "browser_retention_days"
  | "keystroke_retention_days";

type PendingRetentionChange = {
  field: RetentionField;
  dataClass: RetentionDataClass;
  value: number;
  preview: RetentionPreview;
};

const RETENTION_PRESETS: Record<RetentionField, Array<number | null>> = {
  activity_retention_days: [30, 90, 180, 365],
  screenshot_retention_days: [7, 14, 30, 90, null],
  browser_retention_days: [30, 90, 180, 365],
  keystroke_retention_days: [30, 90, 180, 365],
};

function SettingsSection({
  id,
  title,
  description,
  children,
}: {
  id: string;
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
}) {
  return (
    <section className="settings-section" id={id}>
      <div className="settings-section__head">
        <h2 className="settings-section__title">{title}</h2>
        {description && (
          <p className="settings-section__description">{description}</p>
        )}
      </div>
      {children}
    </section>
  );
}

function SettingsRow({
  title,
  description,
  children,
  top = false,
}: {
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  top?: boolean;
}) {
  return (
    <div className={`settings-row${top ? " settings-row--top" : ""}`}>
      <div className="settings-row__copy">
        <div className="settings-row__title">{title}</div>
        {description && (
          <div className="settings-row__description">{description}</div>
        )}
      </div>
      <div className="settings-row__control">{children}</div>
    </div>
  );
}

function Segmented<T extends string | number>({
  value,
  options,
  disabled,
  onChange,
  ariaLabel,
}: {
  value: T;
  options: Array<{ value: T; label: string }>;
  disabled?: boolean;
  onChange: (value: T) => void;
  ariaLabel: string;
}) {
  return (
    <div className="settings-segmented" role="group" aria-label={ariaLabel}>
      {options.map((option) => (
        <button
          key={String(option.value)}
          type="button"
          className={`settings-segmented__option${
            option.value === value ? " is-active" : ""
          }`}
          disabled={disabled}
          aria-pressed={option.value === value}
          onClick={() => onChange(option.value)}
        >
          {option.label}
        </button>
      ))}
    </div>
  );
}

export function Settings() {
  const { t } = useTranslation("settings");
  const { pushToast } = useToast();
  const { businesses, selected, selectedId, loading, reload } = useBusinesses();
  const mayManageSettings = canManageSettings(selected?.role);
  const organizationReadOnly = Boolean(
    selected?.archived_at || selected?.deletion_scheduled_at,
  );

  const [activeSection, setActiveSection] = useState("organization");
  const [saving, setSaving] = useState(false);
  const [pendingRetention, setPendingRetention] =
    useState<PendingRetentionChange | null>(null);
  const [privacyOpen, setPrivacyOpen] = useState(false);
  const [cleanupOpen, setCleanupOpen] = useState(false);
  const [cleanupMode, setCleanupMode] = useState<"older" | "range">("older");
  const [cleanupDays, setCleanupDays] = useState(30);
  const [cleanupFrom, setCleanupFrom] = useState("");
  const [cleanupTo, setCleanupTo] = useState("");
  const [cleanupClasses, setCleanupClasses] =
    useState<RetentionDataClass[]>(["screenshots"]);
  const [cleanupPreview, setCleanupPreview] = useState<RetentionPreview[]>([]);
  const [cleanupPreviewing, setCleanupPreviewing] = useState(false);
  const [cleaning, setCleaning] = useState(false);
  const [dialogError, setDialogError] = useState<string | null>(null);

  async function savePatch(
    patch: BusinessSettingsPatch,
    successText: string,
  ) {
    if (!selectedId) return;

    setSaving(true);
    try {
      await updateBusinessSettings(selectedId, patch);
      await reload();
      pushToast({ title: successText, tone: "success" });
    } catch (error) {
      pushToast({
        title: error instanceof ApiError ? error.message : t("saveError"),
        tone: "danger",
      });
    } finally {
      setSaving(false);
    }
  }

  async function changeRetention(
    field: RetentionField,
    dataClass: RetentionDataClass,
    currentValue: number | null,
    nextValue: number | null,
  ) {
    if (!selectedId || currentValue === nextValue) return;

    const reduction =
      nextValue !== null &&
      (currentValue === null || nextValue < currentValue);

    if (!reduction) {
      await savePatch(
        { [field]: nextValue } as BusinessSettingsPatch,
        t("retention.saved"),
      );
      return;
    }

    setSaving(true);
    try {
      const preview = await previewRetention(selectedId, dataClass, nextValue);
      setPendingRetention({
        field,
        dataClass,
        value: nextValue,
        preview,
      });
    } catch (error) {
      pushToast({
        title: error instanceof ApiError ? error.message : t("retention.previewFailed"),
        tone: "danger",
      });
    } finally {
      setSaving(false);
    }
  }

  async function confirmRetentionReduction() {
    if (!selectedId || !pendingRetention) return;

    setSaving(true);
    try {
      await updateBusinessSettings(
        selectedId,
        { [pendingRetention.field]: pendingRetention.value } as BusinessSettingsPatch,
        true,
      );
      setPendingRetention(null);
      await reload();
      pushToast({ title: t("retention.saved"), tone: "success" });
    } catch (error) {
      pushToast({
        title: error instanceof ApiError ? error.message : t("saveError"),
        tone: "danger",
      });
    } finally {
      setSaving(false);
    }
  }

  async function refreshCleanupPreview(
    classes = cleanupClasses,
    mode = cleanupMode,
    days = cleanupDays,
    fromDate = cleanupFrom,
    toDate = cleanupTo,
  ) {
    if (!selectedId || classes.length === 0) {
      setCleanupPreview([]);
      return;
    }

    setCleanupPreviewing(true);
    setDialogError(null);
    try {
      let previews: RetentionPreview[];
      if (mode === "range") {
        if (!fromDate || !toDate || fromDate > toDate) {
          setCleanupPreview([]);
          setDialogError(t("cleanup.invalidRange"));
          return;
        }
        const bounds = dayRangeToUnix(
          fromDate,
          toDate,
          selected?.timezone || "UTC",
        );
        previews = await Promise.all(
          classes.map((dataClass) =>
            previewCleanupRange(
              selectedId,
              dataClass,
              bounds.from,
              bounds.to,
            ),
          ),
        );
      } else {
        previews = await Promise.all(
          classes.map((dataClass) =>
            previewRetention(selectedId, dataClass, days),
          ),
        );
      }
      setCleanupPreview(previews);
    } catch (error) {
      setCleanupPreview([]);
      setDialogError(
        error instanceof ApiError ? error.message : t("cleanup.previewFailed"),
      );
    } finally {
      setCleanupPreviewing(false);
    }
  }

  function openCleanupDialog() {
    setDialogError(null);
    setCleanupOpen(true);
    void refreshCleanupPreview();
  }

  function toggleCleanupClass(dataClass: RetentionDataClass, checked: boolean) {
    const next = checked
      ? Array.from(new Set([...cleanupClasses, dataClass]))
      : cleanupClasses.filter((value) => value !== dataClass);
    setCleanupClasses(next);
    void refreshCleanupPreview(
      next,
      cleanupMode,
      cleanupDays,
      cleanupFrom,
      cleanupTo,
    );
  }

  function changeCleanupDays(days: number) {
    setCleanupDays(days);
    void refreshCleanupPreview(
      cleanupClasses,
      "older",
      days,
      cleanupFrom,
      cleanupTo,
    );
  }

  function changeCleanupMode(mode: "older" | "range") {
    setCleanupMode(mode);
    if (mode === "range") {
      const today = isoDateInTimeZone(
        new Date(),
        selected?.timezone || "UTC",
      );
      const from = cleanupFrom || today;
      const to = cleanupTo || today;
      setCleanupFrom(from);
      setCleanupTo(to);
      void refreshCleanupPreview(
        cleanupClasses,
        mode,
        cleanupDays,
        from,
        to,
      );
      return;
    }
    void refreshCleanupPreview(
      cleanupClasses,
      mode,
      cleanupDays,
      cleanupFrom,
      cleanupTo,
    );
  }

  function changeCleanupFrom(value: string) {
    setCleanupFrom(value);
    void refreshCleanupPreview(
      cleanupClasses,
      "range",
      cleanupDays,
      value,
      cleanupTo,
    );
  }

  function changeCleanupTo(value: string) {
    setCleanupTo(value);
    void refreshCleanupPreview(
      cleanupClasses,
      "range",
      cleanupDays,
      cleanupFrom,
      value,
    );
  }

  async function runCleanup() {
    if (!selectedId || cleanupClasses.length === 0) return;

    setCleaning(true);
    setDialogError(null);

    try {
      const window =
        cleanupMode === "range"
          ? (() => {
              if (!cleanupFrom || !cleanupTo || cleanupFrom > cleanupTo) {
                throw new Error(t("cleanup.invalidRange"));
              }
              return dayRangeToUnix(
                cleanupFrom,
                cleanupTo,
                selected?.timezone || "UTC",
              );
            })()
          : null;
      const response = await cleanupData(
        selectedId,
        cleanupClasses,
        window
          ? { from: window.from, to: window.to }
          : { older_than_days: cleanupDays },
      );
      setCleanupOpen(false);
      pushToast({
        title: t("cleanup.removed", {
          count: response.deleted_count,
          size: formatBytes(response.bytes_freed),
        }),
        tone: "success",
      });
    } catch (error) {
      setDialogError(
        error instanceof ApiError || error instanceof Error
          ? error.message
          : t("cleanup.failed"),
      );
    } finally {
      setCleaning(false);
    }
  }

  const nav = organizationReadOnly
    ? ([
        ["organization", t("v1.sections.organization")],
        ["storage", t("v1.sections.storage")],
        ["audit", t("v1.sections.audit")],
      ] as const)
    : ([
        ["organization", t("v1.sections.organization")],
        ["monitoring", t("v1.sections.monitoring")],
        ["screenshots", t("v1.sections.screenshots")],
        ["devices", t("v1.sections.devices")],
        ["storage", t("v1.sections.storage")],
        ["audit", t("v1.sections.audit")],
      ] as const);

  useEffect(() => {
    if (!selectedId) return;

    const root = document.querySelector<HTMLElement>(".ds-shell-content");
    if (!root) return;

    const ids = organizationReadOnly
      ? ["organization", "storage", "audit"]
      : ["organization", "monitoring", "screenshots", "devices", "storage", "audit"];
    const sections = ids
      .map((id) => document.getElementById(id))
      .filter((section): section is HTMLElement => Boolean(section));
    if (sections.length === 0) return;

    let frame = 0;
    const updateActiveSection = () => {
      cancelAnimationFrame(frame);
      frame = requestAnimationFrame(() => {
        const markerY = root.getBoundingClientRect().top + 112;
        let current = sections[0].id;

        for (const section of sections) {
          if (section.getBoundingClientRect().top <= markerY) {
            current = section.id;
          } else {
            break;
          }
        }

        if (root.scrollTop + root.clientHeight >= root.scrollHeight - 12) {
          current = sections[sections.length - 1].id;
        }

        setActiveSection(current);
      });
    };

    updateActiveSection();
    root.addEventListener("scroll", updateActiveSection, { passive: true });
    window.addEventListener("resize", updateActiveSection);

    return () => {
      cancelAnimationFrame(frame);
      root.removeEventListener("scroll", updateActiveSection);
      window.removeEventListener("resize", updateActiveSection);
    };
  }, [organizationReadOnly, selectedId]);

  function goToSection(id: string) {
    setActiveSection(id);
    document.getElementById(id)?.scrollIntoView({
      behavior: "smooth",
      block: "start",
    });
  }

  return (
    <div className="settings-v1">
      {loading && (
        <div className="settings-layout" aria-hidden>
          <Skeleton width={180} height={220} />
          <div className="settings-content">
            {Array.from({ length: 3 }, (_, index) => (
              <Skeleton key={index} width="100%" height={180} />
            ))}
          </div>
        </div>
      )}

      {!loading && businesses.length === 0 && (
        <EmptyState
          title={t("noBusinesses")}
          description={t("v1.noBusinessDescription")}
        />
      )}

      {selected && !mayManageSettings && (
        <Alert tone="info">{t("roleDenied")}</Alert>
      )}

      {selected && mayManageSettings && (
        <div className="settings-layout">
          <nav className="settings-nav" aria-label={t("v1.sectionNavigation")}>
            {nav.map(([id, label]) => (
              <button
                key={id}
                type="button"
                className={`settings-nav__item${
                  activeSection === id ? " is-active" : ""
                }`}
                onClick={() => goToSection(id)}
              >
                {label}
              </button>
            ))}
          </nav>

          <div className="settings-content">
            <SettingsSection
              id="organization"
              title={t("v1.sections.organization")}
              description={t("v1.organization.description")}
            >
              {!organizationReadOnly && (
                <OrganizationSettingsCard
                  access={{ business: selected, role: selected.role }}
                  onReload={reload}
                />
              )}
              <OrganizationLifecycleCard
                access={{ business: selected, role: selected.role }}
                onReload={reload}
              />
            </SettingsSection>

            {!organizationReadOnly && (
              <>
                <SettingsSection
                  id="monitoring"
                  title={t("v1.sections.monitoring")}
                  description={t("v1.monitoring.description")}
                >
                  <MonitoringSettingsCard
                    access={{ business: selected, role: selected.role }}
                    onReload={reload}
                  />
                </SettingsSection>

                <SettingsSection
                  id="screenshots"
                  title={t("v1.sections.screenshots")}
                  description={t("v1.screenshots.description")}
                >
                  <ScreenshotPolicyCard
                    access={{ business: selected, role: selected.role }}
                    onReload={reload}
                    onManagePrivacy={() => setPrivacyOpen(true)}
                  />
                </SettingsSection>

                <SettingsSection
                  id="devices"
                  title={t("v1.sections.devices")}
                  description={t("v1.devices.description")}
                >
                  <DeviceEnrollmentSettingsCard
                    access={{ business: selected, role: selected.role }}
                    onReload={reload}
                  />
                </SettingsSection>
              </>
            )}

            <SettingsSection
              id="storage"
              title={t("v1.sections.storage")}
              description={t("v1.storage.description")}
            >
              {!organizationReadOnly && (
                <Card className="settings-card">
                {([
                  {
                    field: "activity_retention_days",
                    dataClass: "activity",
                    value: selected.activity_retention_days,
                    title: t("retention.activity"),
                    description: t("retention.activityDesc"),
                  },
                  {
                    field: "screenshot_retention_days",
                    dataClass: "screenshots",
                    value: selected.screenshot_retention_days,
                    title: t("retention.screenshots"),
                    description: t("retention.screenshotsDesc"),
                  },
                  {
                    field: "browser_retention_days",
                    dataClass: "browser",
                    value: selected.browser_retention_days,
                    title: t("retention.browser"),
                    description: t("retention.browserDesc"),
                  },
                  {
                    field: "keystroke_retention_days",
                    dataClass: "keystrokes",
                    value: selected.keystroke_retention_days,
                    title: t("retention.keystrokes"),
                    description: t("retention.keystrokesDesc"),
                  },
                ] as const).map((item) => (
                  <SettingsRow
                    key={item.field}
                    title={item.title}
                    description={item.description}
                  >
                    <Segmented
                      value={String(item.value)}
                      ariaLabel={item.title}
                      disabled={saving}
                      options={RETENTION_PRESETS[item.field].map((days) => ({
                        value: String(days),
                        label:
                          days === null
                            ? t("presets.never")
                            : t("presets.days", { count: days }),
                      }))}
                      onChange={(value) =>
                        changeRetention(
                          item.field,
                          item.dataClass,
                          item.value,
                          value === "null" ? null : Number(value),
                        )
                      }
                    />
                  </SettingsRow>
                ))}

                <SettingsRow
                  title={t("cleanup.title")}
                  description={t("cleanup.desc", {
                    name: selected.name,
                  })}
                >
                  <Button
                    variant="danger-ghost"
                    size="sm"
                    disabled={cleaning}
                    onClick={openCleanupDialog}
                  >
                    {t("cleanup.button")}
                  </Button>
                </SettingsRow>
                </Card>
              )}
              {selectedId && <ExportSettingsCard businessId={selectedId} />}
            </SettingsSection>

            {selectedId && (
              <SettingsSection
                id="audit"
                title={t("v1.sections.audit")}
                description={t("v1.audit.description")}
              >
                <AuditLogCard businessId={selectedId} />
              </SettingsSection>
            )}

          </div>
        </div>
      )}

      {selectedId && !organizationReadOnly && (
        <PrivacyRulesDialog
          businessId={selectedId}
          open={privacyOpen}
          onClose={() => setPrivacyOpen(false)}
        />
      )}

      {!organizationReadOnly && pendingRetention && (
        <Dialog
          title={t("retention.confirmTitle")}
          size="confirm"
          onClose={() => !saving && setPendingRetention(null)}
          closeOnBackdrop={!saving}
          footer={
            <>
              <Button
                variant="secondary"
                disabled={saving}
                onClick={() => setPendingRetention(null)}
              >
                {t("retention.cancel")}
              </Button>
              <Button
                variant="danger"
                loading={saving}
                onClick={confirmRetentionReduction}
              >
                {t("retention.confirm")}
              </Button>
            </>
          }
        >
          <div className="settings-dialog-stack">
            <Alert tone="warning">
              {pendingRetention.dataClass === "screenshots"
                ? t("retention.previewScreenshots", {
                    count: pendingRetention.preview.affected_count,
                    size: formatBytes(pendingRetention.preview.bytes_freed),
                    days: pendingRetention.value,
                  })
                : t("retention.previewRows", {
                    count: pendingRetention.preview.affected_count,
                    days: pendingRetention.value,
                  })}
            </Alert>
            <p className="settings-dialog-copy">
              {t("retention.workerNotice")}
            </p>
          </div>
        </Dialog>
      )}

      {!organizationReadOnly && cleanupOpen && selected && (
        <Dialog
          title={t("cleanup.modalTitle")}
          size="confirm"
          onClose={() => !cleaning && setCleanupOpen(false)}
          closeOnBackdrop={!cleaning}
          footer={
            <>
              <Button
                variant="secondary"
                disabled={cleaning}
                onClick={() => setCleanupOpen(false)}
              >
                {t("cleanup.cancel")}
              </Button>
              <Button
                variant="danger"
                loading={cleaning}
                disabled={
                  cleanupPreviewing ||
                  cleanupClasses.length === 0 ||
                  cleanupPreview.length !== cleanupClasses.length ||
                  (cleanupMode === "range" &&
                    (!cleanupFrom || !cleanupTo || cleanupFrom > cleanupTo))
                }
                onClick={runCleanup}
              >
                {cleanupMode === "range"
                  ? t("cleanup.deleteRange")
                  : t("cleanup.delete", { days: cleanupDays })}
              </Button>
            </>
          }
        >
          <div className="settings-dialog-stack">
            <Alert tone="warning">
              {cleanupMode === "range"
                ? t("cleanup.warningRange", {
                    from: cleanupFrom,
                    to: cleanupTo,
                  })
                : t("cleanup.warning", { days: cleanupDays })}
            </Alert>
            <div className="settings-dialog-stack">
              <div className="settings-row__title">{t("cleanup.dataClasses")}</div>
              {CLEANUP_DATA_CLASSES.map((dataClass) => (
                <label key={dataClass} className="settings-cleanup-check">
                  <input
                    type="checkbox"
                    checked={cleanupClasses.includes(dataClass)}
                    disabled={cleaning}
                    onChange={(event) =>
                      toggleCleanupClass(dataClass, event.currentTarget.checked)
                    }
                  />
                  <span>{t(`cleanup.classes.${dataClass}`)}</span>
                </label>
              ))}
            </div>
            <Segmented
              value={cleanupMode}
              ariaLabel={t("cleanup.modeAriaLabel")}
              disabled={cleaning}
              options={[
                { value: "older" as const, label: t("cleanup.modeOlder") },
                { value: "range" as const, label: t("cleanup.modeRange") },
              ]}
              onChange={changeCleanupMode}
            />
            {cleanupMode === "older" ? (
              <Segmented
                value={cleanupDays}
                ariaLabel={t("cleanup.olderThanAriaLabel")}
                disabled={cleaning}
                options={CLEANUP_PRESETS.map((days) => ({
                  value: days,
                  label: t("presets.days", { count: days }),
                }))}
                onChange={changeCleanupDays}
              />
            ) : (
              <div className="settings-cleanup-range">
                <label>
                  <span>{t("cleanup.from")}</span>
                  <input
                    className="ds-input"
                    type="date"
                    value={cleanupFrom}
                    max={cleanupTo || undefined}
                    disabled={cleaning}
                    onChange={(event) =>
                      changeCleanupFrom(event.currentTarget.value)
                    }
                  />
                </label>
                <label>
                  <span>{t("cleanup.to")}</span>
                  <input
                    className="ds-input"
                    type="date"
                    value={cleanupTo}
                    min={cleanupFrom || undefined}
                    disabled={cleaning}
                    onChange={(event) =>
                      changeCleanupTo(event.currentTarget.value)
                    }
                  />
                </label>
                <div className="settings-row__description">
                  {t("cleanup.rangeTimezone", {
                    timezone: selected.timezone || "UTC",
                  })}
                </div>
              </div>
            )}
            <div className="settings-cleanup-preview">
              <div className="settings-row__title">{t("cleanup.previewTitle")}</div>
              {cleanupPreviewing ? (
                <div className="settings-row__description">
                  {t("cleanup.previewing")}
                </div>
              ) : cleanupClasses.length === 0 ? (
                <div className="settings-row__description">
                  {t("cleanup.selectClass")}
                </div>
              ) : (
                cleanupPreview.map((item) => (
                  <div key={item.data_class} className="settings-row__description">
                    {t("cleanup.previewLine", {
                      dataClass: t(`cleanup.classes.${item.data_class}`),
                      count: item.affected_count,
                      size:
                        item.data_class === "screenshots"
                          ? formatBytes(item.bytes_freed)
                          : "",
                    })}
                  </div>
                ))
              )}
            </div>
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </div>
        </Dialog>
      )}
    </div>
  );
}
