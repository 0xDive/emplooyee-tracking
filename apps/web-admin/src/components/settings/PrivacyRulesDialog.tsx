import { useEffect, useMemo, useState, type FormEvent } from "react";
import { useTranslation } from "react-i18next";
import {
  createPrivacyRule,
  deletePrivacyRule,
  getPrivacyApps,
  listPrivacyRules,
  updatePrivacyRule,
} from "../../api/endpoints";
import type { PrivacyAppCategory, PrivacyRule } from "../../api/types";
import { Alert, Button, Dialog, Skeleton, TextField } from "../ds";
import { useToast } from "../ToastProvider";

function normalized(value: string) {
  return value.trim().toLowerCase();
}

export function PrivacyRulesDialog({
  businessId,
  open,
  onClose,
}: {
  businessId: string;
  open: boolean;
  onClose: () => void;
}) {
  const { t } = useTranslation("settings");
  const { pushToast } = useToast();

  const [rules, setRules] = useState<PrivacyRule[]>([]);
  const [categories, setCategories] = useState<PrivacyAppCategory[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [customApp, setCustomApp] = useState("");
  const [windowTitle, setWindowTitle] = useState("");

  async function reload() {
    setLoading(true);
    setError(null);
    try {
      const [ruleResponse, appResponse] = await Promise.all([
        listPrivacyRules(businessId),
        getPrivacyApps(),
      ]);
      setRules(ruleResponse.rules);
      setCategories(appResponse.categories);
    } catch {
      setError(t("privacyRules.loadFailed"));
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    if (!open) return;
    void reload();
  }, [businessId, open]);

  const suggestedApps = useMemo(
    () =>
      new Set(
        categories.flatMap((category) =>
          category.apps.map((app) => normalized(app)),
        ),
      ),
    [categories],
  );

  const exactAppRules = rules.filter(
    (rule) => rule.kind === "app" && rule.match_type === "exact",
  );
  const customAppRules = exactAppRules.filter(
    (rule) => rule.enabled && !suggestedApps.has(normalized(rule.pattern)),
  );
  const titleRules = rules.filter(
    (rule) => rule.kind === "window_title" && rule.enabled,
  );

  function findAppRule(app: string) {
    const key = normalized(app);
    return exactAppRules.find((rule) => normalized(rule.pattern) === key);
  }

  function hasApp(app: string) {
    return Boolean(findAppRule(app)?.enabled);
  }

  async function applyAppSelection(app: string, selected: boolean) {
    const existing = findAppRule(app);

    if (selected) {
      if (existing) {
        if (existing.enabled) return;
        const response = await updatePrivacyRule(businessId, existing.id, {
          enabled: true,
        });
        setRules((current) =>
          current.map((rule) =>
            rule.id === existing.id ? response.rule : rule,
          ),
        );
        return;
      }

      const response = await createPrivacyRule(businessId, {
        kind: "app",
        match_type: "exact",
        pattern: app,
      });
      setRules((current) => [...current, response.rule]);
      return;
    }

    if (!existing) return;
    await deletePrivacyRule(businessId, existing.id);
    setRules((current) => current.filter((rule) => rule.id !== existing.id));
  }

  async function toggleApp(app: string) {
    setSaving(true);
    setError(null);
    try {
      await applyAppSelection(app, !hasApp(app));
      pushToast({ title: t("skipApps.saved"), tone: "success" });
    } catch {
      setError(t("privacyRules.saveFailed"));
    } finally {
      setSaving(false);
    }
  }

  async function setCategory(category: PrivacyAppCategory, selected: boolean) {
    setSaving(true);
    setError(null);
    try {
      await Promise.all(
        category.apps.map((app) => applyAppSelection(app, selected)),
      );
      pushToast({ title: t("skipApps.saved"), tone: "success" });
    } catch {
      setError(t("privacyRules.saveFailed"));
      await reload();
    } finally {
      setSaving(false);
    }
  }

  async function addCustomApp(event: FormEvent) {
    event.preventDefault();
    const app = customApp.trim();
    if (!app) return;

    setSaving(true);
    setError(null);
    try {
      await applyAppSelection(app, true);
      setCustomApp("");
      pushToast({ title: t("skipApps.saved"), tone: "success" });
    } catch {
      setError(t("privacyRules.saveFailed"));
    } finally {
      setSaving(false);
    }
  }

  async function addWindowTitle(event: FormEvent) {
    event.preventDefault();
    const pattern = windowTitle.trim();
    if (!pattern) return;

    const existing = rules.find(
      (rule) =>
        rule.kind === "window_title" &&
        rule.match_type === "contains" &&
        normalized(rule.pattern) === normalized(pattern),
    );

    setSaving(true);
    setError(null);
    try {
      if (existing) {
        if (!existing.enabled) {
          const response = await updatePrivacyRule(businessId, existing.id, {
            enabled: true,
          });
          setRules((current) =>
            current.map((rule) =>
              rule.id === existing.id ? response.rule : rule,
            ),
          );
        }
      } else {
        const response = await createPrivacyRule(businessId, {
          kind: "window_title",
          match_type: "contains",
          pattern,
        });
        setRules((current) => [...current, response.rule]);
      }
      setWindowTitle("");
      pushToast({ title: t("privacyRules.created"), tone: "success" });
    } catch {
      setError(t("privacyRules.saveFailed"));
    } finally {
      setSaving(false);
    }
  }

  async function removeRule(rule: PrivacyRule) {
    setSaving(true);
    setError(null);
    try {
      await deletePrivacyRule(businessId, rule.id);
      setRules((current) => current.filter((item) => item.id !== rule.id));
      pushToast({ title: t("privacyRules.deleted"), tone: "success" });
    } catch {
      setError(t("privacyRules.deleteFailed"));
    } finally {
      setSaving(false);
    }
  }

  if (!open) return null;

  return (
    <Dialog
      title={t("skipApps.modalTitle")}
      size="complex"
      onClose={() => !saving && onClose()}
      closeOnBackdrop={!saving}
      footer={
        <Button variant="primary" disabled={saving} onClick={onClose}>
          {t("skipApps.done")}
        </Button>
      }
    >
      <div className="settings-dialog-stack privacy-rules">
        <p className="settings-section__description">{t("skipApps.desc")}</p>

        <form
          className="settings-inline privacy-rules__app-input"
          onSubmit={addCustomApp}
        >
          <div className="settings-skip-input">
            <TextField
              id="privacy-app-custom"
              label={t("skipApps.custom")}
              value={customApp}
              placeholder={t("skipApps.placeholder")}
              disabled={saving}
              onChange={(event) => setCustomApp(event.target.value)}
            />
          </div>
          <Button
            type="submit"
            variant="secondary"
            disabled={saving || !customApp.trim()}
          >
            {t("skipApps.add")}
          </Button>
        </form>

        {error && <Alert tone="danger">{error}</Alert>}

        {loading ? (
          <div className="privacy-rules__loading">
            <Skeleton height={36} />
            <Skeleton height={110} />
            <Skeleton height={110} />
          </div>
        ) : (
          <>
            {customAppRules.length > 0 && (
              <div>
                <div className="settings-row__title">{t("skipApps.custom")}</div>
                <div className="settings-chip-group settings-chip-group--top">
                  {customAppRules.map((rule) => (
                    <button
                      key={rule.id}
                      type="button"
                      className="settings-chip is-active"
                      disabled={saving}
                      aria-label={t("skipApps.remove", { name: rule.pattern })}
                      onClick={() => void removeRule(rule)}
                    >
                      {rule.pattern} ×
                    </button>
                  ))}
                </div>
              </div>
            )}

            <div className="settings-skip-list">
              {categories.map((category) => {
                const selectedCount = category.apps.filter(hasApp).length;
                return (
                  <div className="settings-skip-category" key={category.key}>
                    <div className="settings-skip-category__head">
                      <span className="settings-skip-category__name">
                        {t("skipApps.cat" + category.key)} ({selectedCount}/
                        {category.apps.length})
                      </span>

                      {selectedCount < category.apps.length && (
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={saving}
                          onClick={() => void setCategory(category, true)}
                        >
                          {t("skipApps.addAll")}
                        </Button>
                      )}

                      {selectedCount > 0 && (
                        <Button
                          variant="ghost"
                          size="sm"
                          disabled={saving}
                          onClick={() => void setCategory(category, false)}
                        >
                          {t("skipApps.removeAll")}
                        </Button>
                      )}
                    </div>

                    <div className="settings-chip-group">
                      {category.apps.map((app) => {
                        const active = hasApp(app);
                        return (
                          <button
                            key={app}
                            type="button"
                            role="checkbox"
                            aria-checked={active}
                            className={
                              active ? "settings-chip is-active" : "settings-chip"
                            }
                            disabled={saving}
                            onClick={() => void toggleApp(app)}
                          >
                            {active ? "✓ " : "+ "}
                            {app}
                          </button>
                        );
                      })}
                    </div>
                  </div>
                );
              })}
            </div>

            <details className="privacy-rules__advanced">
              <summary>{t("privacyRules.groups.windowTitles")}</summary>
              <p>{t("privacyRules.windowTitlesHelp")}</p>

              <form
                className="settings-inline privacy-rules__app-input"
                onSubmit={addWindowTitle}
              >
                <div className="settings-skip-input">
                  <TextField
                    id="privacy-window-title"
                    label={t("privacyRules.pattern")}
                    value={windowTitle}
                    placeholder={t("privacyRules.titlePlaceholder")}
                    disabled={saving}
                    onChange={(event) => setWindowTitle(event.target.value)}
                  />
                </div>
                <Button
                  type="submit"
                  variant="secondary"
                  disabled={saving || !windowTitle.trim()}
                >
                  {t("privacyRules.add")}
                </Button>
              </form>

              {titleRules.length > 0 && (
                <div className="settings-chip-group">
                  {titleRules.map((rule) => (
                    <button
                      key={rule.id}
                      type="button"
                      className="settings-chip is-active"
                      disabled={saving}
                      aria-label={t("skipApps.remove", { name: rule.pattern })}
                      onClick={() => void removeRule(rule)}
                    >
                      {rule.pattern} ×
                    </button>
                  ))}
                </div>
              )}
            </details>
          </>
        )}
      </div>
    </Dialog>
  );
}
