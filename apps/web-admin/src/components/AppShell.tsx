import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { NavLink, Outlet, useLocation, useNavigate } from "react-router-dom";
import { useTranslation } from "react-i18next";
import { useAuth } from "../auth/AuthContext";
import { useTheme, type ThemeMode } from "../theme/ThemeProvider";
import { useBusinesses } from "../useBusinesses";
import { memberTerms } from "../terms";
import { DetailHeaderContext } from "../detailHeader";
import { canManageSettings } from "../rbac";
import { LOCALES } from "../i18n";
import { cx, IconButton, SelectMenu } from "./ds";

const SIDEBAR_KEY = "actilens.admin.sidebarCollapsed";

function Icon({
  children,
  size = 18,
}: {
  children: ReactNode;
  size?: number;
}) {
  return (
    <svg
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      {children}
    </svg>
  );
}

const DashboardIcon = () => (
  <Icon>
    <rect x="3" y="3" width="7" height="7" rx="1.5" />
    <rect x="14" y="3" width="7" height="7" rx="1.5" />
    <rect x="3" y="14" width="7" height="7" rx="1.5" />
    <rect x="14" y="14" width="7" height="7" rx="1.5" />
  </Icon>
);

const MembersIcon = () => (
  <Icon>
    <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" />
    <circle cx="9" cy="7" r="4" />
    <path d="M19 8v6" />
    <path d="M22 11h-6" />
  </Icon>
);

const SettingsIcon = () => (
  <Icon>
    <circle cx="12" cy="12" r="3" />
    <path d="M19.4 15a1.7 1.7 0 0 0 .34 1.88l.06.06-2.83 2.83-.06-.06a1.7 1.7 0 0 0-1.88-.34 1.7 1.7 0 0 0-1.03 1.56V21h-4v-.08A1.7 1.7 0 0 0 8.97 19.4a1.7 1.7 0 0 0-1.88.34l-.06.06-2.83-2.83.06-.06A1.7 1.7 0 0 0 4.6 15a1.7 1.7 0 0 0-1.56-1.03H3v-4h.08A1.7 1.7 0 0 0 4.6 8.97a1.7 1.7 0 0 0-.34-1.88L4.2 7.03 7.03 4.2l.06.06A1.7 1.7 0 0 0 8.97 4.6 1.7 1.7 0 0 0 10 3.04V3h4v.08a1.7 1.7 0 0 0 1.03 1.52 1.7 1.7 0 0 0 1.88-.34l.06-.06 2.83 2.83-.06.06a1.7 1.7 0 0 0-.34 1.88A1.7 1.7 0 0 0 20.96 10H21v4h-.08A1.7 1.7 0 0 0 19.4 15Z" />
  </Icon>
);

const ChevronDownIcon = () => (
  <Icon size={16}>
    <path d="m7 10 5 5 5-5" />
  </Icon>
);

const CheckIcon = () => (
  <Icon size={16}>
    <path d="m5 12 4 4L19 6" />
  </Icon>
);

const PlusIcon = () => (
  <Icon size={16}>
    <path d="M12 5v14" />
    <path d="M5 12h14" />
  </Icon>
);

const LogOutIcon = () => (
  <Icon size={17}>
    <path d="m16 17 5-5-5-5" />
    <path d="M21 12H9" />
    <path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4" />
  </Icon>
);

const CollapseIcon = ({ collapsed }: { collapsed: boolean }) => (
  <Icon size={18}>
    <rect x="3" y="4" width="18" height="16" rx="2" />
    <path d="M9 4v16" />
    <path d={collapsed ? "m13 9 3 3-3 3" : "m16 9-3 3 3 3"} />
  </Icon>
);

function BrandMark() {
  return (
    <span className="ds-brand-mark" aria-hidden>
      <img src="/brand/mark.svg" alt="" />
    </span>
  );
}

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (parts.length === 0) return "?";
  if (parts.length === 1) return parts[0].slice(0, 2).toUpperCase();
  return (parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}

function useDismiss(open: boolean, close: () => void) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    function onPointerDown(event: MouseEvent) {
      if (ref.current && !ref.current.contains(event.target as Node)) close();
    }
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") close();
    }
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open, close]);

  return ref;
}

function OrganizationPicker() {
  const { t } = useTranslation("dashboard");
  const { t: tCommon } = useTranslation("common");
  const navigate = useNavigate();
  const { businesses, selected, selectedId, setSelectedId } = useBusinesses();
  const [open, setOpen] = useState(false);
  const ref = useDismiss(open, () => setOpen(false));

  if (!selected) return null;

  return (
    <div className="ds-org-picker" ref={ref}>
      <button
        type="button"
        className="ds-org-picker__trigger"
        aria-haspopup="menu"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <span className="ds-org-picker__mark">{initials(selected.name)}</span>
        <span className="ds-org-picker__copy">
          <span className="ds-org-picker__eyebrow">
            <span>{t("dashboard.organization", { defaultValue: "Workspace" })}</span>
            {selected.deletion_scheduled_at ? (
              <span className="ds-org-picker__status ds-org-picker__status--danger">
                {tCommon("shell.deletionPending")}
              </span>
            ) : selected.archived_at ? (
              <span className="ds-org-picker__status">
                {tCommon("shell.archived")}
              </span>
            ) : null}
          </span>
          <span className="ds-org-picker__name">{selected.name}</span>
        </span>
        <span className="ds-org-picker__chevron">
          <ChevronDownIcon />
        </span>
      </button>

      {open && (
        <div className="ds-shell-popover ds-org-picker__menu" role="menu">
          {businesses.map((business) => (
            <button
              key={business.id}
              type="button"
              role="menuitemradio"
              aria-checked={business.id === selectedId}
              className="ds-menu__item"
              onClick={() => {
                setSelectedId(business.id);
                setOpen(false);
              }}
            >
              <span className="ds-org-option__mark">{initials(business.name)}</span>
              <span style={{ overflow: "hidden", textOverflow: "ellipsis" }}>{business.name}</span>
              {business.id === selectedId && (
                <span className="ds-org-option__check">
                  <CheckIcon />
                </span>
              )}
            </button>
          ))}
          <div className="ds-menu__separator" />
          <button
            type="button"
            className="ds-menu__item"
            role="menuitem"
            onClick={() => {
              setOpen(false);
              navigate("/employees?new=1");
            }}
          >
            <PlusIcon />
            {t("dashboard.newTeam")}
          </button>
        </div>
      )}
    </div>
  );
}

function AccountMenu() {
  const { t, i18n } = useTranslation();
  const navigate = useNavigate();
  const { user, logout } = useAuth();
  const { mode, setMode } = useTheme();
  const [open, setOpen] = useState(false);
  const ref = useDismiss(open, () => setOpen(false));

  const displayName = user?.display_name || user?.email || user?.username || "Account";
  const identifier = user?.email || user?.username || "";
  const locale =
    LOCALES.find((item) => item.code === i18n.resolvedLanguage)?.code ?? "en";

  return (
    <div className="ds-account" ref={ref}>
      <button
        type="button"
        className="ds-account-trigger"
        aria-haspopup="dialog"
        aria-expanded={open}
        onClick={() => setOpen((value) => !value)}
      >
        <span className="ds-account-avatar">{initials(displayName)}</span>
        <span className="ds-account-copy">
          <span className="ds-account-name">{displayName}</span>
          <span className="ds-account-id">{identifier}</span>
        </span>
        <span className="ds-account-caret">
          <ChevronDownIcon />
        </span>
      </button>

      {open && (
        <div
          className="ds-shell-popover ds-account-menu"
          role="dialog"
          aria-label={displayName}
        >
          <div className="ds-account-menu__identity">
            <div className="ds-account-menu__name">{displayName}</div>
            <div className="ds-account-menu__id">{identifier}</div>
          </div>

          <div className="ds-menu__separator" />

          <div className="ds-account-menu__section">
            <div className="ds-account-menu__label">{t("shell.appearance")}</div>
            <div className="ds-theme-choice">
              {(["light", "dark", "system"] as ThemeMode[]).map((themeMode) => (
                <button
                  key={themeMode}
                  type="button"
                  className={cx(
                    "ds-theme-choice__option",
                    themeMode === mode && "is-active",
                  )}
                  aria-pressed={themeMode === mode}
                  onClick={() => setMode(themeMode)}
                >
                  {themeMode === "light"
                    ? t("theme.light")
                    : themeMode === "dark"
                      ? t("theme.dark")
                      : t("theme.auto")}
                </button>
              ))}
            </div>
          </div>

          <div className="ds-account-menu__section">
            <label className="ds-account-menu__label" htmlFor="shell-language">
              {t("language")}
            </label>
            <SelectMenu
              id="shell-language"
              value={locale}
              ariaLabel={t("language")}
              options={LOCALES.map((item) => ({
                value: item.code,
                label: item.label,
              }))}
              onChange={(value) => void i18n.changeLanguage(value)}
            />
          </div>

          <div className="ds-menu__separator" />

          <button
            type="button"
            className="ds-menu__item"
            onClick={() => {
              setOpen(false);
              navigate("/account");
            }}
          >
            <SettingsIcon />
            {t("shell.account")}
          </button>

          <div className="ds-menu__separator" />

          <button
            type="button"
            className="ds-menu__item ds-menu__item--danger"
            onClick={logout}
          >
            <LogOutIcon />
            {t("actions.signOut")}
          </button>
        </div>
      )}
    </div>
  );
}

export function AppShell() {
  const { t } = useTranslation();
  const { selected } = useBusinesses();
  const terms = memberTerms(selected?.kind);
  const location = useLocation();

  const [collapsed, setCollapsed] = useState(() => {
    const saved = localStorage.getItem(SIDEBAR_KEY);
    if (saved !== null) return saved === "1";
    return window.matchMedia("(max-width: 1179px)").matches;
  });

  const nav = [
    { to: "/", label: t("nav.dashboard"), end: true, icon: <DashboardIcon /> },
    { to: "/employees", label: terms.many, end: false, icon: <MembersIcon /> },
    ...(canManageSettings(selected?.role)
      ? [{ to: "/settings", label: t("nav.settings"), end: false, icon: <SettingsIcon /> }]
      : []),
  ];

  const activeNav = nav.find((item) =>
    item.end ? location.pathname === item.to : location.pathname.startsWith(item.to),
  );
  const isDetail = /^\/employees\/[^/]+/.test(location.pathname);
  const [detailTitle, setDetailTitle] = useState<string | null>(null);
  const detailHeader = useMemo(() => ({ setTitle: setDetailTitle }), []);

  const currentTitle = isDetail
    ? detailTitle || terms.one
    : location.pathname.startsWith("/account")
      ? t("shell.account")
      : activeNav?.label || t("nav.dashboard");

  function toggleSidebar() {
    setCollapsed((current) => {
      const next = !current;
      localStorage.setItem(SIDEBAR_KEY, next ? "1" : "0");
      return next;
    });
  }

  return (
    <div className="ds-app-shell" data-sidebar={collapsed ? "collapsed" : "expanded"}>
      <aside className="ds-sidebar">
        <div className="ds-sidebar__brand">
          <NavLink to="/" className="ds-brand-link" aria-label="ActiLens">
            <BrandMark />
            <span className="ds-brand-wordmark">ActiLens</span>
          </NavLink>
        </div>

        <OrganizationPicker />

        <nav className="ds-sidebar__nav" aria-label={t("shell.navigation")}>
          {nav.map((item) => (
            <NavLink
              key={item.to}
              to={item.to}
              end={item.end}
              title={collapsed ? item.label : undefined}
              className={({ isActive }) =>
                cx("ds-sidebar-nav__item", isActive && "is-active")
              }
            >
              <span className="ds-sidebar-nav__icon">{item.icon}</span>
              <span className="ds-sidebar-nav__label">{item.label}</span>
            </NavLink>
          ))}
        </nav>

        <div className="ds-sidebar__spacer" />
        <AccountMenu />
      </aside>

      <main className="ds-shell-main">
        <header className="ds-shell-topbar">
          <IconButton
            label={collapsed ? t("shell.expand") : t("shell.collapse")}
            className="ds-shell-topbar__toggle"
            onClick={toggleSidebar}
          >
            <CollapseIcon collapsed={collapsed} />
          </IconButton>
          <div className="ds-shell-page-title">{currentTitle}</div>
        </header>

        <div className="ds-shell-content">
          <div className="ds-shell-content__inner">
            <DetailHeaderContext.Provider value={detailHeader}>
              <Outlet />
            </DetailHeaderContext.Provider>
          </div>
        </div>
      </main>
    </div>
  );
}
