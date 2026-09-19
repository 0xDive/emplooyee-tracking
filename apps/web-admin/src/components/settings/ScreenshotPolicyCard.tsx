import { useState, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { updateBusinessSettings } from "../../api/endpoints";
import type { BusinessAccess, BusinessSettingsPatch, ScreenshotCaptureScope } from "../../api/types";
import { Card, Switch } from "../ds";
import { useToast } from "../ToastProvider";

const INTERVALS = [
  { value: 60, minutes: 1 },
  { value: 300, minutes: 5 },
  { value: 600, minutes: 10 },
  { value: 900, minutes: 15 },
];

function Row({
  title,
  description,
  children,
  top = false,
}: {
  title: string;
  description: string;
  children: ReactNode;
  top?: boolean;
}) {
  return (
    <div className={`settings-row${top ? " settings-row--top" : ""}`}>
      <div className="settings-row__copy">
        <div className="settings-row__title">{title}</div>
        <div className="settings-row__description">{description}</div>
      </div>
      <div className="settings-row__control">{children}</div>
    </div>
  );
}

export function ScreenshotPolicyCard({
  access,
  onReload,
  onManagePrivacy,
}: {
  access: BusinessAccess;
  onReload: () => Promise<void> | void;
  onManagePrivacy: () => void;
}) {
  const { t } = useTranslation("settings");
  const { pushToast } = useToast();
  const { business } = access;
  const [saving, setSaving] = useState(false);

  async function patch(patch: BusinessSettingsPatch) {
    setSaving(true);
    try {
      await updateBusinessSettings(business.id, patch);
      await onReload();
      pushToast({ title: t("foundation.screenshots.saved"), tone: "success" });
    } catch {
      pushToast({ title: t("saveError"), tone: "danger" });
    } finally {
      setSaving(false);
    }
  }

  return (
    <Card className="settings-card">
      <Row
        title={t("foundation.screenshots.enabled")}
        description={t("foundation.screenshots.enabledHelp")}
      >
        <Switch
          checked={business.collect_screenshots}
          disabled={saving}
          label={
            business.collect_screenshots
              ? t("foundation.enabled")
              : t("foundation.disabled")
          }
          onCheckedChange={(enabled) => patch({ collect_screenshots: enabled })}
        />
      </Row>

      <Row
        top
        title={t("foundation.screenshots.scope")}
        description={t("foundation.screenshots.scopeHelp")}
      >
        <div className="settings-mode-grid" role="radiogroup">
          {(
            [
              ["active_window", "activeWindow"],
              ["active_display", "activeDisplay"],
              ["all_displays", "allDisplays"],
            ] as Array<[ScreenshotCaptureScope, string]>
          ).map(([value, key]) => (
            <button
              key={value}
              type="button"
              role="radio"
              aria-checked={business.screenshot_capture_scope === value}
              className={`settings-mode${
                business.screenshot_capture_scope === value ? " is-active" : ""
              }`}
              disabled={saving || !business.collect_screenshots}
              onClick={() => patch({ screenshot_capture_scope: value })}
            >
              <span className="settings-mode__radio" aria-hidden />
              <span>
                <span className="settings-mode__label">
                  {t(`foundation.screenshots.scopes.${key}.label`)}
                </span>
                <span className="settings-mode__description">
                  {t(`foundation.screenshots.scopes.${key}.description`)}
                </span>
              </span>
            </button>
          ))}
        </div>
      </Row>

      <Row
        title={t("screenshotInterval.title")}
        description={t("foundation.screenshots.intervalHelp")}
      >
        <div className="settings-segmented" role="group">
          {INTERVALS.map((option) => (
            <button
              key={option.value}
              type="button"
              className={`settings-segmented__option${
                business.screenshot_interval_s === option.value ? " is-active" : ""
              }`}
              disabled={saving || !business.collect_screenshots}
              aria-pressed={business.screenshot_interval_s === option.value}
              onClick={() => patch({ screenshot_interval_s: option.value })}
            >
              {t("presets.min", { count: option.minutes })}
            </button>
          ))}
        </div>
      </Row>

      <Row
        title={t("foundation.screenshots.privacy")}
        description={t("foundation.screenshots.privacyHelp")}
      >
        <button
          type="button"
          className="ds-button ds-button--secondary ds-button--sm"
          onClick={onManagePrivacy}
        >
          {t("skipApps.manage")}
        </button>
      </Row>
    </Card>
  );
}
