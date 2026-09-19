import { useState } from "react";
import { call as invoke } from "../api";
import { openUrl } from "@tauri-apps/plugin-opener";
import { useTranslation } from "react-i18next";
import { BrandMark } from "../ui";
import { AuthTitleBar } from "../components/AuthTitleBar";
import { LanguageSwitcher } from "../components/LanguageSwitcher";
import { localizedApiError, stableApiErrorCode } from "../errorText";

export type Session = {
  email: string;
  business_id?: string | null;
};

type OrganizationChoice = {
  business_id: string;
  business_name: string;
  kind: string;
  role: string;
  status: string;
};

type LoginFlowResult = {
  status: "authenticated" | "mfa_required" | "organization_required";
  session?: Session;
  challenge_token?: string;
  business_id?: string | null;
  organizations?: OrganizationChoice[];
};

const AtSignIcon = () => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"
    strokeLinecap="round" strokeLinejoin="round" aria-hidden>
    <circle cx="12" cy="12" r="4" />
    <path d="M16 8v5a3 3 0 0 0 6 0v-1a10 10 0 1 0-3.92 7.94" />
  </svg>
);
const LockIcon = () => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"
    strokeLinecap="round" strokeLinejoin="round" aria-hidden>
    <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
    <path d="M7 11V7a5 5 0 0 1 10 0v4" />
  </svg>
);
const AlertIcon = () => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"
    strokeLinecap="round" strokeLinejoin="round" aria-hidden>
    <circle cx="12" cy="12" r="10" />
    <line x1="12" y1="8" x2="12" y2="12" />
    <line x1="12" y1="16" x2="12.01" y2="16" />
  </svg>
);
const BackIcon = () => (
  <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"
    strokeLinecap="round" strokeLinejoin="round" aria-hidden>
    <path d="m12 19-7-7 7-7" />
    <path d="M19 12H5" />
  </svg>
);

export function Login({
  onLoggedIn,
  onBack,
}: {
  onLoggedIn: (s: Session) => void;
  onBack?: () => void;
}) {
  const { t } = useTranslation("auth");
  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const [mfaChallenge, setMfaChallenge] = useState<string | null>(null);
  const [mfaBusinessId, setMfaBusinessId] = useState<string | null>(null);
  const [mfaCode, setMfaCode] = useState("");
  const [organizations, setOrganizations] = useState<OrganizationChoice[]>([]);

  async function openSignup() {
    try {
      const url = await invoke<string>("signup_url");
      await openUrl(url);
    } catch {
      // User can still sign in.
    }
  }

  function handleResult(result: LoginFlowResult) {
    if (result.status === "authenticated" && result.session) {
      onLoggedIn(result.session);
      return;
    }
    if (result.status === "mfa_required" && result.challenge_token) {
      setOrganizations([]);
      setMfaChallenge(result.challenge_token);
      setMfaBusinessId(result.business_id || null);
      setMfaCode("");
      return;
    }
    if (result.status === "organization_required") {
      setMfaChallenge(null);
      setOrganizations(result.organizations || []);
      return;
    }
    setError(t("login.unexpected"));
  }

  async function runPasswordLogin(businessId: string | null) {
    setError(null);
    setBusy(true);
    try {
      const result = await invoke<LoginFlowResult>("login", {
        email: identifier.trim(),
        password,
        businessId,
      });
      handleResult(result);
    } catch (err) {
      setError(localizedApiError(err, t("login.unexpected")));
    } finally {
      setBusy(false);
    }
  }

  async function signIn(event: React.FormEvent) {
    event.preventDefault();
    if (busy) return;
    await runPasswordLogin(null);
  }

  async function completeMFA(event: React.FormEvent) {
    event.preventDefault();
    if (busy || !mfaChallenge || !mfaCode.trim()) return;
    setError(null);
    setBusy(true);
    try {
      const result = await invoke<LoginFlowResult>("complete_mfa_login", {
        challengeToken: mfaChallenge,
        code: mfaCode.trim(),
        email: identifier.trim(),
        businessId: mfaBusinessId,
      });
      handleResult(result);
    } catch (err) {
      setError(
        stableApiErrorCode(err) === "mfa_required"
          ? t("mfa.invalid")
          : localizedApiError(err, t("login.unexpected")),
      );
    } finally {
      setBusy(false);
    }
  }

  function resetToPassword() {
    setError(null);
    setMfaChallenge(null);
    setMfaBusinessId(null);
    setMfaCode("");
    setOrganizations([]);
  }

  const choosingOrganization = organizations.length > 0;
  const secondFactor = !!mfaChallenge;

  return (
    <div className="login welcome">
      <AuthTitleBar />
      {(onBack || secondFactor || choosingOrganization) && (
        <button
          type="button"
          className="welcome-back"
          onClick={secondFactor || choosingOrganization ? resetToPassword : onBack}
        >
          <BackIcon />
          {t("login.back")}
        </button>
      )}
      <div className="welcome-lang">
        <LanguageSwitcher compact />
      </div>

      <BrandMark />

      {choosingOrganization ? (
        <div className="login-card">
          <h1 className="login-title">{t("organization.title")}</h1>
          <p className="login-sub">{t("organization.subtitle")}</p>

          {error && (
            <div className="auth-err" role="alert">
              <AlertIcon />
              {error}
            </div>
          )}

          <div className="persona-grid login-org-list">
            {organizations.map((organization) => (
              <button
                key={organization.business_id}
                type="button"
                className="persona-card"
                disabled={busy}
                onClick={() => runPasswordLogin(organization.business_id)}
              >
                <span className="p-copy">
                  <span className="p-title">{organization.business_name}</span>
                  <span className="p-desc">
                    {t(`organization.kind.${organization.kind}`)} ·{" "}
                    {t(`organization.status.${organization.status}`)}
                  </span>
                </span>
                <span className="p-action">→</span>
              </button>
            ))}
          </div>
        </div>
      ) : secondFactor ? (
        <form className="login-card" onSubmit={completeMFA}>
          <h1 className="login-title">{t("mfa.title")}</h1>
          <p className="login-sub">{t("mfa.subtitle")}</p>

          <div className="auth-form">
            {error && (
              <div className="auth-err" role="alert">
                <AlertIcon />
                {error}
              </div>
            )}

            <label className="auth-field">
              <span className="auth-field-lbl">{t("mfa.code")}</span>
              <div className="auth-input">
                <span className="auth-input-ic"><LockIcon /></span>
                <input
                  type="text"
                  inputMode="text"
                  autoComplete="one-time-code"
                  autoCapitalize="characters"
                  spellCheck={false}
                  value={mfaCode}
                  onChange={(event) => setMfaCode(event.target.value)}
                  placeholder={t("mfa.placeholder")}
                  autoFocus
                />
              </div>
            </label>

            <p className="login-mfa-hint">{t("mfa.recoveryHint")}</p>

            <button className="auth-btn" type="submit" disabled={busy || !mfaCode.trim()}>
              {busy ? t("mfa.verifying") : t("mfa.verify")}
            </button>
          </div>
        </form>
      ) : (
        <form className="login-card" onSubmit={signIn}>
          <h1 className="login-title">{t("login.title")}</h1>
          <p className="login-sub">{t("login.subtitle")}</p>

          <div className="auth-form">
            {error && (
              <div className="auth-err" role="alert">
                <AlertIcon />
                {error}
              </div>
            )}

            <label className="auth-field">
              <span className="auth-field-lbl">{t("login.identifier")}</span>
              <div className="auth-input">
                <span className="auth-input-ic"><AtSignIcon /></span>
                <input
                  type="text"
                  autoComplete="username"
                  value={identifier}
                  onChange={(event) => setIdentifier(event.target.value)}
                  placeholder="you@example.com"
                  autoFocus
                />
              </div>
            </label>

            <label className="auth-field">
              <span className="auth-field-lbl">{t("login.password")}</span>
              <div className="auth-input">
                <span className="auth-input-ic"><LockIcon /></span>
                <input
                  type="password"
                  autoComplete="current-password"
                  value={password}
                  onChange={(event) => setPassword(event.target.value)}
                  placeholder="••••••••"
                />
              </div>
            </label>

            <div className="auth-forgot-row">
              <button type="button" className="auth-signup" onClick={openSignup}>
                {t("login.signupLink")}
                <svg
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2.2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                  aria-hidden
                >
                  <path d="M5 12h14" />
                  <path d="m12 5 7 7-7 7" />
                </svg>
              </button>
            </div>

            <button
              className="auth-btn"
              type="submit"
              disabled={busy || !identifier.trim() || !password}
            >
              {busy ? t("login.submitting") : t("login.submit")}
            </button>
          </div>
        </form>
      )}
    </div>
  );
}
