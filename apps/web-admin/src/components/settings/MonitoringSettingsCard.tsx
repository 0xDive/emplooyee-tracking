import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import {
  defaultMonitoringImpact,
  updateBusinessSettings,
  updateDefaultMonitoring,
} from "../../api/endpoints";
import type { BusinessAccess, BusinessSettingsPatch } from "../../api/types";
import { Alert, Badge, Button, Card, Dialog, Switch } from "../ds";
import { useToast } from "../ToastProvider";

function Row({
  title,
  description,
  children,
}: {
  title: string;
  description: string;
  children: ReactNode;
}) {
  return (
    <div className="settings-row">
      <div className="settings-row__copy">
        <div className="settings-row__title">{title}</div>
        <div className="settings-row__description">{description}</div>
      </div>
      <div className="settings-row__control">{children}</div>
    </div>
  );
}

export function MonitoringSettingsCard({
  access,
  onReload,
}: {
  access: BusinessAccess;
  onReload: () => Promise<void> | void;
}) {
  const { t } = useTranslation("settings");
  const { pushToast } = useToast();
  const { business } = access;
  const [saving, setSaving] = useState(false);
  const [pendingDefault, setPendingDefault] = useState<{
    enabled: boolean;
    affected: number;
  } | null>(null);
  const [dialogError, setDialogError] = useState<string | null>(null);

  async function patch(patch: BusinessSettingsPatch, success: string) {
    setSaving(true);
    try {
      await updateBusinessSettings(business.id, patch);
      await onReload();
      pushToast({ title: success, tone: "success" });
    } catch {
      pushToast({ title: t("saveError"), tone: "danger" });
    } finally {
      setSaving(false);
    }
  }

  async function requestDefaultChange(enabled: boolean) {
    setSaving(true);
    setDialogError(null);
    try {
      const impact = await defaultMonitoringImpact(business.id, enabled);
      setPendingDefault({ enabled, affected: impact.affected_count });
    } catch {
      pushToast({ title: t("saveError"), tone: "danger" });
    } finally {
      setSaving(false);
    }
  }

  async function applyDefault(applyExisting: boolean) {
    if (!pendingDefault) return;
    setSaving(true);
    setDialogError(null);
    try {
      const result = await updateDefaultMonitoring(
        business.id,
        pendingDefault.enabled,
        applyExisting,
      );
      await onReload();
      setPendingDefault(null);
      pushToast({
        title: applyExisting
          ? t("foundation.monitoring.defaultApplied", {
              count: result.affected_count,
            })
          : t("foundation.monitoring.defaultSaved"),
        tone: "success",
      });
    } catch {
      setDialogError(t("saveError"));
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      <Card className="settings-card">
        <Row
          title={t("foundation.monitoring.activeIdle")}
          description={t("foundation.monitoring.activeIdleHelp")}
        >
          <Badge tone="success">{t("foundation.monitoring.alwaysOn")}</Badge>
        </Row>

        <Row
          title={t("idleThreshold.title")}
          description={t("idleThreshold.desc")}
        >
          <div className="settings-segmented" role="group">
            {[
              { value: 60, minutes: 1 },
              { value: 180, minutes: 3 },
              { value: 300, minutes: 5 },
            ].map((option) => (
              <button
                key={option.value}
                type="button"
                className={`settings-segmented__option${
                  business.idle_threshold_s === option.value ? " is-active" : ""
                }`}
                disabled={saving}
                aria-pressed={business.idle_threshold_s === option.value}
                onClick={() =>
                  patch(
                    { idle_threshold_s: option.value },
                    t("foundation.monitoring.collectionSaved"),
                  )
                }
              >
                {t("presets.min", { count: option.minutes })}
              </button>
            ))}
          </div>
        </Row>

        <Row
          title={t("foundation.monitoring.defaultMember")}
          description={t("foundation.monitoring.defaultMemberHelp")}
        >
          <Switch
            checked={business.default_member_monitoring_enabled}
            disabled={saving}
            label={
              business.default_member_monitoring_enabled
                ? t("foundation.enabled")
                : t("foundation.disabled")
            }
            onCheckedChange={requestDefaultChange}
          />
        </Row>

        <Row
          title={t("foundation.monitoring.appActivity")}
          description={t("foundation.monitoring.appActivityHelp")}
        >
          <Switch
            checked={business.collect_app_activity}
            disabled={saving}
            label={
              business.collect_app_activity
                ? t("foundation.enabled")
                : t("foundation.disabled")
            }
            onCheckedChange={(enabled) =>
              patch(
                enabled
                  ? { collect_app_activity: true }
                  : {
                      collect_app_activity: false,
                      collect_window_titles: false,
                    },
                t("foundation.monitoring.collectionSaved"),
              )
            }
          />
        </Row>

        <Row
          title={t("foundation.monitoring.windowTitles")}
          description={t("foundation.monitoring.windowTitlesHelp")}
        >
          <Switch
            checked={business.collect_window_titles}
            disabled={saving || !business.collect_app_activity}
            label={
              business.collect_window_titles
                ? t("foundation.enabled")
                : t("foundation.disabled")
            }
            onCheckedChange={(enabled) =>
              patch(
                { collect_window_titles: enabled },
                t("foundation.monitoring.collectionSaved"),
              )
            }
          />
        </Row>

        <Row
          title={t("foundation.monitoring.browser")}
          description={t("foundation.monitoring.browserHelp")}
        >
          <Switch
            checked={business.collect_browser_activity}
            disabled={saving}
            label={
              business.collect_browser_activity
                ? t("foundation.enabled")
                : t("foundation.disabled")
            }
            onCheckedChange={(enabled) =>
              patch(
                { collect_browser_activity: enabled },
                t("foundation.monitoring.collectionSaved"),
              )
            }
          />
        </Row>

        <Row
          title={t("foundation.monitoring.keystrokes")}
          description={t("foundation.monitoring.keystrokesHelp")}
        >
          <Switch
            checked={business.collect_keystroke_counts}
            disabled={saving}
            label={
              business.collect_keystroke_counts
                ? t("foundation.enabled")
                : t("foundation.disabled")
            }
            onCheckedChange={(enabled) =>
              patch(
                { collect_keystroke_counts: enabled },
                t("foundation.monitoring.collectionSaved"),
              )
            }
          />
        </Row>
      </Card>

      {pendingDefault && (
        <Dialog
          title={t("foundation.monitoring.defaultDialogTitle")}
          size="confirm"
          onClose={() => !saving && setPendingDefault(null)}
          closeOnBackdrop={!saving}
          footer={
            <>
              <Button
                variant="secondary"
                disabled={saving}
                onClick={() => setPendingDefault(null)}
              >
                {t("foundation.cancel")}
              </Button>
              <Button
                variant="secondary"
                disabled={saving}
                onClick={() => applyDefault(false)}
              >
                {t("foundation.monitoring.futureOnly")}
              </Button>
              <Button
                variant="primary"
                loading={saving}
                onClick={() => applyDefault(true)}
              >
                {t("foundation.monitoring.applyExisting", {
                  count: pendingDefault.affected,
                })}
              </Button>
            </>
          }
        >
          <div className="settings-dialog-stack">
            <Alert tone="info">
              {t("foundation.monitoring.defaultDialogBody", {
                state: pendingDefault.enabled
                  ? t("foundation.enabled")
                  : t("foundation.disabled"),
                count: pendingDefault.affected,
              })}
            </Alert>
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </div>
        </Dialog>
      )}
    </>
  );
}
