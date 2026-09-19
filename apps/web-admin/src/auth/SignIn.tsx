import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { completeMFA, login } from "../api/endpoints";
import { ApiError } from "../api/types";
import { Alert, Button, TextField } from "../components/ds";
import { useAuth } from "./AuthContext";
import { AuthLayout } from "./AuthLayout";

const DOWNLOAD_URL = import.meta.env.VITE_DOWNLOAD_URL || "/";

function challengeFromError(error: ApiError): string | null {
  if (error.code !== "mfa_required" || !error.details || typeof error.details !== "object") {
    return null;
  }
  const value = (error.details as Record<string, unknown>).challenge_token;
  return typeof value === "string" ? value : null;
}

export function SignIn() {
  const navigate = useNavigate();
  const { t } = useTranslation("auth");
  const { setSession } = useAuth();
  const [identifier, setIdentifier] = useState("");
  const [password, setPassword] = useState("");
  const [mfaChallenge, setMfaChallenge] = useState<string | null>(null);
  const [mfaCode, setMfaCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  function finish(user: Parameters<typeof setSession>[0]) {
    setSession(user);
    navigate("/", { replace: true });
  }

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError(null);
    setBusy(true);

    try {
      const response = await login(identifier.trim(), password);
      finish(response.user);
    } catch (err) {
      if (err instanceof ApiError) {
        const challenge = challengeFromError(err);
        if (challenge) {
          setMfaChallenge(challenge);
          setMfaCode("");
          return;
        }
        setError(err.message);
      } else {
        setError(t("errors.network"));
      }
    } finally {
      setBusy(false);
    }
  }

  async function submitMFA(event: FormEvent) {
    event.preventDefault();
    if (!mfaChallenge) return;
    setError(null);
    setBusy(true);
    try {
      const response = await completeMFA(mfaChallenge, mfaCode.trim());
      finish(response.user);
    } catch (err) {
      setError(
        err instanceof ApiError && err.code === "mfa_required"
          ? t("mfa.invalid")
          : err instanceof ApiError
            ? err.message
            : t("errors.network"),
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthLayout>
      <section className="auth-v1__card">
        <h1 className="auth-v1__title">
          {mfaChallenge ? t("mfa.title") : t("signIn.title")}
        </h1>
        <p className="auth-v1__subtitle">
          {mfaChallenge ? t("mfa.subtitle") : t("signIn.subtitle")}
        </p>

        {error && (
          <div className="auth-v1__error">
            <Alert tone="danger">{error}</Alert>
          </div>
        )}

        {mfaChallenge ? (
          <form className="auth-v1__form" onSubmit={submitMFA}>
            <TextField
              id="sign-in-mfa"
              label={t("mfa.code")}
              type="text"
              inputMode="text"
              value={mfaCode}
              onChange={(event) => setMfaCode(event.target.value)}
              required
              autoComplete="one-time-code"
              autoCapitalize="characters"
              spellCheck={false}
              placeholder={t("mfa.placeholder")}
              autoFocus
            />
            <div className="auth-v1__submit">
              <Button
                type="submit"
                variant="primary"
                size="lg"
                loading={busy}
                disabled={!mfaCode.trim()}
              >
                {busy ? t("mfa.verifying") : t("mfa.verify")}
              </Button>
            </div>
            <div className="auth-v1__footer">
              <button
                type="button"
                className="auth-v1__link auth-v1__link-button"
                onClick={() => {
                  setMfaChallenge(null);
                  setMfaCode("");
                  setError(null);
                }}
              >
                {t("mfa.back")}
              </button>
              <span>{t("mfa.recoveryHint")}</span>
            </div>
          </form>
        ) : (
          <>
            <form className="auth-v1__form" onSubmit={submit}>
              <TextField
                id="sign-in-identifier"
                label={t("signIn.identifier")}
                type="text"
                value={identifier}
                onChange={(event) => setIdentifier(event.target.value)}
                required
                autoComplete="username"
                autoCapitalize="none"
                spellCheck={false}
                placeholder={t("signIn.identifierPlaceholder")}
                autoFocus
              />

              <TextField
                id="sign-in-password"
                label={t("signIn.password")}
                type="password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                required
                autoComplete="current-password"
                placeholder="••••••••"
              />

              <div className="auth-v1__submit">
                <Button
                  type="submit"
                  variant="primary"
                  size="lg"
                  loading={busy}
                  disabled={!identifier.trim() || !password}
                >
                  {busy ? t("signIn.submitting") : t("signIn.submit")}
                </Button>
              </div>
            </form>

            <div className="auth-v1__footer">
              <span>
                {t("signIn.newHere")}{" "}
                <Link className="auth-v1__link" to="/signup">
                  {t("signIn.createAccount")}
                </Link>
              </span>
              <a className="auth-v1__link" href={DOWNLOAD_URL}>
                {t("signIn.download")}
              </a>
            </div>
          </>
        )}
      </section>
    </AuthLayout>
  );
}
