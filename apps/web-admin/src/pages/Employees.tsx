import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties, type FormEvent, type ReactNode } from "react";
import { createPortal } from "react-dom";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { Trans, useTranslation } from "react-i18next";
import {
  blockMember,
  createBusiness,
  createEmployee,
  listBusinessEmployees,
  listFormerMembers,
  removeMember,
  resetManagedMemberPassword,
  restoreMember,
  unblockMember,
  updateManagedMemberIdentity,
} from "../api/endpoints";
import { ApiError, type BusinessKind, type Employee } from "../api/types";
import {
  Alert,
  Button,
  Dialog,
  EmptyState,
  IconButton,
  Badge,
  Skeleton,
  TextField,
} from "../components/ds";
import { useToast } from "../components/ToastProvider";
import { MemberRoleControl } from "../components/employee/MemberRoleControl";
import { MemberMonitoringControl } from "../components/employee/MemberMonitoringControl";
import { EnrollmentTokenControl } from "../components/employee/EnrollmentTokenControl";
import { PermanentDeleteControl } from "../components/employee/PermanentDeleteControl";
import { useBusinesses } from "../useBusinesses";
import { memberTerms, type MemberTerms } from "../terms";
import { canManageMembers, canManageRoles } from "../rbac";
import "../theme/employees.css";

function Icon({
  children,
  size = 16,
}: {
  children: ReactNode;
  size?: number;
}) {
  return (
    <svg
      viewBox="0 0 24 24"
      width={size}
      height={size}
      fill="none"
      stroke="currentColor"
      strokeWidth="2"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      {children}
    </svg>
  );
}

const PlusIcon = () => (
  <Icon>
    <path d="M12 5v14" />
    <path d="M5 12h14" />
  </Icon>
);

const MoreIcon = () => (
  <Icon size={18}>
    <circle cx="5" cy="12" r="1" fill="currentColor" stroke="none" />
    <circle cx="12" cy="12" r="1" fill="currentColor" stroke="none" />
    <circle cx="19" cy="12" r="1" fill="currentColor" stroke="none" />
  </Icon>
);

const DiceIcon = () => (
  <Icon>
    <rect x="3" y="3" width="18" height="18" rx="4" />
    <circle cx="8" cy="8" r="1" fill="currentColor" stroke="none" />
    <circle cx="16" cy="16" r="1" fill="currentColor" stroke="none" />
    <circle cx="12" cy="12" r="1" fill="currentColor" stroke="none" />
  </Icon>
);

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (!parts.length) return "?";
  if (parts.length === 1) return parts[0][0]?.toUpperCase() || "?";
  return `${parts[0][0] || ""}${parts[parts.length - 1][0] || ""}`.toUpperCase();
}

type Presence = "active" | "idle" | "offline" | "blocked";

function presence(employee: Employee): Presence {
  if (employee.status === "blocked" || !employee.active) return "blocked";
  if (!employee.last_seen) return "offline";
  const age = Math.max(0, Date.now() / 1000 - employee.last_seen);
  if (age < 420) return "active";
  if (age < 1200) return "idle";
  return "offline";
}

function genTempPassword(): string {
  const chars = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789";
  let out = "";
  for (let i = 0; i < 12; i++) {
    out += chars[Math.floor(Math.random() * chars.length)];
  }
  return out;
}

function useAnchoredMenu(
  open: boolean,
  setOpen: (open: boolean) => void,
) {
  const anchorRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const [style, setStyle] = useState<CSSProperties>({
    position: "fixed",
    top: 0,
    left: 0,
    visibility: "hidden",
  });

  useLayoutEffect(() => {
    if (!open) return;

    const gap = 6;
    const margin = 8;

    function updatePosition() {
      const anchor = anchorRef.current;
      if (!anchor) return;

      const rect = anchor.getBoundingClientRect();
      const width = menuRef.current?.offsetWidth || 232;
      const height = menuRef.current?.offsetHeight || 0;
      const maxLeft = Math.max(margin, window.innerWidth - width - margin);
      const left = Math.min(Math.max(margin, rect.right - width), maxLeft);
      const below = rect.bottom + gap;
      const above = rect.top - height - gap;
      const maxTop = Math.max(margin, window.innerHeight - height - margin);
      const top =
        height > 0 && below + height > window.innerHeight - margin && above >= margin
          ? above
          : Math.min(Math.max(margin, below), maxTop);

      setStyle({
        position: "fixed",
        top,
        left,
        zIndex: 1000,
        visibility: "visible",
      });
    }

    function onPointerDown(event: MouseEvent) {
      const target = event.target as Node;
      if (anchorRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      setOpen(false);
    }

    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") setOpen(false);
    }

    const frame = requestAnimationFrame(updatePosition);
    window.addEventListener("resize", updatePosition);
    document.addEventListener("scroll", updatePosition, true);
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);
    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("resize", updatePosition);
      document.removeEventListener("scroll", updatePosition, true);
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [open, setOpen]);

  return { anchorRef, menuRef, style };
}

function EmployeesSkeleton() {
  return (
    <div className="employees-loading-card" aria-hidden>
      {Array.from({ length: 4 }, (_, index) => (
        <div className="employees-loading-row" key={index}>
          <Skeleton width="72%" height={16} />
          <Skeleton width="68%" height={14} />
          <Skeleton width={92} height={26} />
          <Skeleton width={112} height={24} />
          <Skeleton width="70%" height={14} />
          <Skeleton width={86} height={14} />
          <Skeleton width={30} height={30} />
        </div>
      ))}
    </div>
  );
}

function EmployeeActionsMenu({
  employee,
  businessId,
  canManage,
  canDelete,
  onChanged,
}: {
  employee: Employee;
  businessId: string;
  canManage: boolean;
  canDelete: boolean;
  onChanged: () => void;
}) {
  const { t } = useTranslation("dashboard");
  const { pushToast } = useToast();
  const [open, setOpen] = useState(false);
  const [editOpen, setEditOpen] = useState(false);
  const [passwordOpen, setPasswordOpen] = useState(false);
  const [statusOpen, setStatusOpen] = useState(false);
  const [removeOpen, setRemoveOpen] = useState(false);
  const [editName, setEditName] = useState(employee.display_name);
  const [editLogin, setEditLogin] = useState(employee.email || employee.username || "");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [dialogError, setDialogError] = useState<string | null>(null);
  const menu = useAnchoredMenu(open, setOpen);

  async function saveEdit() {
    const name = editName.trim();
    const login = editLogin.trim();
    if (!name || !login) return;

    setBusy(true);
    setDialogError(null);
    try {
      const patch = login.includes("@")
        ? { display_name: name, email: login, username: "" }
        : { display_name: name, username: login.toLowerCase(), email: "" };
      await updateManagedMemberIdentity(businessId, employee.id, patch);
      setEditOpen(false);
      setOpen(false);
      onChanged();
      pushToast({ title: t("employees.prompts.saved"), tone: "success" });
    } catch {
      setDialogError(t("employees.prompts.failed"));
    } finally {
      setBusy(false);
    }
  }

  async function savePassword() {
    if (password.length < 8) return;
    setBusy(true);
    setDialogError(null);
    try {
      await resetManagedMemberPassword(businessId, employee.id, password);
      setPasswordOpen(false);
      setOpen(false);
      setPassword("");
      pushToast({ title: t("employees.prompts.passwordSaved"), tone: "success" });
    } catch {
      setDialogError(t("employees.prompts.failed"));
    } finally {
      setBusy(false);
    }
  }

  async function toggleBlocked() {
    setBusy(true);
    setDialogError(null);
    try {
      if (employee.status === "blocked") {
        await unblockMember(businessId, employee.id);
      } else {
        await blockMember(businessId, employee.id);
      }
      setStatusOpen(false);
      setOpen(false);
      onChanged();
      pushToast({ title: t("employees.prompts.saved"), tone: "success" });
    } catch {
      setDialogError(t("employees.prompts.failed"));
    } finally {
      setBusy(false);
    }
  }

  async function removeFromOrganization() {
    setBusy(true);
    setDialogError(null);
    try {
      await removeMember(businessId, employee.id);
      setRemoveOpen(false);
      setOpen(false);
      onChanged();
      pushToast({ title: t("employees.lifecycle.removedToast"), tone: "success" });
    } catch {
      setDialogError(t("employees.prompts.failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="employees-actions">
      <IconButton
        ref={menu.anchorRef}
        label={t("employees.actions.more")}
        onClick={(event) => {
          event.stopPropagation();
          setOpen((current) => !current);
        }}
      >
        <MoreIcon />
      </IconButton>

      {open && createPortal(
        <div
          ref={menu.menuRef}
          className="ds-shell-popover employees-actions__menu"
          role="menu"
          style={menu.style}
        >
          <Link
            className="ds-menu__item"
            to={`/employees/${employee.id}?business=${businessId}`}
            onClick={() => setOpen(false)}
          >
            {t("employees.actions.openProfile")}
          </Link>

          {canManage && (
            <>
              {employee.status !== "blocked" && (
                <>
                  <EnrollmentTokenControl
                    employee={employee}
                    businessId={businessId}
                    canChange
                    triggerVariant="menu-item"
                    onDialogClose={() => setOpen(false)}
                  />
                  <button
                    type="button"
                    className="ds-menu__item"
                    onClick={() => {
                      setEditName(employee.display_name);
                      setEditLogin(employee.email || employee.username || "");
                      setOpen(false);
                      setDialogError(null);
                      setEditOpen(true);
                    }}
                  >
                    {t("employees.actions.edit")}
                  </button>
                  <button
                    type="button"
                    className="ds-menu__item"
                    onClick={() => {
                      setOpen(false);
                      setPassword("");
                      setDialogError(null);
                      setPasswordOpen(true);
                    }}
                  >
                    {t("employees.actions.password")}
                  </button>
                </>
              )}
              <button
                type="button"
                className="ds-menu__item"
                onClick={() => {
                  setOpen(false);
                  setDialogError(null);
                  setStatusOpen(true);
                }}
              >
                {t(employee.status === "blocked" ? "employees.actions.restore" : "employees.actions.archive")}
              </button>
              <button
                type="button"
                className="ds-menu__item ds-menu__item--danger"
                onClick={() => {
                  setOpen(false);
                  setDialogError(null);
                  setRemoveOpen(true);
                }}
              >
                {t("employees.actions.remove")}
              </button>
            </>
          )}

          {canDelete && (
            <>
              <div className="ds-menu__separator" />
              <PermanentDeleteControl
                employee={employee}
                businessId={businessId}
                canDelete={canDelete}
                onDeleted={onChanged}
                triggerVariant="menu-item"
                onDialogClose={() => setOpen(false)}
              />
            </>
          )}
        </div>,
        document.body,
      )}

      {editOpen && (
        <Dialog
          title={t("employees.actions.edit")}
          onClose={() => !busy && setEditOpen(false)}
          closeOnBackdrop={!busy}
          footer={
            <>
              <Button variant="secondary" disabled={busy} onClick={() => setEditOpen(false)}>
                {t("newBusinessModal.cancel")}
              </Button>
              <Button variant="primary" loading={busy} onClick={saveEdit}>
                {t("common:actions.save")}
              </Button>
            </>
          }
        >
          <div className="employees-form">
            <TextField
              id={`edit-name-${employee.id}`}
              label={t("employees.prompts.displayName")}
              value={editName}
              onChange={(event) => setEditName(event.target.value)}
              disabled={busy}
            />
            <TextField
              id={`edit-login-${employee.id}`}
              label={t("employees.prompts.login")}
              value={editLogin}
              onChange={(event) => setEditLogin(event.target.value)}
              disabled={busy}
              autoCapitalize="none"
              spellCheck={false}
            />
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </div>
        </Dialog>
      )}

      {passwordOpen && (
        <Dialog
          title={t("employees.actions.password")}
          onClose={() => !busy && setPasswordOpen(false)}
          closeOnBackdrop={!busy}
          footer={
            <>
              <Button variant="secondary" disabled={busy} onClick={() => setPasswordOpen(false)}>
                {t("newBusinessModal.cancel")}
              </Button>
              <Button
                variant="primary"
                loading={busy}
                disabled={password.length < 8}
                onClick={savePassword}
              >
                {t("common:actions.save")}
              </Button>
            </>
          }
        >
          <div className="employees-form">
            <TextField
              id={`password-${employee.id}`}
              label={t("employees.prompts.password")}
              type="password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
              disabled={busy}
              autoComplete="new-password"
            />
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </div>
        </Dialog>
      )}

      {statusOpen && (
        <Dialog
          title={t(employee.status === "blocked" ? "employees.actions.restore" : "employees.actions.archive")}
          size="confirm"
          onClose={() => !busy && setStatusOpen(false)}
          closeOnBackdrop={!busy}
          footer={
            <>
              <Button variant="secondary" disabled={busy} onClick={() => setStatusOpen(false)}>
                {t("newBusinessModal.cancel")}
              </Button>
              <Button
                variant={employee.status === "blocked" ? "primary" : "danger"}
                loading={busy}
                onClick={toggleBlocked}
              >
                {t(employee.status === "blocked" ? "employees.actions.restore" : "employees.actions.archive")}
              </Button>
            </>
          }
        >
          <p className="employees-dialog-note">
            {t(employee.status === "blocked" ? "employees.prompts.confirmRestore" : "employees.prompts.confirmArchive")}
          </p>
          {dialogError && <Alert tone="danger">{dialogError}</Alert>}
        </Dialog>
      )}

      {removeOpen && (
        <Dialog
          title={t("employees.lifecycle.removeTitle", { name: employee.display_name })}
          size="confirm"
          onClose={() => !busy && setRemoveOpen(false)}
          closeOnBackdrop={!busy}
          footer={
            <>
              <Button variant="secondary" disabled={busy} onClick={() => setRemoveOpen(false)}>
                {t("newBusinessModal.cancel")}
              </Button>
              <Button variant="danger" loading={busy} onClick={removeFromOrganization}>
                {t("employees.actions.remove")}
              </Button>
            </>
          }
        >
          <div className="employees-dialog-stack">
            <Alert tone="warning">{t("employees.lifecycle.removeWarning")}</Alert>
            <p className="employees-dialog-note">
              {t("employees.lifecycle.removeScope")}
            </p>
            {dialogError && <Alert tone="danger">{dialogError}</Alert>}
          </div>
        </Dialog>
      )}
    </div>
  );
}

function FormerMemberActions({
  employee,
  businessId,
  canRestore,
  canDelete,
  onChanged,
}: {
  employee: Employee;
  businessId: string;
  canRestore: boolean;
  canDelete: boolean;
  onChanged: () => void;
}) {
  const { t } = useTranslation("dashboard");
  const { pushToast } = useToast();
  const [open, setOpen] = useState(false);
  const [restoreOpen, setRestoreOpen] = useState(false);
  const [monitoringEnabled, setMonitoringEnabled] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const menu = useAnchoredMenu(open, setOpen);

  async function restore() {
    setBusy(true);
    setError(null);
    try {
      await restoreMember(businessId, employee.id, monitoringEnabled);
      setRestoreOpen(false);
      onChanged();
      pushToast({ title: t("employees.lifecycle.restoredToast"), tone: "success" });
    } catch {
      setError(t("employees.prompts.failed"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="employees-actions">
      <IconButton
        ref={menu.anchorRef}
        label={t("employees.actions.more")}
        onClick={(event) => {
          event.stopPropagation();
          setOpen((current) => !current);
        }}
      >
        <MoreIcon />
      </IconButton>

      {open && createPortal(
        <div
          ref={menu.menuRef}
          className="ds-shell-popover employees-actions__menu"
          role="menu"
          style={menu.style}
        >
          <Link
            className="ds-menu__item"
            to={`/employees/${employee.id}?business=${businessId}&former=1`}
            onClick={() => setOpen(false)}
          >
            {t("employees.lifecycle.viewHistory")}
          </Link>

          {canRestore && (
            <button
              type="button"
              className="ds-menu__item"
              onClick={() => {
                setOpen(false);
                setError(null);
                setMonitoringEnabled(true);
                setRestoreOpen(true);
              }}
            >
              {t("employees.lifecycle.restore")}
            </button>
          )}

          {canDelete && (
            <>
              <div className="ds-menu__separator" />
              <PermanentDeleteControl
                employee={employee}
                businessId={businessId}
                canDelete={canDelete}
                onDeleted={onChanged}
                triggerVariant="menu-item"
                onDialogClose={() => setOpen(false)}
              />
            </>
          )}
        </div>,
        document.body,
      )}

      {restoreOpen && (
        <Dialog
          title={t("employees.lifecycle.restoreTitle", { name: employee.display_name })}
          size="confirm"
          onClose={() => !busy && setRestoreOpen(false)}
          closeOnBackdrop={!busy}
          footer={
            <>
              <Button variant="secondary" disabled={busy} onClick={() => setRestoreOpen(false)}>
                {t("newBusinessModal.cancel")}
              </Button>
              <Button variant="primary" loading={busy} onClick={restore}>
                {t("employees.lifecycle.restore")}
              </Button>
            </>
          }
        >
          <div className="employees-dialog-stack">
            <Alert tone="info">{t("employees.lifecycle.restoreInfo")}</Alert>
            <label className="employees-monitoring-choice">
              <input
                type="checkbox"
                checked={monitoringEnabled}
                disabled={busy}
                onChange={(event) => setMonitoringEnabled(event.currentTarget.checked)}
              />
              <span>
                <strong>{t("employees.lifecycle.restoreMonitoring")}</strong>
                <small>{t("employees.lifecycle.restoreMonitoringHelp")}</small>
              </span>
            </label>
            {error && <Alert tone="danger">{error}</Alert>}
          </div>
        </Dialog>
      )}
    </div>
  );
}

function NewBusinessDialog({
  terms,
  kind,
  onClose,
  onCreated,
}: {
  terms: MemberTerms;
  kind: BusinessKind | undefined;
  onClose: () => void;
  onCreated: (id: string) => void;
}) {
  const { t } = useTranslation("dashboard");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const isFamily = kind === "family";
  const orgCap = terms.org.charAt(0).toUpperCase() + terms.org.slice(1);

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (!name.trim()) return;
    setBusy(true);
    setError(null);
    try {
      const business = await createBusiness(name.trim());
      onCreated(business.id);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : t("newBusinessModal.errorCreate"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      title={t("employees.newOrg", { org: terms.org })}
      onClose={() => !busy && onClose()}
      closeOnBackdrop={!busy}
      footer={
        <>
          <Button variant="secondary" disabled={busy} onClick={onClose}>
            {t("newBusinessModal.cancel")}
          </Button>
          <Button
            type="submit"
            form="new-business-form"
            variant="primary"
            loading={busy}
            disabled={!name.trim()}
          >
            {t("newBusinessModal.create")}
          </Button>
        </>
      }
    >
      <form id="new-business-form" className="employees-form" onSubmit={submit}>
        {error && <Alert tone="danger">{error}</Alert>}
        <TextField
          id="new-business-name"
          label={t("newBusinessModal.orgNameLabel", { org: orgCap })}
          placeholder={t(
            isFamily
              ? "newBusinessModal.namePlaceholderFamily"
              : "newBusinessModal.namePlaceholder",
          )}
          value={name}
          onChange={(event) => setName(event.target.value)}
          disabled={busy}
          autoFocus
        />
      </form>
    </Dialog>
  );
}

function NewEmployeeDialog({
  businessId,
  terms,
  onClose,
  onCreated,
}: {
  businessId: string;
  terms: MemberTerms;
  onClose: () => void;
  onCreated: (businessId: string) => void;
}) {
  const { t } = useTranslation("dashboard");
  const [login, setLogin] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [fieldErrors, setFieldErrors] = useState<{ login?: string; password?: string }>({});

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    setFieldErrors({});

    const value = login.trim();
    const isEmail = value.includes("@");

    try {
      const result = await createEmployee({
        email: isEmail ? value : undefined,
        username: isEmail ? undefined : value.toLowerCase(),
        display_name: displayName.trim(),
        password,
        business_id: businessId,
      });
      onCreated(result.business.id);
    } catch (err) {
      if (err instanceof ApiError) {
        if (err.status === 409) {
          setFieldErrors({ login: t("newEmployeeModal.errorTaken") });
        } else if (/password/i.test(err.message)) {
          setFieldErrors({ password: err.message });
        } else if (/username/i.test(err.message)) {
          setFieldErrors({ login: err.message });
        } else {
          setError(err.message);
        }
      } else {
        setError(t("newEmployeeModal.errorCreate", { member: terms.lowerOne }));
      }
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog
      title={terms.addCta}
      onClose={() => !busy && onClose()}
      closeOnBackdrop={!busy}
      footer={
        <>
          <Button variant="secondary" disabled={busy} onClick={onClose}>
            {t("newEmployeeModal.cancel")}
          </Button>
          <Button
            type="submit"
            form="new-employee-form"
            variant="primary"
            loading={busy}
            disabled={!displayName.trim() || !login.trim() || password.length < 8}
          >
            {terms.addCta}
          </Button>
        </>
      }
    >
      <form id="new-employee-form" className="employees-form" onSubmit={submit}>
        {error && <Alert tone="danger">{error}</Alert>}

        <TextField
          id="new-employee-name"
          label={t("newEmployeeModal.displayName")}
          value={displayName}
          onChange={(event) => setDisplayName(event.target.value)}
          disabled={busy}
          autoFocus
        />

        <TextField
          id="new-employee-login"
          label={t("newEmployeeModal.usernameOrEmail")}
          value={login}
          onChange={(event) => setLogin(event.target.value)}
          disabled={busy}
          error={fieldErrors.login}
          autoCapitalize="none"
          spellCheck={false}
          autoComplete="off"
        />

        <div className="employees-password-row">
          <TextField
            id="new-employee-password"
            label={t("newEmployeeModal.temporaryPassword")}
            value={password}
            onChange={(event) => setPassword(event.target.value)}
            disabled={busy}
            error={fieldErrors.password}
            autoComplete="new-password"
          />
          <Button
            variant="secondary"
            leadingIcon={<DiceIcon />}
            disabled={busy}
            onClick={() => setPassword(genTempPassword())}
          >
            {t("newEmployeeModal.generate")}
          </Button>
        </div>

        <p className="employees-dialog-note">
          {t("newEmployeeModal.shareCredentials", { member: terms.lowerOne })}
        </p>
      </form>
    </Dialog>
  );
}

export function Employees() {
  const { t } = useTranslation("dashboard");
  const { t: tCommon } = useTranslation("common");
  const navigate = useNavigate();
  const { pushToast } = useToast();
  const {
    businesses,
    selected,
    selectedId,
    setSelectedId,
    loading: businessLoading,
    reload: reloadBusinesses,
  } = useBusinesses();

  const [employees, setEmployees] = useState<Employee[]>([]);
  const [formerEmployees, setFormerEmployees] = useState<Employee[]>([]);
  const [view, setView] = useState<"active" | "former">("active");
  const [loading, setLoading] = useState(false);
  const [listError, setListError] = useState<string | null>(null);
  const [showBusiness, setShowBusiness] = useState(false);
  const [showEmployee, setShowEmployee] = useState(false);

  const terms = memberTerms(selected?.kind);
  const organizationReadOnly = Boolean(
    selected?.archived_at || selected?.deletion_scheduled_at,
  );
  const mayViewFormer = canManageMembers(selected?.role);
  const mayManageMembers = mayViewFormer && !organizationReadOnly;
  const mayManageRoles = canManageRoles(selected?.role) && !organizationReadOnly;

  function loadEmployees(id: string) {
    setLoading(true);
    setListError(null);
    listBusinessEmployees(id)
      .then((result) => setEmployees(result.employees))
      .catch(() =>
        setListError(t("employees.errorLoadMembers", { members: terms.lowerMany })),
      )
      .finally(() => setLoading(false));
  }

  function loadFormer(id: string) {
    setLoading(true);
    setListError(null);
    listFormerMembers(id)
      .then((result) => setFormerEmployees(result.employees))
      .catch(() => setListError(t("employees.lifecycle.errorLoadFormer")))
      .finally(() => setLoading(false));
  }

  useEffect(() => {
    if (!selectedId) {
      setEmployees([]);
      setFormerEmployees([]);
      return;
    }
    if (view === "former") loadFormer(selectedId);
    else loadEmployees(selectedId);
  }, [selectedId, view]);

  const [searchParams, setSearchParams] = useSearchParams();
  useEffect(() => {
    if (searchParams.get("new") === null) return;
    setShowBusiness(true);
    const next = new URLSearchParams(searchParams);
    next.delete("new");
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams]);

  function relativeTime(timestamp?: number | null): string {
    if (!timestamp) return t("employees.status.never");
    const seconds = Math.max(0, Math.floor(Date.now() / 1000 - timestamp));
    if (seconds < 60) return tCommon("time.justNow");
    if (seconds < 3600) return tCommon("time.minutesAgo", { count: Math.floor(seconds / 60) });
    if (seconds < 86400) return tCommon("time.hoursAgo", { count: Math.floor(seconds / 3600) });
    return tCommon("time.daysAgo", { count: Math.floor(seconds / 86400) });
  }

  return (
    <div className="employees-page">
      <div className="employees-toolbar">
        <span className="employees-toolbar__count">
          {selected
            ? t("employees.total", {
                count: view === "former" ? formerEmployees.length : employees.length,
              })
            : ""}
        </span>
        {mayManageMembers && (
          <Button
            variant="primary"
            leadingIcon={<PlusIcon />}
            onClick={() => setShowEmployee(true)}
          >
            {terms.addCta}
          </Button>
        )}
      </div>

      {selectedId && mayViewFormer && (
        <div className="employees-view-tabs" role="tablist" aria-label={t("employees.lifecycle.viewLabel")}>
          <button
            type="button"
            role="tab"
            aria-selected={view === "active"}
            className={`employees-view-tab${view === "active" ? " is-active" : ""}`}
            onClick={() => setView("active")}
          >
            {t("employees.lifecycle.activeMembers")}
          </button>
          <button
            type="button"
            role="tab"
            aria-selected={view === "former"}
            className={`employees-view-tab${view === "former" ? " is-active" : ""}`}
            onClick={() => setView("former")}
          >
            {t("employees.lifecycle.formerMembers")}
          </button>
        </div>
      )}

      {!businessLoading && businesses.length === 0 && (
        <div style={{ marginBottom: 16 }}>
          <Alert tone="info">
            <Trans
              t={t}
              i18nKey="employees.noBusinessNotice"
              values={{ member: terms.lowerOne }}
              components={{ 1: <em /> }}
            />
          </Alert>
        </div>
      )}

      {listError && (
        <div style={{ marginBottom: 16 }}>
          <Alert tone="danger">{listError}</Alert>
        </div>
      )}

      {(businessLoading || loading) && <EmployeesSkeleton />}

      {view === "active" && !businessLoading && !loading && selectedId && employees.length === 0 && !listError && (
        <EmptyState
          title={t("employees.noMembersYet", { members: terms.lowerMany })}
          action={
            mayManageMembers ? (
              <Button variant="primary" onClick={() => setShowEmployee(true)}>
                {terms.addCta}
              </Button>
            ) : undefined
          }
        />
      )}

      {view === "active" && !loading && employees.length > 0 && selectedId && (
        <div className="ds-table-wrap employees-table-wrap">
          <table className="ds-table employees-table">
            <thead>
              <tr>
                <th>{t("employees.table.name")}</th>
                <th>{t("employees.table.login")}</th>
                <th>{t("employees.table.role")}</th>
                <th>{t("employees.table.monitoring")}</th>
                <th>{t("employees.table.currentApp")}</th>
                <th>{t("employees.table.lastSeen")}</th>
                <th aria-label={t("employees.actions.more")} />
              </tr>
            </thead>
            <tbody>
              {employees.map((employee) => {
                const state = presence(employee);
                const isOwner = employee.role === "owner";
                const isSelf = employee.id === selected?.owner_user_id;
                const isPeerAdmin =
                  selected?.role === "admin" && employee.role === "admin";
                const mayManageThis = mayManageMembers && !isPeerAdmin && !isOwner;
                const mayChangeRole = mayManageRoles && !isOwner;
                const statusLabel = t(`employees.status.${state}`);
                const showCurrentApp =
                  (state === "active" || state === "idle") && employee.current_app;

                return (
                  <tr
                    key={employee.id}
                    className="employees-row"
                    onClick={(event) => {
                      const target = event.target as HTMLElement;
                      if (!event.currentTarget.contains(target)) return;
                      if (
                        target.closest(
                          "button, a, input, select, label, [role='button'], [role='option'], [data-no-row-nav]",
                        )
                      ) {
                        return;
                      }
                      navigate(`/employees/${employee.id}?business=${selectedId}`);
                    }}
                  >
                    <td>
                      <Link
                        className="employees-person employees-person-link"
                        to={`/employees/${employee.id}?business=${selectedId}`}
                      >
                        <span className="employees-avatar">
                          {initials(employee.display_name)}
                          <span
                            className={`employees-avatar__dot employees-avatar__dot--${state}`}
                          />
                        </span>
                        <span className="employees-person__copy">
                          <span className="employees-person__name-row">
                            <span className="employees-person__name">
                              {employee.display_name}
                            </span>
                            {isSelf && (
                              <Badge tone="neutral" className="employees-self-badge">
                                {t("dashboard.selfBadge")}
                              </Badge>
                            )}
                          </span>
                          <span className="employees-person__status">{statusLabel}</span>
                        </span>
                      </Link>
                    </td>
                    <td className="employees-login">
                      {employee.email || employee.username || "—"}
                    </td>
                    <td>
                      <MemberRoleControl
                        employee={employee}
                        businessId={selectedId}
                        canChange={mayChangeRole && employee.status !== "blocked"}
                        onChanged={() => loadEmployees(selectedId)}
                      />
                    </td>
                    <td>
                      <MemberMonitoringControl
                        employee={employee}
                        businessId={selectedId}
                        canChange={mayManageThis && employee.status !== "blocked"}
                        onChanged={() => loadEmployees(selectedId)}
                      />
                    </td>
                    <td>
                      <div className="employees-app">
                        <div className="employees-app__name">
                          {showCurrentApp ? employee.current_app : "—"}
                        </div>
                        {showCurrentApp && employee.current_window && (
                          <div className="employees-app__window">
                            {employee.current_window}
                          </div>
                        )}
                      </div>
                    </td>
                    <td className="employees-last-seen">
                      {relativeTime(employee.last_seen)}
                    </td>
                    <td>
                      <EmployeeActionsMenu
                        employee={employee}
                        businessId={selectedId}
                        canManage={mayManageThis}
                        canDelete={mayManageRoles && !isOwner}
                        onChanged={() => loadEmployees(selectedId)}
                      />
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}

      {view === "former" && !loading && selectedId && formerEmployees.length === 0 && !listError && (
        <EmptyState
          title={t("employees.lifecycle.noFormer")}
          description={t("employees.lifecycle.noFormerDescription")}
        />
      )}

      {view === "former" && !loading && selectedId && formerEmployees.length > 0 && (
        <div className="ds-table-wrap employees-table-wrap">
          <table className="ds-table employees-table employees-table--former">
            <thead>
              <tr>
                <th>{t("employees.table.name")}</th>
                <th>{t("employees.table.login")}</th>
                <th>{t("employees.table.role")}</th>
                <th>{t("employees.lifecycle.removedAt")}</th>
                <th>{t("employees.table.lastSeen")}</th>
                <th aria-label={t("employees.actions.more")} />
              </tr>
            </thead>
            <tbody>
              {formerEmployees.map((employee) => (
                <tr
                  key={employee.id}
                  className="employees-row employees-row--former"
                  onClick={(event) => {
                    const target = event.target as HTMLElement;
                    if (!event.currentTarget.contains(target)) return;
                    if (
                      target.closest(
                        "button, a, input, select, label, [role='button'], [role='option'], [data-no-row-nav]",
                      )
                    ) {
                      return;
                    }
                    navigate(
                      `/employees/${employee.id}?business=${selectedId}&former=1`,
                    );
                  }}
                >
                  <td>
                    <Link
                      className="employees-person employees-person-link"
                      to={`/employees/${employee.id}?business=${selectedId}&former=1`}
                    >
                      <span className="employees-avatar employees-avatar--former">
                        {initials(employee.display_name)}
                      </span>
                      <span className="employees-person__copy">
                        <span className="employees-person__name">{employee.display_name}</span>
                        <span className="employees-person__status">
                          {t("employees.lifecycle.removed")}
                        </span>
                      </span>
                    </Link>
                  </td>
                  <td className="employees-login">{employee.email || employee.username || "—"}</td>
                  <td>{employee.role ? t(`employees.roles.${employee.role}`) : "—"}</td>
                  <td className="employees-last-seen">
                    {employee.removed_at ? new Date(employee.removed_at).toLocaleString() : "—"}
                  </td>
                  <td className="employees-last-seen">{relativeTime(employee.last_seen)}</td>
                  <td>
                    <FormerMemberActions
                      employee={employee}
                      businessId={selectedId}
                      canRestore={mayManageMembers}
                      canDelete={mayManageRoles}
                      onChanged={() => loadFormer(selectedId)}
                    />
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showBusiness && (
        <NewBusinessDialog
          terms={terms}
          kind={selected?.kind}
          onClose={() => setShowBusiness(false)}
          onCreated={async (id) => {
            setShowBusiness(false);
            await reloadBusinesses();
            setSelectedId(id);
          }}
        />
      )}

      {showEmployee && mayManageMembers && selectedId && (
        <NewEmployeeDialog
          businessId={selectedId}
          terms={terms}
          onClose={() => setShowEmployee(false)}
          onCreated={async (businessId) => {
            setShowEmployee(false);
            loadEmployees(businessId);
            pushToast({ title: t("employees.prompts.saved"), tone: "success" });
          }}
        />
      )}
    </div>
  );
}
