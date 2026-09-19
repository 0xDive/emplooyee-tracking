import { useMemo, useState } from "react";
import { useTranslation } from "react-i18next";
import { createEnrollmentToken, type EnrollmentTokenResponse } from "../../api/enrollment";
import type { Employee } from "../../api/types";
import { Alert, Button, Dialog, SelectMenu, TextField } from "../ds";

const INSTALLER_SCRIPT_URL =
  "https://github.com/0xDive/actilens/releases/latest/download/install-windows-agent.ps1";

function psQuote(value: string) {
  return value.replace(/'/g, "''");
}

async function copyText(value: string) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(value);
    return;
  }

  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  textarea.style.pointerEvents = "none";
  document.body.appendChild(textarea);
  textarea.select();
  textarea.setSelectionRange(0, value.length);
  const copied = document.execCommand("copy");
  textarea.remove();
  if (!copied) throw new Error("copy failed");
}

export function EnrollmentTokenControl({
  employee,
  businessId,
  canChange,
  triggerVariant = "button",
  triggerLabel,
  onDialogClose,
}: {
  employee: Employee;
  businessId: string;
  canChange: boolean;
  triggerVariant?: "button" | "menu-item";
  triggerLabel?: string;
  onDialogClose?: () => void;
}) {
  const { t } = useTranslation("dashboard");
  const [open, setOpen] = useState(false);
  const [busy, setBusy] = useState(false);
  const [hours, setHours] = useState(24);
  const [grant, setGrant] = useState<EnrollmentTokenResponse | null>(null);
  const [copied, setCopied] = useState<"token" | "powershell" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const defaultBackendUrl = typeof window === "undefined" ? "" : window.location.origin;
  const [serverUrl, setServerUrl] = useState(defaultBackendUrl);

  const powershell = useMemo(() => {
    if (!grant) return "";
    const backendUrl = serverUrl.trim().replace(/\/+$/, "");
    const path = "$env:TEMP\\install-actilens.ps1";
    return [
      `$p=${path}`,
      `Invoke-WebRequest -UseBasicParsing -Uri '${psQuote(INSTALLER_SCRIPT_URL)}' -OutFile $p`,
      `& powershell.exe -NoProfile -ExecutionPolicy Bypass -File $p -ServerUrl '${psQuote(backendUrl)}' -EnrollmentToken '${psQuote(grant.token)}'`,
      "Remove-Item -LiteralPath $p -Force -ErrorAction SilentlyContinue",
    ].join("; ");
  }, [grant, serverUrl]);

  if (!canChange || !employee.active) return null;

  function close() {
    if (busy) return;
    setOpen(false);
    onDialogClose?.();
  }

  function start() {
    setOpen(true);
    setGrant(null);
    setError(null);
    setCopied(null);
    setServerUrl(defaultBackendUrl);
  }

  async function generate() {
    setBusy(true);
    setError(null);
    setCopied(null);
    try {
      setGrant(await createEnrollmentToken(businessId, employee.id, hours));
    } catch {
      setError(t("employees.enrollment.failed"));
    } finally {
      setBusy(false);
    }
  }

  async function copy(kind: "token" | "powershell", value: string) {
    try {
      await copyText(value);
      setCopied(kind);
      window.setTimeout(() => setCopied(null), 1600);
    } catch {
      setError(t("employees.enrollment.copyFailed"));
    }
  }

  const label = triggerLabel ?? t("employees.actions.enrollment");
  const trigger =
    triggerVariant === "menu-item" ? (
      <button type="button" className="ds-menu__item" onClick={start}>
        {label}
      </button>
    ) : (
      <Button variant="secondary" size="sm" onClick={start}>
        {label}
      </Button>
    );

  return (
    <>
      {trigger}

      {open && (
        <Dialog
          title={t("employees.enrollment.title", { name: employee.display_name })}
          size="complex"
          onClose={close}
          closeOnBackdrop={!busy}
          footer={
            !grant ? (
              <>
                <Button variant="secondary" disabled={busy} onClick={close}>
                  {t("employees.enrollment.cancel")}
                </Button>
                <Button variant="primary" loading={busy} onClick={generate}>
                  {busy
                    ? t("employees.enrollment.generating")
                    : t("employees.enrollment.generate")}
                </Button>
              </>
            ) : (
              <>
                <Button variant="secondary" onClick={() => setGrant(null)}>
                  {t("employees.enrollment.regenerate")}
                </Button>
                <Button variant="primary" onClick={close}>
                  {t("employees.enrollment.done")}
                </Button>
              </>
            )
          }
        >
          <div className="employees-dialog-stack">
            <Alert tone="info">{t("employees.enrollment.description")}</Alert>

            {!grant && (
              <SelectMenu
                id="enrollment-ttl"
                label={t("employees.enrollment.expires")}
                value={String(hours)}
                options={[
                  { value: "1", label: t("employees.enrollment.ttl1") },
                  { value: "24", label: t("employees.enrollment.ttl24") },
                  { value: "72", label: t("employees.enrollment.ttl72") },
                  { value: "168", label: t("employees.enrollment.ttl168") },
                ]}
                onChange={(value) => setHours(Number(value))}
              />
            )}

            {grant && (
              <>
                <Alert tone="success">
                  {t("employees.enrollment.created", {
                    expires: new Date(grant.expires_at).toLocaleString(),
                  })}
                </Alert>

                <div className="employees-code-section">
                  <div className="employees-code-label">
                    {t("employees.enrollment.tokenLabel")}
                  </div>
                  <code className="employees-code-block">{grant.token}</code>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => copy("token", grant.token)}
                  >
                    {copied === "token"
                      ? t("employees.enrollment.copied")
                      : t("employees.enrollment.copyToken")}
                  </Button>
                </div>

                <TextField
                  id="enrollment-server-url"
                  label={t("employees.enrollment.serverUrlLabel")}
                  description={t("employees.enrollment.serverUrlHelp")}
                  value={serverUrl}
                  onChange={(event) => setServerUrl(event.target.value)}
                  placeholder="http://192.168.0.249:8081"
                  spellCheck={false}
                  autoCapitalize="none"
                />

                <div className="employees-code-section">
                  <div className="employees-code-label">
                    {t("employees.enrollment.powershellLabel")}
                  </div>
                  <code className="employees-code-block">{powershell}</code>
                  <Button
                    variant="secondary"
                    size="sm"
                    onClick={() => copy("powershell", powershell)}
                  >
                    {copied === "powershell"
                      ? t("employees.enrollment.copied")
                      : t("employees.enrollment.copyPowershell")}
                  </Button>
                </div>

                <p className="employees-dialog-note">
                  {t("employees.enrollment.oneTime")}
                </p>
              </>
            )}

            {error && <Alert tone="danger">{error}</Alert>}
          </div>
        </Dialog>
      )}
    </>
  );
}
