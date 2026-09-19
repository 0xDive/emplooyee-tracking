import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import {
  createOrganizationExport,
  downloadOrganizationExport,
  listOrganizationExports,
} from "../../api/endpoints";
import type {
  OrganizationExport,
  OrganizationExportKind,
} from "../../api/types";
import { Badge, Button, Card, SelectMenu, Skeleton } from "../ds";
import { useToast } from "../ToastProvider";

const EXPORT_KINDS: OrganizationExportKind[] = [
  "activity_csv",
  "activity_json",
  "browser_csv",
  "browser_json",
  "keystrokes_csv",
  "keystrokes_json",
  "audit_csv",
  "audit_json",
  "screenshots_archive",
  "full",
];

function statusTone(status: OrganizationExport["status"]) {
  switch (status) {
    case "ready":
      return "success" as const;
    case "failed":
    case "expired":
      return "danger" as const;
    case "running":
      return "info" as const;
    default:
      return "warning" as const;
  }
}

function exportExtension(kind: OrganizationExportKind) {
  if (kind.endsWith("_csv")) return "csv";
  if (kind === "screenshots_archive" || kind === "full") return "zip";
  return "json";
}

export function ExportSettingsCard({ businessId }: { businessId: string }) {
  const { t } = useTranslation("settings");
  const { pushToast } = useToast();
  const [kind, setKind] = useState<OrganizationExportKind>("full");
  const [jobs, setJobs] = useState<OrganizationExport[]>([]);
  const [loading, setLoading] = useState(true);
  const [requesting, setRequesting] = useState(false);
  const [downloading, setDownloading] = useState<string | null>(null);

  const hasWork = useMemo(
    () => jobs.some((job) => job.status === "pending" || job.status === "running"),
    [jobs],
  );

  async function reload(silent = false) {
    if (!silent) setLoading(true);
    try {
      const response = await listOrganizationExports(businessId, 12);
      setJobs(response.exports);
    } catch {
      if (!silent) {
        pushToast({ title: t("exports.loadFailed"), tone: "danger" });
      }
    } finally {
      if (!silent) setLoading(false);
    }
  }

  useEffect(() => {
    void reload();
  }, [businessId]);

  useEffect(() => {
    if (!hasWork) return;
    const timer = window.setInterval(() => {
      void reload(true);
    }, 2500);
    return () => window.clearInterval(timer);
  }, [businessId, hasWork]);

  async function requestExport() {
    setRequesting(true);
    try {
      const response = await createOrganizationExport(businessId, kind);
      setJobs((current) => [response.export, ...current]);
      pushToast({ title: t("exports.requested"), tone: "success" });
    } catch {
      pushToast({ title: t("exports.requestFailed"), tone: "danger" });
    } finally {
      setRequesting(false);
    }
  }

  async function download(job: OrganizationExport) {
    setDownloading(job.id);
    try {
      const blob = await downloadOrganizationExport(businessId, job.id);
      const url = URL.createObjectURL(blob);
      const link = document.createElement("a");
      link.href = url;
      link.download = `actilens-${job.kind}-${job.id}.${exportExtension(job.kind)}`;
      document.body.appendChild(link);
      link.click();
      link.remove();
      URL.revokeObjectURL(url);
    } catch {
      pushToast({ title: t("exports.downloadFailed"), tone: "danger" });
    } finally {
      setDownloading(null);
    }
  }

  return (
    <Card className="settings-card settings-export-card">
      <div className="settings-export-create">
        <div className="settings-row__copy">
          <div className="settings-row__title">{t("exports.title")}</div>
          <div className="settings-row__description">{t("exports.description")}</div>
        </div>
        <div className="settings-export-create__controls">
          <SelectMenu
            id="organization-export-kind"
            label={t("exports.kind")}
            value={kind}
            disabled={requesting}
            options={EXPORT_KINDS.map((value) => ({
              value,
              label: t(`exports.kinds.${value}`),
            }))}
            onChange={setKind}
          />
          <Button
            variant="secondary"
            loading={requesting}
            onClick={requestExport}
          >
            {t("exports.create")}
          </Button>
        </div>
      </div>

      <div className="settings-export-list">
        {loading ? (
          <>
            <Skeleton height={62} />
            <Skeleton height={62} />
          </>
        ) : jobs.length === 0 ? (
          <div className="settings-export-empty">{t("exports.empty")}</div>
        ) : (
          jobs.map((job) => (
            <div className="settings-export-row" key={job.id}>
              <div className="settings-export-row__copy">
                <div className="settings-export-row__title">
                  {t(`exports.kinds.${job.kind}`)}
                </div>
                <div className="settings-export-row__meta">
                  {new Date(job.created_at).toLocaleString()}
                  {job.expires_at && job.status === "ready"
                    ? ` · ${t("exports.expires", {
                        value: new Date(job.expires_at).toLocaleString(),
                      })}`
                    : ""}
                </div>
                {job.status === "failed" && (
                  <div className="settings-export-row__error">
                    {t("exports.failed")}
                  </div>
                )}
              </div>
              <div className="settings-export-row__actions">
                <Badge tone={statusTone(job.status)}>
                  {t(`exports.status.${job.status}`)}
                </Badge>
                {job.status === "ready" && (
                  <Button
                    size="sm"
                    variant="ghost"
                    loading={downloading === job.id}
                    disabled={downloading !== null && downloading !== job.id}
                    onClick={() => download(job)}
                  >
                    {t("exports.download")}
                  </Button>
                )}
              </div>
            </div>
          ))
        )}
      </div>
    </Card>
  );
}
