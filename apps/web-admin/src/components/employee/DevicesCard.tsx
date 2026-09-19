import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { listEmployeeDevices, updateDevice } from "../../api/endpoints";
import { ApiError, type Device, type Employee } from "../../api/types";
import {
  Alert,
  Badge,
  Button,
  Card,
  Dialog,
  EmptyState,
  Skeleton,
  TextField,
} from "../ds";
import { useToast } from "../ToastProvider";
import { EnrollmentTokenControl } from "./EnrollmentTokenControl";

function fmtTimestamp(timestamp: number | null, never: string): string {
  if (!timestamp) return never;
  return new Date(timestamp * 1000).toLocaleString();
}

function displayName(device: Device, unnamed: string): string {
  return device.label || device.hostname || unnamed;
}

function shortID(id: string): string {
  return id.length > 12 ? `${id.slice(0, 8)}…${id.slice(-4)}` : id;
}

export function DevicesCard({
  employee,
  employeeId,
  businessId,
  canChange = true,
}: {
  employee?: Employee | null;
  employeeId: string;
  businessId: string;
  canChange?: boolean;
}) {
  const { t } = useTranslation("dashboard");
  const { pushToast } = useToast();
  const [devices, setDevices] = useState<Device[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busyID, setBusyID] = useState<string | null>(null);
  const [renameDevice, setRenameDevice] = useState<Device | null>(null);
  const [renameValue, setRenameValue] = useState("");
  const [confirmDevice, setConfirmDevice] = useState<Device | null>(null);
  const [dialogError, setDialogError] = useState<string | null>(null);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const response = await listEmployeeDevices(employeeId, businessId);
      setDevices(response.devices);
    } catch {
      setError(t("detail.devices.loadFailed"));
    } finally {
      setLoading(false);
    }
  }, [businessId, employeeId, t]);

  useEffect(() => {
    load();
  }, [load]);

  async function saveRename() {
    if (!renameDevice) return;
    setBusyID(renameDevice.id);
    setDialogError(null);
    try {
      const response = await updateDevice(
        renameDevice.id,
        { label: renameValue.trim() },
        businessId,
      );
      setDevices((current) =>
        current.map((device) =>
          device.id === renameDevice.id ? response.device : device,
        ),
      );
      setRenameDevice(null);
      pushToast({
        title: t("detail.devices.saved"),
        tone: "success",
      });
    } catch (error) {
      setDialogError(
        error instanceof ApiError && error.code === "device_limit_reached"
          ? t("detail.devices.deviceLimitReached")
          : t("detail.devices.actionFailed"),
      );
    } finally {
      setBusyID(null);
    }
  }

  async function toggleRevoked() {
    if (!confirmDevice) return;
    const revoked = Boolean(confirmDevice.revoked_at);
    setBusyID(confirmDevice.id);
    setDialogError(null);
    try {
      const response = await updateDevice(
        confirmDevice.id,
        { revoked: !revoked },
        businessId,
      );
      setDevices((current) =>
        current.map((device) =>
          device.id === confirmDevice.id ? response.device : device,
        ),
      );
      setConfirmDevice(null);
      pushToast({
        title: t("detail.devices.saved"),
        tone: "success",
      });
    } catch (error) {
      setDialogError(
        error instanceof ApiError && error.code === "device_limit_reached"
          ? t("detail.devices.deviceLimitReached")
          : t("detail.devices.actionFailed"),
      );
    } finally {
      setBusyID(null);
    }
  }

  return (
    <Card>
      <div className="report-card__head device-card__head">
        <div>
          <h2 className="report-card__title">{t("detail.devices.title")}</h2>
          <p className="report-card__subtitle">{t("detail.devices.subtitle")}</p>
        </div>
        {employee && (
          <EnrollmentTokenControl
            employee={employee}
            businessId={businessId}
            canChange={canChange}
            triggerLabel={t("detail.devices.reenroll")}
          />
        )}
      </div>

      {!loading &&
        !error &&
        devices.some(
          (device) => !device.revoked_at && device.version_status === "outdated",
        ) && (
          <div className="device-health-warning">
            <Alert tone="warning">
              {t("detail.devices.outdatedWarning", {
                count: devices.filter(
                  (device) =>
                    !device.revoked_at && device.version_status === "outdated",
                ).length,
              })}
            </Alert>
          </div>
        )}

      {loading && (
        <div className="devices-v1" aria-hidden>
          {Array.from({ length: 2 }, (_, index) => (
            <Skeleton key={index} width="100%" height={76} />
          ))}
        </div>
      )}

      {error && <Alert tone="danger">{error}</Alert>}

      {!loading && !error && devices.length === 0 && (
        <EmptyState
          title={t("detail.devices.empty")}
          description={t("detail.devices.v1.emptyDescription")}
        />
      )}

      {!loading && !error && devices.length > 0 && (
        <div className="devices-v1">
          {devices.map((device) => {
            const revoked = Boolean(device.revoked_at);
            const name = displayName(device, t("detail.devices.unnamed"));
            const platform = [device.platform, device.arch]
              .filter(Boolean)
              .join(" · ");

            return (
              <div
                key={device.id}
                className={`device-row${revoked ? " is-revoked" : ""}`}
              >
                <div className="device-row__main">
                  <div className="device-row__title">
                    <span>{name}</span>
                    <Badge tone={revoked ? "danger" : "success"}>
                      {t(
                        revoked
                          ? "detail.devices.revoked"
                          : "detail.devices.active",
                      )}
                    </Badge>
                    {revoked && canChange && (
                      <span className="device-row__reenroll-hint">
                        {t("detail.devices.reenrollHint")}
                      </span>
                    )}
                    {!revoked && (
                      <Badge
                        tone={
                          device.version_status === "current"
                            ? "success"
                            : device.version_status === "outdated"
                              ? "warning"
                              : "neutral"
                        }
                      >
                        {t(`detail.devices.versionStatus.${device.version_status}`)}
                      </Badge>
                    )}
                  </div>
                  <div className="device-row__meta">
                    {platform || shortID(device.id)}
                    {device.app_version
                      ? ` · ${t("detail.devices.version", {
                          version: device.app_version,
                        })}`
                      : ""}
                  </div>
                  <div className="device-row__meta">
                    {t("detail.devices.lastSeen", {
                      value: fmtTimestamp(
                        device.last_seen,
                        t("detail.devices.never"),
                      ),
                    })}
                    {" · "}
                    {t("detail.devices.firstSeen", {
                      value: fmtTimestamp(
                        device.first_seen,
                        t("detail.devices.never"),
                      ),
                    })}
                  </div>
                </div>

                {canChange && (
                  <div className="device-row__actions">
                    <Button
                      variant="ghost"
                      size="sm"
                      disabled={busyID === device.id}
                      onClick={() => {
                        setRenameValue(device.label || device.hostname || "");
                        setDialogError(null);
                        setRenameDevice(device);
                      }}
                    >
                      {t("detail.devices.rename")}
                    </Button>
                    <Button
                      variant={revoked ? "secondary" : "danger-ghost"}
                      size="sm"
                      disabled={busyID === device.id}
                      onClick={() => {
                        setDialogError(null);
                        setConfirmDevice(device);
                      }}
                    >
                      {t(
                        revoked
                          ? "detail.devices.restore"
                          : "detail.devices.revoke",
                      )}
                    </Button>
                  </div>
                )}
              </div>
            );
          })}
        </div>
      )}

      {renameDevice && (
        <Dialog
          title={t("detail.devices.rename")}
          size="confirm"
          onClose={() => !busyID && setRenameDevice(null)}
          closeOnBackdrop={!busyID}
          footer={
            <>
              <Button
                variant="secondary"
                disabled={Boolean(busyID)}
                onClick={() => setRenameDevice(null)}
              >
                {t("newBusinessModal.cancel")}
              </Button>
              <Button
                variant="primary"
                loading={busyID === renameDevice.id}
                onClick={saveRename}
              >
                {t("common:actions.save")}
              </Button>
            </>
          }
        >
          <div className="detail-dialog-stack">
            <TextField
              id={`device-name-${renameDevice.id}`}
              label={t("detail.devices.renamePrompt")}
              value={renameValue}
              onChange={(event) => setRenameValue(event.target.value)}
              disabled={busyID === renameDevice.id}
              autoFocus
            />
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </div>
        </Dialog>
      )}

      {confirmDevice && (
        <Dialog
          title={t(
            confirmDevice.revoked_at
              ? "detail.devices.restore"
              : "detail.devices.revoke",
          )}
          size="confirm"
          onClose={() => !busyID && setConfirmDevice(null)}
          closeOnBackdrop={!busyID}
          footer={
            <>
              <Button
                variant="secondary"
                disabled={Boolean(busyID)}
                onClick={() => setConfirmDevice(null)}
              >
                {t("newBusinessModal.cancel")}
              </Button>
              <Button
                variant={confirmDevice.revoked_at ? "primary" : "danger"}
                loading={busyID === confirmDevice.id}
                onClick={toggleRevoked}
              >
                {t(
                  confirmDevice.revoked_at
                    ? "detail.devices.restore"
                    : "detail.devices.revoke",
                )}
              </Button>
            </>
          }
        >
          <div className="detail-dialog-stack">
            <Alert tone={confirmDevice.revoked_at ? "info" : "warning"}>
              {t(
                confirmDevice.revoked_at
                  ? "detail.devices.confirmRestore"
                  : "detail.devices.confirmRevoke",
              )}
            </Alert>
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </div>
        </Dialog>
      )}
    </Card>
  );
}
