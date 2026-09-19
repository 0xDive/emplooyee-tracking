import { useEffect, useState, type FormEvent, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { updateBusinessSettings } from "../../api/endpoints";
import type { BusinessAccess, BusinessSettingsPatch } from "../../api/types";
import { Button, Card, TextField } from "../ds";
import { useToast } from "../ToastProvider";

const TTL_OPTIONS = [
  { seconds: 3600, key: "1h" },
  { seconds: 86400, key: "24h" },
  { seconds: 259200, key: "72h" },
  { seconds: 604800, key: "7d" },
];

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

export function DeviceEnrollmentSettingsCard({
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
  const [limit, setLimit] = useState(
    business.device_limit === null ? "" : String(business.device_limit),
  );

  useEffect(() => {
    setLimit(business.device_limit === null ? "" : String(business.device_limit));
  }, [business.device_limit]);

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

  async function saveLimit(event: FormEvent) {
    event.preventDefault();
    const trimmed = limit.trim();
    const next = trimmed === "" ? null : Number(trimmed);
    if (next !== null && (!Number.isInteger(next) || next < 1 || next > 1000)) {
      pushToast({ title: t("foundation.devices.limitInvalid"), tone: "danger" });
      return;
    }
    await patch({ device_limit: next }, t("foundation.devices.limitSaved"));
  }

  return (
    <Card className="settings-card">
      <Row
        title={t("foundation.devices.limit")}
        description={t("foundation.devices.limitHelp")}
      >
        <form className="settings-device-limit" onSubmit={saveLimit}>
          <TextField
            id="device-limit"
            label={t("foundation.devices.limitField")}
            type="number"
            min={1}
            max={1000}
            value={limit}
            placeholder={t("foundation.devices.unlimited")}
            disabled={saving}
            onChange={(event) => setLimit(event.target.value)}
          />
          <Button
            type="submit"
            variant="secondary"
            disabled={
              saving ||
              limit ===
                (business.device_limit === null ? "" : String(business.device_limit))
            }
          >
            {t("foundation.save")}
          </Button>
        </form>
      </Row>

      <Row
        title={t("foundation.devices.enrollmentTTL")}
        description={t("foundation.devices.enrollmentTTLHelp")}
      >
        <div className="settings-segmented" role="group">
          {TTL_OPTIONS.map((option) => (
            <button
              key={option.seconds}
              type="button"
              className={`settings-segmented__option${
                business.enrollment_token_ttl_s === option.seconds ? " is-active" : ""
              }`}
              disabled={saving}
              aria-pressed={business.enrollment_token_ttl_s === option.seconds}
              onClick={() =>
                patch(
                  { enrollment_token_ttl_s: option.seconds },
                  t("foundation.devices.ttlSaved"),
                )
              }
            >
              {t(`foundation.devices.ttl.${option.key}`)}
            </button>
          ))}
        </div>
      </Row>

      <Row
        title={t("foundation.devices.enrollmentBehavior")}
        description={t("foundation.devices.enrollmentBehaviorHelp")}
      >
        <div className="settings-readonly">
          <span className="settings-readonly__value">
            {t("foundation.devices.automaticEnrollment")}
          </span>
        </div>
      </Row>
    </Card>
  );
}
