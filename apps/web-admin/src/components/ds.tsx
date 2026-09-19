import {
  forwardRef,
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type CSSProperties,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
} from "react";
import { createPortal } from "react-dom";

export function cx(...values: Array<string | false | null | undefined>): string {
  return values.filter(Boolean).join(" ");
}

export type ButtonVariant =
  | "primary"
  | "secondary"
  | "ghost"
  | "danger"
  | "danger-ghost";

export type ButtonSize = "sm" | "md" | "lg";

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
  leadingIcon?: ReactNode;
};

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  {
    variant = "secondary",
    size = "md",
    loading = false,
    leadingIcon,
    className,
    children,
    disabled,
    type = "button",
    ...props
  },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      className={cx(
        "ds-button",
        `ds-button--${size}`,
        `ds-button--${variant}`,
        className,
      )}
      aria-busy={loading || undefined}
      disabled={disabled || loading}
      {...props}
    >
      {loading ? <span className="ds-button__spinner" aria-hidden /> : leadingIcon}
      <span>{children}</span>
    </button>
  );
});

type IconButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  label: string;
  bordered?: boolean;
};

export const IconButton = forwardRef<HTMLButtonElement, IconButtonProps>(
  function IconButton(
    { label, bordered = false, className, children, type = "button", ...props },
    ref,
  ) {
    return (
      <button
        ref={ref}
        type={type}
        className={cx(
          "ds-icon-button",
          bordered && "ds-icon-button--bordered",
          className,
        )}
        aria-label={label}
        title={label}
        {...props}
      >
        {children}
      </button>
    );
  },
);

export function Card({
  children,
  compact = false,
  className,
}: {
  children: ReactNode;
  compact?: boolean;
  className?: string;
}) {
  return (
    <section className={cx("ds-card", compact && "ds-card--compact", className)}>
      {children}
    </section>
  );
}

export type BadgeTone = "neutral" | "brand" | "success" | "warning" | "danger" | "info";

export function Badge({
  children,
  tone = "neutral",
  className,
}: {
  children: ReactNode;
  tone?: BadgeTone;
  className?: string;
}) {
  return <span className={cx("ds-badge", `ds-badge--${tone}`, className)}>{children}</span>;
}

type FieldFrameProps = {
  label: string;
  description?: string;
  error?: string;
  htmlFor: string;
  children: ReactNode;
};

export function FieldFrame({
  label,
  description,
  error,
  htmlFor,
  children,
}: FieldFrameProps) {
  return (
    <div className="ds-field">
      <label className="ds-field__label" htmlFor={htmlFor}>
        {label}
      </label>
      {description && (
        <p id={`${htmlFor}-description`} className="ds-field__description">
          {description}
        </p>
      )}
      {children}
      {error && (
        <p
          id={`${htmlFor}-error`}
          className="ds-field__message ds-field__message--error"
          role="alert"
        >
          {error}
        </p>
      )}
    </div>
  );
}

type TextFieldProps = Omit<InputHTMLAttributes<HTMLInputElement>, "id"> & {
  id: string;
  label: string;
  description?: string;
  error?: string;
};

export const TextField = forwardRef<HTMLInputElement, TextFieldProps>(
  function TextField({ id, label, description, error, className, ...props }, ref) {
    return (
      <FieldFrame
        htmlFor={id}
        label={label}
        description={description}
        error={error}
      >
        <input
          ref={ref}
          id={id}
          className={cx("ds-input", className)}
          aria-invalid={Boolean(error) || undefined}
          aria-describedby={error ? `${id}-error` : description ? `${id}-description` : undefined}
          {...props}
        />
      </FieldFrame>
    );
  },
);

type SelectFieldProps = Omit<SelectHTMLAttributes<HTMLSelectElement>, "id"> & {
  id: string;
  label: string;
  description?: string;
  error?: string;
};

export const SelectField = forwardRef<HTMLSelectElement, SelectFieldProps>(
  function SelectField(
    { id, label, description, error, className, children, ...props },
    ref,
  ) {
    return (
      <FieldFrame
        htmlFor={id}
        label={label}
        description={description}
        error={error}
      >
        <select
          ref={ref}
          id={id}
          className={cx("ds-select", className)}
          aria-invalid={Boolean(error) || undefined}
          {...props}
        >
          {children}
        </select>
      </FieldFrame>
    );
  },
);

export type SelectMenuOption<T extends string = string> = {
  value: T;
  label: string;
};

export function SelectMenu<T extends string>({
  id,
  label,
  description,
  value,
  options,
  onChange,
  disabled = false,
  className,
  ariaLabel,
  searchable = false,
  searchPlaceholder = "Search…",
  emptyText = "No matches",
  menuWidth,
}: {
  id: string;
  label?: string;
  description?: string;
  value: T;
  options: Array<SelectMenuOption<T>>;
  onChange: (value: T) => void;
  disabled?: boolean;
  className?: string;
  ariaLabel?: string;
  searchable?: boolean;
  searchPlaceholder?: string;
  emptyText?: string;
  menuWidth?: number;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const triggerRef = useRef<HTMLButtonElement>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const searchRef = useRef<HTMLInputElement>(null);
  const [menuStyle, setMenuStyle] = useState<CSSProperties>({
    position: "fixed",
    top: 0,
    left: 0,
    width: 220,
    maxHeight: 420,
    visibility: "hidden",
  });
  const selected = options.find((option) => option.value === value) ?? options[0];
  const normalizedQuery = query.trim().toLocaleLowerCase();
  const visibleOptions =
    searchable && normalizedQuery
      ? options.filter((option) =>
          option.label.toLocaleLowerCase().includes(normalizedQuery),
        )
      : options;

  useLayoutEffect(() => {
    if (!open) return;

    const gap = 6;
    const margin = 12;

    function positionMenu() {
      const trigger = triggerRef.current;
      if (!trigger) return;

      const rect = trigger.getBoundingClientRect();
      const width = Math.min(
        Math.max(menuWidth ?? rect.width, rect.width, 200),
        Math.max(200, window.innerWidth - margin * 2),
      );
      const measuredHeight = menuRef.current?.scrollHeight ?? 320;
      const spaceBelow = Math.max(120, window.innerHeight - rect.bottom - gap - margin);
      const spaceAbove = Math.max(120, rect.top - gap - margin);
      const openAbove = spaceBelow < Math.min(measuredHeight, 300) && spaceAbove > spaceBelow;
      const available = openAbove ? spaceAbove : spaceBelow;
      const maxHeight = Math.min(460, available);
      const left = Math.min(
        Math.max(margin, rect.left),
        Math.max(margin, window.innerWidth - width - margin),
      );
      const top = openAbove
        ? Math.max(margin, rect.top - gap - Math.min(measuredHeight, maxHeight))
        : rect.bottom + gap;

      setMenuStyle({
        position: "fixed",
        top,
        left,
        width,
        maxHeight,
        zIndex: 1400,
        visibility: "visible",
      });
    }

    function onPointerDown(event: MouseEvent) {
      const target = event.target as Node;
      if (triggerRef.current?.contains(target) || menuRef.current?.contains(target)) return;
      setOpen(false);
    }

    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") setOpen(false);
    }

    const frame = requestAnimationFrame(() => {
      positionMenu();
      if (searchable) searchRef.current?.focus();
    });
    window.addEventListener("resize", positionMenu);
    document.addEventListener("scroll", positionMenu, true);
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("keydown", onKeyDown);

    return () => {
      cancelAnimationFrame(frame);
      window.removeEventListener("resize", positionMenu);
      document.removeEventListener("scroll", positionMenu, true);
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("keydown", onKeyDown);
    };
  }, [menuWidth, open, searchable]);

  function toggleOpen() {
    setOpen((current) => {
      if (!current) setQuery("");
      return !current;
    });
  }

  const control = (
    <div className={cx("ds-popover-select", className)}>
      <button
        ref={triggerRef}
        id={id}
        type="button"
        className="ds-popover-select__trigger"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={ariaLabel}
        disabled={disabled}
        onClick={toggleOpen}
        onKeyDown={(event) => {
          if (event.key === "ArrowDown" || event.key === "Enter" || event.key === " ") {
            event.preventDefault();
            if (!open) setQuery("");
            setOpen(true);
          }
        }}
      >
        <span className="ds-popover-select__value">{selected?.label ?? ""}</span>
        <svg
          className="ds-popover-select__chevron"
          viewBox="0 0 20 20"
          width="16"
          height="16"
          aria-hidden
        >
          <path
            d="m6 8 4 4 4-4"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.8"
            strokeLinecap="round"
            strokeLinejoin="round"
          />
        </svg>
      </button>

      {open && createPortal(
        <div
          ref={menuRef}
          className="ds-popover-select__menu"
          role="listbox"
          aria-labelledby={id}
          style={menuStyle}
        >
          {searchable && (
            <div className="ds-popover-select__search-wrap">
              <input
                ref={searchRef}
                type="search"
                className="ds-input ds-popover-select__search"
                value={query}
                placeholder={searchPlaceholder}
                aria-label={searchPlaceholder}
                onChange={(event) => setQuery(event.currentTarget.value)}
              />
            </div>
          )}
          <div className="ds-popover-select__options">
            {visibleOptions.length === 0 ? (
              <div className="ds-popover-select__empty">{emptyText}</div>
            ) : (
              visibleOptions.map((option) => {
                const active = option.value === value;
                return (
                  <button
                    key={option.value}
                    type="button"
                    role="option"
                    aria-selected={active}
                    className={cx("ds-popover-select__option", active && "is-active")}
                    onClick={() => {
                      onChange(option.value);
                      setOpen(false);
                    }}
                  >
                    <span>{option.label}</span>
                    {active && <span className="ds-popover-select__check">✓</span>}
                  </button>
                );
              })
            )}
          </div>
        </div>,
        document.body,
      )}
    </div>
  );

  if (!label) return control;

  return (
    <FieldFrame htmlFor={id} label={label} description={description}>
      {control}
    </FieldFrame>
  );
}

export function Switch({
  checked,
  onCheckedChange,
  label,
  disabled = false,
  name,
}: {
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
  label: string;
  disabled?: boolean;
  name?: string;
}) {
  return (
    <label className="ds-switch-row">
      <input
        className="ds-switch"
        type="checkbox"
        role="switch"
        name={name}
        checked={checked}
        disabled={disabled}
        onChange={(event) => onCheckedChange(event.currentTarget.checked)}
      />
      <span className="ds-switch__label">{label}</span>
    </label>
  );
}

export function PageHeader({
  title,
  subtitle,
  actions,
}: {
  title: ReactNode;
  subtitle?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <header className="ds-page-header">
      <div className="ds-page-header__copy">
        <h1 className="ds-page-title">{title}</h1>
        {subtitle && <p className="ds-page-subtitle">{subtitle}</p>}
      </div>
      {actions && <div className="ds-page-header__actions">{actions}</div>}
    </header>
  );
}

export function Skeleton({
  width = "100%",
  height = 16,
  className,
}: {
  width?: number | string;
  height?: number | string;
  className?: string;
}) {
  return (
    <span
      className={cx("ds-skeleton", className)}
      aria-hidden
      style={{ width, height }}
    />
  );
}

export function EmptyState({
  title,
  description,
  action,
}: {
  title: ReactNode;
  description?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="ds-empty">
      <div className="ds-empty__content">
        <div className="ds-empty__title">{title}</div>
        {description && (
          <div className="ds-empty__description">{description}</div>
        )}
        {action && <div className="ds-empty__action">{action}</div>}
      </div>
    </div>
  );
}


export type AlertTone = "info" | "success" | "warning" | "danger";

export function Alert({
  children,
  tone = "info",
  className,
}: {
  children: ReactNode;
  tone?: AlertTone;
  className?: string;
}) {
  return (
    <div
      className={cx("ds-alert", `ds-alert--${tone}`, className)}
      role={tone === "danger" ? "alert" : "status"}
    >
      {children}
    </div>
  );
}

export function Dialog({
  title,
  onClose,
  children,
  footer,
  size = "default",
  closeOnBackdrop = true,
}: {
  title: ReactNode;
  onClose: () => void;
  children: ReactNode;
  footer?: ReactNode;
  size?: "confirm" | "default" | "complex" | "wide" | "workspace";
  closeOnBackdrop?: boolean;
}) {
  const titleId = useId();

  useEffect(() => {
    function onKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [onClose]);

  return createPortal(
    <div
      className="ds-modal-backdrop"
      onMouseDown={(event) => {
        if (closeOnBackdrop && event.currentTarget === event.target) onClose();
      }}
    >
      <div
        className={cx(
          "ds-modal",
          size === "confirm" && "ds-modal--confirm",
          size === "complex" && "ds-modal--complex",
          size === "wide" && "ds-modal--wide",
          size === "workspace" && "ds-modal--workspace",
        )}
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
      >
        <div className="ds-modal__header">
          <h2 id={titleId} className="ds-modal__title">
            {title}
          </h2>
        </div>
        <div className="ds-modal__body">{children}</div>
        {footer && <div className="ds-modal__footer">{footer}</div>}
      </div>
    </div>,
    document.body,
  );
}
