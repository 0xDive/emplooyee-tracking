import { useCallback, useEffect, useState, type FormEvent } from "react";
import { useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import {
  beginMFASetup,
  changeOwnPassword,
  confirmMFASetup,
  disableMFA,
  getAccount,
  listAuthSessions,
  regenerateRecoveryCodes,
  revokeAuthSession,
  revokeOtherAuthSessions,
  updateOwnDisplayName,
  updateOwnLoginIdentifiers,
} from "../api/endpoints";
import type {
  AccountResponse,
  AuthSession,
  MFASetupResponse,
} from "../api/types";
import { useAuth } from "../auth/AuthContext";
import {
  Alert,
  Badge,
  Button,
  Card,
  Dialog,
  EmptyState,
  Skeleton,
  TextField,
} from "../components/ds";
import { useToast } from "../components/ToastProvider";
import { useBusinesses } from "../useBusinesses";
import "../theme/account-v1.css";

function formatDate(value: string): string {
  return new Date(value).toLocaleString();
}

async function copyText(value: string) {
  if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(value);
    return;
  }
  const input = document.createElement("textarea");
  input.value = value;
  input.style.position = "fixed";
  input.style.opacity = "0";
  document.body.appendChild(input);
  input.select();
  const ok = document.execCommand("copy");
  input.remove();
  if (!ok) throw new Error("copy failed");
}

export function Account() {
  const { t } = useTranslation("settings");
  const navigate = useNavigate();
  const { logout, setSession } = useAuth();
  const { businesses } = useBusinesses();
  const { pushToast } = useToast();

  const [account, setAccount] = useState<AccountResponse | null>(null);
  const [sessions, setSessions] = useState<AuthSession[]>([]);
  const [loading, setLoading] = useState(true);
  const [sessionsLoading, setSessionsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  const [nameOpen, setNameOpen] = useState(false);
  const [identityOpen, setIdentityOpen] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const [mfaSetupOpen, setMfaSetupOpen] = useState(false);
  const [mfaDisableOpen, setMfaDisableOpen] = useState(false);
  const [recoveryOpen, setRecoveryOpen] = useState(false);

  const [busy, setBusy] = useState(false);
  const [dialogError, setDialogError] = useState<string | null>(null);

  const [displayName, setDisplayName] = useState("");
  const [email, setEmail] = useState("");
  const [username, setUsername] = useState("");
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [newPasswordConfirm, setNewPasswordConfirm] = useState("");

  const [mfaSetup, setMfaSetup] = useState<MFASetupResponse | null>(null);
  const [mfaCode, setMfaCode] = useState("");
  const [recoveryCodes, setRecoveryCodes] = useState<string[] | null>(null);

  const isOwner = businesses.some((business) => business.role === "owner");

  const loadAccount = useCallback(async () => {
    setError(null);
    try {
      const response = await getAccount();
      setAccount(response);
      setDisplayName(response.user.display_name);
      setEmail(response.user.email || "");
      setUsername(response.user.username || "");
    } catch {
      setError(t("accountSecurity.loadFailed"));
    } finally {
      setLoading(false);
    }
  }, [t]);

  const loadSessions = useCallback(async () => {
    setSessionsLoading(true);
    try {
      const response = await listAuthSessions();
      setSessions(response.sessions);
    } catch {
      setError(t("accountSecurity.sessions.loadFailed"));
    } finally {
      setSessionsLoading(false);
    }
  }, [t]);

  useEffect(() => {
    loadAccount();
    loadSessions();
  }, [loadAccount, loadSessions]);

  function resetDialogState() {
    setDialogError(null);
    setCurrentPassword("");
    setNewPassword("");
    setNewPasswordConfirm("");
    setMfaCode("");
  }

  function forceReauth() {
    logout();
    navigate("/login", { replace: true });
  }

  async function saveDisplayName(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setDialogError(null);
    try {
      const response = await updateOwnDisplayName(displayName.trim());
      setSession(response.user);
      setAccount((current) => (current ? { ...current, user: response.user } : current));
      setNameOpen(false);
      pushToast({ title: t("accountSecurity.profile.saved"), tone: "success" });
    } catch {
      setDialogError(t("accountSecurity.saveFailed"));
    } finally {
      setBusy(false);
    }
  }

  async function saveIdentifiers(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setDialogError(null);
    try {
      await updateOwnLoginIdentifiers({
        current_password: currentPassword,
        email: email.trim(),
        username: username.trim(),
      });
      forceReauth();
    } catch {
      setDialogError(t("accountSecurity.identity.failed"));
      setBusy(false);
    }
  }

  async function savePassword(event: FormEvent) {
    event.preventDefault();
    if (newPassword !== newPasswordConfirm) {
      setDialogError(t("accountSecurity.password.mismatch"));
      return;
    }
    setBusy(true);
    setDialogError(null);
    try {
      await changeOwnPassword(currentPassword, newPassword);
      forceReauth();
    } catch {
      setDialogError(t("accountSecurity.password.failed"));
      setBusy(false);
    }
  }

  async function startMFA(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setDialogError(null);
    try {
      setMfaSetup(await beginMFASetup(currentPassword));
      setCurrentPassword("");
      setMfaCode("");
    } catch {
      setDialogError(t("accountSecurity.mfa.setupFailed"));
    } finally {
      setBusy(false);
    }
  }

  async function confirmMFA(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setDialogError(null);
    try {
      const response = await confirmMFASetup(mfaCode);
      setRecoveryCodes(response.recovery_codes);
      setMfaSetup(null);
      setMfaCode("");
      await loadAccount();
    } catch {
      setDialogError(t("accountSecurity.mfa.invalid"));
    } finally {
      setBusy(false);
    }
  }

  async function runDisableMFA(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setDialogError(null);
    try {
      await disableMFA(currentPassword, mfaCode);
      forceReauth();
    } catch {
      setDialogError(t("accountSecurity.mfa.disableFailed"));
      setBusy(false);
    }
  }

  async function runRegenerateRecovery(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setDialogError(null);
    try {
      const response = await regenerateRecoveryCodes(currentPassword, mfaCode);
      setRecoveryCodes(response.recovery_codes);
      setRecoveryOpen(false);
      setCurrentPassword("");
      setMfaCode("");
    } catch {
      setDialogError(t("accountSecurity.mfa.regenerateFailed"));
    } finally {
      setBusy(false);
    }
  }

  if (loading) {
    return (
      <div className="account-v1">
        <Skeleton width="100%" height={420} />
      </div>
    );
  }

  if (!account) {
    return (
      <div className="account-v1">
        <Alert tone="danger">{error || t("accountSecurity.loadFailed")}</Alert>
      </div>
    );
  }

  return (
    <div className="account-v1">
      {error && <div className="account-v1__alert"><Alert tone="danger">{error}</Alert></div>}

      <div className="account-v1__stack">
        <section>
          <div className="settings-section__head">
            <h2 className="settings-section__title">{t("accountSecurity.profile.title")}</h2>
            <p className="settings-section__description">{t("accountSecurity.profile.description")}</p>
          </div>
          <Card className="settings-card">
            <div className="settings-row">
              <div className="settings-row__copy">
                <div className="settings-row__title">{t("account.displayName")}</div>
              </div>
              <div className="settings-row__control">
                <div className="settings-inline">
                  <span>{account.user.display_name}</span>
                  {isOwner && (
                    <Button variant="secondary" size="sm" onClick={() => { resetDialogState(); setNameOpen(true); }}>
                      {t("accountSecurity.edit")}
                    </Button>
                  )}
                </div>
              </div>
            </div>
            <div className="settings-row">
              <div className="settings-row__copy">
                <div className="settings-row__title">{t("accountSecurity.identity.title")}</div>
                <div className="settings-row__description">{t("accountSecurity.identity.description")}</div>
              </div>
              <div className="settings-row__control">
                <div className="account-v1__identity">
                  <span>{account.user.email || "—"}</span>
                  <span>{account.user.username || "—"}</span>
                  <Button variant="secondary" size="sm" onClick={() => { resetDialogState(); setIdentityOpen(true); }}>
                    {t("accountSecurity.identity.change")}
                  </Button>
                </div>
              </div>
            </div>
          </Card>
        </section>

        <section>
          <div className="settings-section__head">
            <h2 className="settings-section__title">{t("accountSecurity.password.title")}</h2>
            <p className="settings-section__description">{t("accountSecurity.password.description")}</p>
          </div>
          <Card className="settings-card">
            <div className="settings-row">
              <div className="settings-row__copy">
                <div className="settings-row__title">{t("accountSecurity.password.change")}</div>
                <div className="settings-row__description">{t("accountSecurity.password.logoutWarning")}</div>
              </div>
              <div className="settings-row__control">
                <Button variant="secondary" onClick={() => { resetDialogState(); setPasswordOpen(true); }}>
                  {t("accountSecurity.password.change")}
                </Button>
              </div>
            </div>
          </Card>
        </section>

        <section>
          <div className="settings-section__head">
            <h2 className="settings-section__title">{t("accountSecurity.mfa.title")}</h2>
            <p className="settings-section__description">{t("accountSecurity.mfa.description")}</p>
          </div>
          <Card className="settings-card">
            <div className="settings-row">
              <div className="settings-row__copy">
                <div className="settings-row__title">{t("accountSecurity.mfa.status")}</div>
                <div className="settings-row__description">
                  {account.mfa.enabled ? t("accountSecurity.mfa.enabledHelp") : t("accountSecurity.mfa.disabledHelp")}
                </div>
              </div>
              <div className="settings-row__control">
                <div className="settings-inline">
                  <Badge tone={account.mfa.enabled ? "success" : "neutral"}>
                    {account.mfa.enabled ? t("accountSecurity.mfa.enabled") : t("accountSecurity.mfa.disabled")}
                  </Badge>
                  {account.mfa.enabled ? (
                    <>
                      <Button variant="secondary" size="sm" onClick={() => { resetDialogState(); setRecoveryOpen(true); }}>
                        {t("accountSecurity.mfa.regenerate")}
                      </Button>
                      <Button variant="danger-ghost" size="sm" onClick={() => { resetDialogState(); setMfaDisableOpen(true); }}>
                        {t("accountSecurity.mfa.disable")}
                      </Button>
                    </>
                  ) : (
                    <Button variant="primary" size="sm" onClick={() => { resetDialogState(); setMfaSetup(null); setMfaSetupOpen(true); }}>
                      {t("accountSecurity.mfa.enable")}
                    </Button>
                  )}
                </div>
              </div>
            </div>
          </Card>
        </section>

        <section>
          <div className="settings-section__head">
            <h2 className="settings-section__title">{t("accountSecurity.sessions.title")}</h2>
            <p className="settings-section__description">{t("accountSecurity.sessions.description")}</p>
          </div>
          <Card>
            <div className="account-v1__sessions-head">
              <span>{t("accountSecurity.sessions.count", { count: sessions.filter((item) => !item.revoked_at).length })}</span>
              <Button
                variant="secondary"
                size="sm"
                onClick={async () => {
                  await revokeOtherAuthSessions();
                  await loadSessions();
                  pushToast({ title: t("accountSecurity.sessions.revokedOthers"), tone: "success" });
                }}
              >
                {t("accountSecurity.sessions.revokeOthers")}
              </Button>
            </div>

            {sessionsLoading ? (
              <div className="account-v1__session-list">
                <Skeleton height={66} />
                <Skeleton height={66} />
              </div>
            ) : sessions.length === 0 ? (
              <EmptyState title={t("accountSecurity.sessions.empty")} />
            ) : (
              <div className="account-v1__session-list">
                {sessions.map((session) => (
                  <div className="account-v1__session" key={session.id}>
                    <div className="account-v1__session-copy">
                      <div className="account-v1__session-title">
                        {session.client_type === "desktop"
                          ? t("accountSecurity.sessions.desktop")
                          : t("accountSecurity.sessions.web")}
                        {session.current && <Badge tone="brand">{t("accountSecurity.sessions.current")}</Badge>}
                        {session.revoked_at && <Badge tone="neutral">{t("accountSecurity.sessions.revoked")}</Badge>}
                      </div>
                      <div className="account-v1__session-label">{session.client_label || "—"}</div>
                      <div className="account-v1__session-meta">
                        {t("accountSecurity.sessions.created", { value: formatDate(session.created_at) })}
                        {" · "}
                        {t("accountSecurity.sessions.lastUsed", { value: formatDate(session.last_used_at) })}
                      </div>
                    </div>
                    {!session.revoked_at && (
                      <Button
                        variant={session.current ? "danger-ghost" : "ghost"}
                        size="sm"
                        onClick={async () => {
                          const result = await revokeAuthSession(session.id);
                          if (result.reauth_required) {
                            forceReauth();
                            return;
                          }
                          await loadSessions();
                        }}
                      >
                        {t("accountSecurity.sessions.revoke")}
                      </Button>
                    )}
                  </div>
                ))}
              </div>
            )}
          </Card>
        </section>
      </div>

      {nameOpen && (
        <Dialog
          title={t("accountSecurity.profile.editTitle")}
          size="confirm"
          onClose={() => !busy && setNameOpen(false)}
          footer={
            <>
              <Button variant="secondary" disabled={busy} onClick={() => setNameOpen(false)}>
                {t("foundation.cancel")}
              </Button>
              <Button type="submit" form="account-name-form" variant="primary" loading={busy}>
                {t("foundation.save")}
              </Button>
            </>
          }
        >
          <form id="account-name-form" className="account-v1__form" onSubmit={saveDisplayName}>
            <TextField
              id="account-display-name"
              label={t("account.displayName")}
              value={displayName}
              maxLength={120}
              onChange={(event) => setDisplayName(event.target.value)}
            />
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </form>
        </Dialog>
      )}

      {identityOpen && (
        <Dialog
          title={t("accountSecurity.identity.change")}
          onClose={() => !busy && setIdentityOpen(false)}
          closeOnBackdrop={!busy}
          footer={
            <>
              <Button variant="secondary" disabled={busy} onClick={() => setIdentityOpen(false)}>
                {t("foundation.cancel")}
              </Button>
              <Button type="submit" form="account-identity-form" variant="primary" loading={busy}>
                {t("foundation.save")}
              </Button>
            </>
          }
        >
          <form id="account-identity-form" className="account-v1__form" onSubmit={saveIdentifiers}>
            <Alert tone="warning">{t("accountSecurity.identity.logoutWarning")}</Alert>
            <TextField id="account-email" label={t("accountSecurity.identity.email")} type="email" value={email} onChange={(event) => setEmail(event.target.value)} />
            <TextField id="account-username" label={t("accountSecurity.identity.username")} value={username} onChange={(event) => setUsername(event.target.value)} autoCapitalize="none" spellCheck={false} />
            <TextField id="account-identity-password" label={t("accountSecurity.currentPassword")} type="password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} autoComplete="current-password" />
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </form>
        </Dialog>
      )}

      {passwordOpen && (
        <Dialog
          title={t("accountSecurity.password.change")}
          onClose={() => !busy && setPasswordOpen(false)}
          closeOnBackdrop={!busy}
          footer={
            <>
              <Button variant="secondary" disabled={busy} onClick={() => setPasswordOpen(false)}>
                {t("foundation.cancel")}
              </Button>
              <Button
                type="submit"
                form="account-password-form"
                variant="primary"
                loading={busy}
                disabled={newPassword.length < 8 || newPassword !== newPasswordConfirm}
              >
                {t("accountSecurity.password.change")}
              </Button>
            </>
          }
        >
          <form id="account-password-form" className="account-v1__form" onSubmit={savePassword}>
            <Alert tone="warning">{t("accountSecurity.password.logoutWarning")}</Alert>
            <TextField id="account-current-password" label={t("accountSecurity.currentPassword")} type="password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} autoComplete="current-password" />
            <TextField id="account-new-password" label={t("accountSecurity.password.new")} type="password" value={newPassword} onChange={(event) => setNewPassword(event.target.value)} autoComplete="new-password" />
            <TextField id="account-confirm-password" label={t("accountSecurity.password.confirm")} type="password" value={newPasswordConfirm} onChange={(event) => setNewPasswordConfirm(event.target.value)} autoComplete="new-password" />
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </form>
        </Dialog>
      )}

      {mfaSetupOpen && (
        <Dialog
          title={t("accountSecurity.mfa.enableTitle")}
          onClose={() => !busy && setMfaSetupOpen(false)}
          closeOnBackdrop={!busy}
          footer={
            !mfaSetup ? (
              <>
                <Button variant="secondary" disabled={busy} onClick={() => setMfaSetupOpen(false)}>
                  {t("foundation.cancel")}
                </Button>
                <Button type="submit" form="mfa-start-form" variant="primary" loading={busy}>
                  {t("accountSecurity.mfa.continue")}
                </Button>
              </>
            ) : (
              <>
                <Button variant="secondary" disabled={busy} onClick={() => setMfaSetupOpen(false)}>
                  {t("foundation.cancel")}
                </Button>
                <Button type="submit" form="mfa-confirm-form" variant="primary" loading={busy} disabled={!mfaCode.trim()}>
                  {t("accountSecurity.mfa.confirm")}
                </Button>
              </>
            )
          }
        >
          {!mfaSetup ? (
            <form id="mfa-start-form" className="account-v1__form" onSubmit={startMFA}>
              <TextField id="mfa-password" label={t("accountSecurity.currentPassword")} type="password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} autoComplete="current-password" />
              {dialogError && <Alert tone="danger">{dialogError}</Alert>}
            </form>
          ) : (
            <form id="mfa-confirm-form" className="account-v1__form" onSubmit={confirmMFA}>
              <Alert tone="info">{t("accountSecurity.mfa.manualIntro")}</Alert>
              <div className="account-v1__secret">
                <span>{t("accountSecurity.mfa.secret")}</span>
                <code>{mfaSetup.secret}</code>
                <Button variant="ghost" size="sm" onClick={() => copyText(mfaSetup.secret)}>
                  {t("accountSecurity.copy")}
                </Button>
              </div>
              <TextField id="mfa-confirm-code" label={t("accountSecurity.mfa.code")} value={mfaCode} onChange={(event) => setMfaCode(event.target.value)} autoComplete="one-time-code" inputMode="numeric" />
              {dialogError && <Alert tone="danger">{dialogError}</Alert>}
            </form>
          )}
        </Dialog>
      )}

      {recoveryCodes && (
        <Dialog
          title={t("accountSecurity.mfa.recoveryTitle")}
          onClose={() => setRecoveryCodes(null)}
          closeOnBackdrop={false}
          footer={
            <Button variant="primary" onClick={() => setRecoveryCodes(null)}>
              {t("accountSecurity.mfa.savedCodes")}
            </Button>
          }
        >
          <Alert tone="warning">{t("accountSecurity.mfa.recoveryWarning")}</Alert>
          <div className="account-v1__recovery-codes">
            {recoveryCodes.map((code) => <code key={code}>{code}</code>)}
          </div>
          <Button variant="secondary" onClick={() => copyText(recoveryCodes.join("\n"))}>
            {t("accountSecurity.mfa.copyAll")}
          </Button>
        </Dialog>
      )}

      {recoveryOpen && (
        <Dialog
          title={t("accountSecurity.mfa.regenerate")}
          onClose={() => !busy && setRecoveryOpen(false)}
          footer={
            <>
              <Button variant="secondary" disabled={busy} onClick={() => setRecoveryOpen(false)}>
                {t("foundation.cancel")}
              </Button>
              <Button type="submit" form="mfa-recovery-form" variant="primary" loading={busy}>
                {t("accountSecurity.mfa.regenerate")}
              </Button>
            </>
          }
        >
          <form id="mfa-recovery-form" className="account-v1__form" onSubmit={runRegenerateRecovery}>
            <Alert tone="warning">{t("accountSecurity.mfa.regenerateWarning")}</Alert>
            <TextField id="mfa-recovery-password" label={t("accountSecurity.currentPassword")} type="password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} />
            <TextField id="mfa-recovery-code" label={t("accountSecurity.mfa.code")} value={mfaCode} onChange={(event) => setMfaCode(event.target.value)} />
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </form>
        </Dialog>
      )}

      {mfaDisableOpen && (
        <Dialog
          title={t("accountSecurity.mfa.disable")}
          size="confirm"
          onClose={() => !busy && setMfaDisableOpen(false)}
          footer={
            <>
              <Button variant="secondary" disabled={busy} onClick={() => setMfaDisableOpen(false)}>
                {t("foundation.cancel")}
              </Button>
              <Button type="submit" form="mfa-disable-form" variant="danger" loading={busy}>
                {t("accountSecurity.mfa.disable")}
              </Button>
            </>
          }
        >
          <form id="mfa-disable-form" className="account-v1__form" onSubmit={runDisableMFA}>
            <Alert tone="warning">{t("accountSecurity.mfa.disableWarning")}</Alert>
            <TextField id="mfa-disable-password" label={t("accountSecurity.currentPassword")} type="password" value={currentPassword} onChange={(event) => setCurrentPassword(event.target.value)} />
            <TextField id="mfa-disable-code" label={t("accountSecurity.mfa.code")} value={mfaCode} onChange={(event) => setMfaCode(event.target.value)} />
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </form>
        </Dialog>
      )}
    </div>
  );
}
