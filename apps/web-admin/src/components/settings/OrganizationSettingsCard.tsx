import { useEffect, useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { updateOrganization } from "../../api/endpoints";
import type { BusinessAccess, BusinessKind } from "../../api/types";
import { Alert, Button, Dialog, SelectMenu } from "../ds";
import { useToast } from "../ToastProvider";

function timezones(current: string): string[] {
  const fn = (Intl as unknown as {
    supportedValuesOf?: (key: string) => string[];
  }).supportedValuesOf;
  const values = fn ? fn("timeZone") : ["UTC", current];
  return Array.from(new Set([current, ...values])).filter(Boolean);
}

export function OrganizationSettingsCard({
  access,
  onReload,
}: {
  access: BusinessAccess;
  onReload: () => Promise<void> | void;
}) {
  const { t } = useTranslation("settings");
  const { pushToast } = useToast();
  const { business, role } = access;
  const [name, setName] = useState(business.name);
  const [saving, setSaving] = useState(false);
  const [kindTarget, setKindTarget] = useState<BusinessKind | null>(null);

  useEffect(() => setName(business.name), [business.name]);

  const zoneOptions = useMemo(() => timezones(business.timezone), [business.timezone]);
  const canChangeKind = role === "owner";

  async function patch(
    body: Parameters<typeof updateOrganization>[1],
    success: string,
  ) {
    setSaving(true);
    try {
      await updateOrganization(business.id, body);
      await onReload();
      pushToast({ title: success, tone: "success" });
    } catch {
      pushToast({ title: t("saveError"), tone: "danger" });
    } finally {
      setSaving(false);
    }
  }

  return (
    <>
      <div className="settings-card ds-card">
        <div className="settings-row">
          <div className="settings-row__copy">
            <div className="settings-row__title">{t("v1.organization.name")}</div>
            <div className="settings-row__description">
              {t("foundation.organization.nameHelp")}
            </div>
          </div>
          <div className="settings-row__control">
            <div className="settings-identity-edit">
              <input
                className="ds-input"
                value={name}
                maxLength={120}
                disabled={saving}
                aria-label={t("v1.organization.name")}
                onChange={(event) => setName(event.target.value)}
              />
              <Button
                variant="secondary"
                disabled={saving || !name.trim() || name.trim() === business.name}
                onClick={() =>
                  patch({ name: name.trim() }, t("foundation.organization.nameSaved"))
                }
              >
                {t("foundation.save")}
              </Button>
            </div>
          </div>
        </div>

        <div className="settings-row settings-row--top">
          <div className="settings-row__copy">
            <div className="settings-row__title">{t("v1.organization.type")}</div>
            <div className="settings-row__description">
              {t("foundation.organization.typeHelp")}
            </div>
          </div>
          <div className="settings-row__control">
            <div className="settings-segmented" role="group" aria-label={t("v1.organization.type")}>
              {(["team", "family", "other"] as BusinessKind[]).map((kind) => (
                <button
                  key={kind}
                  type="button"
                  className={`settings-segmented__option${business.kind === kind ? " is-active" : ""}`}
                  disabled={saving || !canChangeKind}
                  aria-pressed={business.kind === kind}
                  title={!canChangeKind ? t("foundation.organization.ownerOnly") : undefined}
                  onClick={() => kind !== business.kind && setKindTarget(kind)}
                >
                  {t(`foundation.organization.kinds.${kind}`)}
                </button>
              ))}
            </div>
          </div>
        </div>

        <div className="settings-row">
          <div className="settings-row__copy">
            <div className="settings-row__title">{t("foundation.organization.timezone")}</div>
            <div className="settings-row__description">
              {t("foundation.organization.timezoneHelp")}
            </div>
          </div>
          <div className="settings-row__control">
            <SelectMenu
              id="organization-timezone"
              className="settings-timezone"
              ariaLabel={t("foundation.organization.timezone")}
              value={business.timezone}
              disabled={saving}
              options={zoneOptions.map((zone) => ({ value: zone, label: zone }))}
              searchable
              searchPlaceholder={t("foundation.organization.timezoneSearch")}
              emptyText={t("foundation.organization.timezoneNoMatches")}
              menuWidth={420}
              onChange={(timezone) =>
                patch({ timezone }, t("foundation.organization.timezoneSaved"))
              }
            />
          </div>
        </div>

        <div className="settings-row">
          <div className="settings-row__copy">
            <div className="settings-row__title">{t("foundation.organization.weekStart")}</div>
            <div className="settings-row__description">
              {t("foundation.organization.weekStartHelp")}
            </div>
          </div>
          <div className="settings-row__control">
            <SelectMenu
              id="organization-week-start"
              className="settings-week-start"
              ariaLabel={t("foundation.organization.weekStart")}
              value={business.week_starts_on === null ? "auto" : String(business.week_starts_on)}
              disabled={saving}
              options={[
                { value: "auto", label: t("foundation.organization.weekDays.auto") },
                ...Array.from({ length: 7 }, (_, day) => ({
                  value: String(day),
                  label: t(`foundation.organization.weekDays.${day}`),
                })),
              ]}
              onChange={(value) =>
                patch(
                  { week_starts_on: value === "auto" ? null : Number(value) },
                  t("foundation.organization.weekStartSaved"),
                )
              }
            />
          </div>
        </div>

        <div className="settings-row">
          <div className="settings-row__copy">
            <div className="settings-row__title">{t("v1.organization.yourRole")}</div>
          </div>
          <div className="settings-row__control">
            <div className="settings-readonly">
              <span className="settings-readonly__value">
                {t(`v1.roles.${role}`)}
              </span>
            </div>
          </div>
        </div>
      </div>

      {kindTarget && (
        <Dialog
          title={t("foundation.organization.changeTypeTitle")}
          size="confirm"
          onClose={() => !saving && setKindTarget(null)}
          closeOnBackdrop={!saving}
          footer={
            <>
              <Button variant="secondary" disabled={saving} onClick={() => setKindTarget(null)}>
                {t("foundation.cancel")}
              </Button>
              <Button
                variant="primary"
                loading={saving}
                onClick={async () => {
                  await patch(
                    { kind: kindTarget },
                    t("foundation.organization.typeSaved"),
                  );
                  setKindTarget(null);
                }}
              >
                {t("foundation.organization.confirmType")}
              </Button>
            </>
          }
        >
          <div className="settings-dialog-stack">
            <Alert tone="info">
              {t("foundation.organization.typeSafeIntro", {
                type: t(`foundation.organization.kinds.${kindTarget}`),
              })}
            </Alert>
            <div className="settings-change-summary">
              <strong>{t("foundation.organization.willChange")}</strong>
              <ul>
                <li>{t("foundation.organization.changeTerminology")}</li>
              </ul>
              <strong>{t("foundation.organization.willNotChange")}</strong>
              <ul>
                <li>{t("foundation.organization.noDataLoss")}</li>
                <li>{t("foundation.organization.noMemberChanges")}</li>
                <li>{t("foundation.organization.noPolicyReset")}</li>
              </ul>
            </div>
          </div>
        </Dialog>
      )}
    </>
  );
}
