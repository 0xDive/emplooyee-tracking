import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { LOCALES } from "../i18n";
import { SelectMenu } from "../components/ds";
import { useTheme, type ThemeMode } from "../theme/ThemeProvider";
import "../theme/auth-v1.css";

function BrandMark() {
  return (
    <span className="auth-v1__brand-mark" aria-hidden>
      <img src="/brand/mark.svg" alt="" />
    </span>
  );
}

export function AuthLayout({
  children,
  wide = false,
}: {
  children: ReactNode;
  wide?: boolean;
}) {
  const { t, i18n } = useTranslation();
  const { mode, setMode } = useTheme();
  const locale =
    LOCALES.find((item) => item.code === i18n.resolvedLanguage)?.code ?? "en";

  return (
    <div className="auth-v1">
      <div className="auth-v1__top">
        <SelectMenu
          id="auth-language"
          className="auth-v1__control"
          ariaLabel={t("language")}
          value={locale}
          options={LOCALES.map((item) => ({
            value: item.code,
            label: item.label,
          }))}
          onChange={(value) => void i18n.changeLanguage(value)}
        />

        <SelectMenu<ThemeMode>
          id="auth-theme"
          className="auth-v1__control"
          ariaLabel={t("theme.auto")}
          value={mode}
          options={[
            { value: "light", label: t("theme.light") },
            { value: "dark", label: t("theme.dark") },
            { value: "system", label: t("theme.auto") },
          ]}
          onChange={setMode}
        />
      </div>

      <main className="auth-v1__stage">
        <div
          className={
            wide ? "auth-v1__surface auth-v1__surface--wide" : "auth-v1__surface"
          }
        >
          <div className="auth-v1__brand">
            <BrandMark />
            <span className="auth-v1__brand-name">ActiLens</span>
          </div>
          {children}
        </div>
      </main>
    </div>
  );
}
