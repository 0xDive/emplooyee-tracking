import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  archiveOrganization,
  cancelOrganizationDeletion,
  createOrganizationExport,
  createReauthGrant,
  listBusinessEmployees,
  organizationDeletionPreview,
  restoreOrganization,
  scheduleOrganizationDeletion,
  transferOrganizationOwnership,
} from "../../api/endpoints";
import {
  ApiError,
  type BusinessAccess,
  type Employee,
  type OrganizationDeletionPreview,
} from "../../api/types";
import { useAuth } from "../../auth/AuthContext";
import { Alert, Badge, Button, Card, Dialog, SelectField, TextField } from "../ds";
import { useToast } from "../ToastProvider";

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${(value / 1024).toFixed(1)} KB`;
  return `${(value / (1024 * 1024)).toFixed(1)} MB`;
}

export function OrganizationLifecycleCard({
  access,
  onReload,
}: {
  access: BusinessAccess;
  onReload: () => Promise<void> | void;
}) {
  const { t } = useTranslation("settings");
  const { pushToast } = useToast();
  const { logout } = useAuth();
  const navigate = useNavigate();
  const { business, role } = access;

  const [busy, setBusy] = useState(false);
  const [archiveOpen, setArchiveOpen] = useState(false);
  const [transferOpen, setTransferOpen] = useState(false);
  const [deleteOpen, setDeleteOpen] = useState(false);
  const [members, setMembers] = useState<Employee[]>([]);
  const [targetUserId, setTargetUserId] = useState("");
  const [preview, setPreview] = useState<OrganizationDeletionPreview | null>(null);
  const [currentPassword, setCurrentPassword] = useState("");
  const [mfaCode, setMfaCode] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [dialogError, setDialogError] = useState<string | null>(null);
  const [exporting, setExporting] = useState(false);

  const isOwner = role === "owner";
  const archived = Boolean(business.archived_at);
  const deletionPending = Boolean(business.deletion_scheduled_at);

  const admins = useMemo(
    () =>
      members.filter(
        (member) =>
          member.role === "admin" &&
          member.status === "active" &&
          member.active,
      ),
    [members],
  );

  useEffect(() => {
    if (!isOwner || archived) {
      setMembers([]);
      return;
    }
    listBusinessEmployees(business.id)
      .then((response) => setMembers(response.employees))
      .catch(() => setMembers([]));
  }, [archived, business.id, isOwner]);

  function resetSecurityFields() {
    setDialogError(null);
    setCurrentPassword("");
    setMfaCode("");
    setConfirmation("");
  }

  async function archive() {
    setBusy(true);
    setDialogError(null);
    try {
      await archiveOrganization(business.id);
      setArchiveOpen(false);
      await onReload();
      pushToast({ title: t("lifecycle.archived"), tone: "success" });
    } catch (error) {
      setDialogError(
        error instanceof ApiError ? error.message : t("lifecycle.actionFailed"),
      );
    } finally {
      setBusy(false);
    }
  }

  async function restore() {
    setBusy(true);
    try {
      await restoreOrganization(business.id);
      await onReload();
      pushToast({ title: t("lifecycle.restored"), tone: "success" });
    } catch {
      pushToast({ title: t("lifecycle.actionFailed"), tone: "danger" });
    } finally {
      setBusy(false);
    }
  }

  async function openTransfer() {
    resetSecurityFields();
    if (members.length === 0) {
      try {
        const response = await listBusinessEmployees(business.id);
        setMembers(response.employees);
        const first = response.employees.find(
          (member) => member.role === "admin" && member.status === "active",
        );
        setTargetUserId(first?.id ?? "");
      } catch {
        setTargetUserId("");
      }
    } else {
      setTargetUserId(admins[0]?.id ?? "");
    }
    setTransferOpen(true);
  }

  async function transfer(event: FormEvent) {
    event.preventDefault();
    if (!targetUserId) return;
    setBusy(true);
    setDialogError(null);
    try {
      const grant = await createReauthGrant(currentPassword, mfaCode);
      await transferOrganizationOwnership(
        business.id,
        targetUserId,
        grant.reauth_token,
      );
      // Ownership transfer invalidates sessions for both owners by design.
      logout();
      navigate("/login", { replace: true });
    } catch (error) {
      setDialogError(
        error instanceof ApiError && error.code === "mfa_required"
          ? t("lifecycle.mfaRequired")
          : t("lifecycle.transferFailed"),
      );
    } finally {
      setBusy(false);
    }
  }

  async function openDeletion() {
    resetSecurityFields();
    setPreview(null);
    setDeleteOpen(true);
    try {
      const next = await organizationDeletionPreview(business.id);
      setPreview(next);
    } catch {
      setDialogError(t("lifecycle.previewFailed"));
    }
  }

  async function requestBackup() {
    setExporting(true);
    try {
      await createOrganizationExport(business.id, "full");
      pushToast({ title: t("lifecycle.backupQueued"), tone: "success" });
    } catch {
      pushToast({ title: t("exports.requestFailed"), tone: "danger" });
    } finally {
      setExporting(false);
    }
  }

  async function scheduleDeletion(event: FormEvent) {
    event.preventDefault();
    if (confirmation !== business.name) return;
    setBusy(true);
    setDialogError(null);
    try {
      const grant = await createReauthGrant(currentPassword, mfaCode);
      await scheduleOrganizationDeletion(
        business.id,
        grant.reauth_token,
        confirmation,
      );
      setDeleteOpen(false);
      await onReload();
      pushToast({ title: t("lifecycle.deletionScheduled"), tone: "success" });
    } catch (error) {
      setDialogError(
        error instanceof ApiError && error.code === "mfa_required"
          ? t("lifecycle.mfaRequired")
          : t("lifecycle.scheduleFailed"),
      );
    } finally {
      setBusy(false);
    }
  }

  async function cancelDeletion() {
    setBusy(true);
    try {
      await cancelOrganizationDeletion(business.id);
      await onReload();
      pushToast({ title: t("lifecycle.deletionCancelled"), tone: "success" });
    } catch {
      pushToast({ title: t("lifecycle.actionFailed"), tone: "danger" });
    } finally {
      setBusy(false);
    }
  }

  return (
    <>
      <Card className="settings-card settings-lifecycle-card">
        <div className="settings-row">
          <div className="settings-row__copy">
            <div className="settings-row__title">{t("lifecycle.status")}</div>
            <div className="settings-row__description">
              {deletionPending
                ? t("lifecycle.deletionPendingHelp", {
                    value: new Date(business.deletion_scheduled_at!).toLocaleString(),
                  })
                : archived
                  ? t("lifecycle.archivedHelp")
                  : t("lifecycle.activeHelp")}
            </div>
          </div>
          <div className="settings-row__control">
            <div className="settings-inline">
              <Badge tone={deletionPending ? "danger" : archived ? "warning" : "success"}>
                {deletionPending
                  ? t("lifecycle.deletionPending")
                  : archived
                    ? t("lifecycle.archivedStatus")
                    : t("lifecycle.activeStatus")}
              </Badge>
              {deletionPending ? (
                isOwner && (
                  <Button
                    size="sm"
                    variant="secondary"
                    loading={busy}
                    onClick={cancelDeletion}
                  >
                    {t("lifecycle.cancelDeletion")}
                  </Button>
                )
              ) : archived ? (
                <Button size="sm" variant="secondary" loading={busy} onClick={restore}>
                  {t("lifecycle.restore")}
                </Button>
              ) : (
                <Button
                  size="sm"
                  variant="danger-ghost"
                  onClick={() => {
                    resetSecurityFields();
                    setArchiveOpen(true);
                  }}
                >
                  {t("lifecycle.archive")}
                </Button>
              )}
            </div>
          </div>
        </div>

        {isOwner && !archived && (
          <div className="settings-row">
            <div className="settings-row__copy">
              <div className="settings-row__title">{t("lifecycle.transfer")}</div>
              <div className="settings-row__description">{t("lifecycle.transferHelp")}</div>
            </div>
            <div className="settings-row__control">
              <Button
                size="sm"
                variant="secondary"
                disabled={admins.length === 0 && members.length > 0}
                onClick={openTransfer}
              >
                {t("lifecycle.transfer")}
              </Button>
            </div>
          </div>
        )}

        {isOwner && archived && !deletionPending && (
          <div className="settings-row">
            <div className="settings-row__copy">
              <div className="settings-row__title settings-danger-copy">
                {t("lifecycle.delete")}
              </div>
              <div className="settings-row__description">
                {t("lifecycle.deleteHelp")}
              </div>
            </div>
            <div className="settings-row__control">
              <Button size="sm" variant="danger" onClick={openDeletion}>
                {t("lifecycle.scheduleDeletion")}
              </Button>
            </div>
          </div>
        )}
      </Card>

      {archiveOpen && (
        <Dialog
          title={t("lifecycle.archiveTitle")}
          size="confirm"
          onClose={() => !busy && setArchiveOpen(false)}
          closeOnBackdrop={!busy}
          footer={
            <>
              <Button
                variant="secondary"
                disabled={busy}
                onClick={() => setArchiveOpen(false)}
              >
                {t("foundation.cancel")}
              </Button>
              <Button variant="danger" loading={busy} onClick={archive}>
                {t("lifecycle.archive")}
              </Button>
            </>
          }
        >
          <div className="settings-dialog-stack">
            <Alert tone="warning">{t("lifecycle.archiveWarning")}</Alert>
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </div>
        </Dialog>
      )}

      {transferOpen && (
        <Dialog
          title={t("lifecycle.transferTitle")}
          size="confirm"
          onClose={() => !busy && setTransferOpen(false)}
          closeOnBackdrop={!busy}
          footer={
            <>
              <Button
                variant="secondary"
                disabled={busy}
                onClick={() => setTransferOpen(false)}
              >
                {t("foundation.cancel")}
              </Button>
              <Button
                type="submit"
                form="organization-transfer-form"
                variant="danger"
                loading={busy}
                disabled={!targetUserId || !currentPassword}
              >
                {t("lifecycle.transfer")}
              </Button>
            </>
          }
        >
          <form
            id="organization-transfer-form"
            className="settings-dialog-stack"
            onSubmit={transfer}
          >
            <Alert tone="warning">{t("lifecycle.transferWarning")}</Alert>
            {admins.length > 0 ? (
              <SelectField
                id="transfer-target"
                label={t("lifecycle.newOwner")}
                value={targetUserId}
                onChange={(event) => setTargetUserId(event.currentTarget.value)}
              >
                <option value="">{t("lifecycle.selectAdmin")}</option>
                {admins.map((member) => (
                  <option value={member.id} key={member.id}>
                    {member.display_name}
                  </option>
                ))}
              </SelectField>
            ) : (
              <Alert tone="info">{t("lifecycle.noAdmins")}</Alert>
            )}
            <TextField
              id="transfer-password"
              label={t("accountSecurity.currentPassword")}
              type="password"
              value={currentPassword}
              onChange={(event) => setCurrentPassword(event.target.value)}
              autoComplete="current-password"
            />
            <TextField
              id="transfer-mfa"
              label={t("lifecycle.mfaOptional")}
              value={mfaCode}
              onChange={(event) => setMfaCode(event.target.value)}
              autoComplete="one-time-code"
            />
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </form>
        </Dialog>
      )}

      {deleteOpen && (
        <Dialog
          title={t("lifecycle.deleteTitle")}
          onClose={() => !busy && setDeleteOpen(false)}
          closeOnBackdrop={!busy}
          footer={
            <>
              <Button
                variant="secondary"
                disabled={busy}
                onClick={() => setDeleteOpen(false)}
              >
                {t("foundation.cancel")}
              </Button>
              <Button
                type="submit"
                form="organization-delete-form"
                variant="danger"
                loading={busy}
                disabled={
                  !currentPassword ||
                  confirmation !== business.name ||
                  preview === null
                }
              >
                {t("lifecycle.scheduleDeletion")}
              </Button>
            </>
          }
        >
          <form
            id="organization-delete-form"
            className="settings-dialog-stack"
            onSubmit={scheduleDeletion}
          >
            <Alert tone="danger">{t("lifecycle.deleteWarning")}</Alert>
            {preview && (
              <div className="settings-lifecycle-preview">
                <span>{t("lifecycle.preview.members", { count: preview.members })}</span>
                <span>{t("lifecycle.preview.devices", { count: preview.devices })}</span>
                <span>{t("lifecycle.preview.activity", { count: preview.activity })}</span>
                <span>{t("lifecycle.preview.browser", { count: preview.browser })}</span>
                <span>{t("lifecycle.preview.keystrokes", { count: preview.keystrokes })}</span>
                <span>
                  {t("lifecycle.preview.screenshots", {
                    count: preview.screenshots,
                    size: formatBytes(preview.screenshot_bytes),
                  })}
                </span>
              </div>
            )}
            <div className="settings-lifecycle-backup">
              <div>
                <strong>{t("lifecycle.backupTitle")}</strong>
                <p>{t("lifecycle.backupHelp")}</p>
              </div>
              <Button
                variant="secondary"
                size="sm"
                loading={exporting}
                onClick={requestBackup}
              >
                {t("lifecycle.createBackup")}
              </Button>
            </div>
            <TextField
              id="delete-confirmation"
              label={t("lifecycle.typeName", { name: business.name })}
              value={confirmation}
              onChange={(event) => setConfirmation(event.target.value)}
            />
            <TextField
              id="delete-password"
              label={t("accountSecurity.currentPassword")}
              type="password"
              value={currentPassword}
              onChange={(event) => setCurrentPassword(event.target.value)}
              autoComplete="current-password"
            />
            <TextField
              id="delete-mfa"
              label={t("lifecycle.mfaOptional")}
              value={mfaCode}
              onChange={(event) => setMfaCode(event.target.value)}
              autoComplete="one-time-code"
            />
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </form>
        </Dialog>
      )}
    </>
  );
}
