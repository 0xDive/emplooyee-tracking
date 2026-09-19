//! Persisted user settings (`settings.json` in the app data dir).
//!
//! On startup these are loaded and applied to `TrackerControl` (which the trackers
//! and ingest server read live). The UI reads/writes them via commands.

use std::path::Path;
use std::sync::Mutex;

use serde::{Deserialize, Serialize};

use crate::trackers::{
    DEFAULT_IDLE_THRESHOLD_S, DEFAULT_RETENTION_DAYS, DEFAULT_SCREENSHOT_INTERVAL_S,
};

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Settings {
    /// "light" | "dark" | "system" — applied by the UI.
    pub theme: String,
    pub idle_threshold_s: u64,
    pub screenshot_interval_s: u64,
    pub screenshot_retention_days: u64,
    /// Store only the site origin for browser visits, not the full URL.
    pub domain_only: bool,
    /// Organization-controlled identifying/detail collection switches. Active/idle
    /// remains mandatory in managed mode even when app identity is disabled.
    #[serde(default = "default_true")]
    pub collect_app_activity: bool,
    #[serde(default = "default_true")]
    pub collect_window_titles: bool,
    #[serde(default = "default_true")]
    pub collect_browser_activity: bool,
    /// Run as a menu-bar-only app (no Dock icon).
    #[serde(default)]
    pub hide_dock: bool,
    /// Capture periodic screenshots. User opt-out (Settings). Default on.
    #[serde(default = "default_true")]
    pub capture_screenshots: bool,
    /// "privacy" (frontmost window only — the default) | "normal" (one shot per
    /// display). Window shots fall back to full screen when the window can't be
    /// captured. Pre-rename values ("full_screen"/"active_window") still parse.
    #[serde(default = "default_screenshot_mode")]
    pub screenshot_mode: String,
    /// Explicit screenshot scope: active_window | active_display | all_displays.
    /// Missing on older settings files and normalized from screenshot_mode on load.
    #[serde(default)]
    pub screenshot_capture_scope: String,
    /// Last server-confirmed privacy rules. Cached locally so an offline restart
    /// keeps the same pre-capture exclusions instead of widening capture.
    #[serde(default)]
    pub screenshot_privacy_rules: Vec<crate::sync::client::PrivacyRule>,
    /// App names for which the capture tick is skipped entirely while that app is
    /// frontmost (case-insensitive whole-word match on the active window's app
    /// name). Prefilled with the curated sensitive-app rules; user-editable.
    #[serde(default = "default_skip_apps")]
    pub screenshot_skip_apps: Vec<String>,
    /// Count keystrokes (counts only, never keys). User opt-out (Settings). Default on.
    #[serde(default = "default_true")]
    pub count_keystrokes: bool,
    /// First-run consent acknowledged. Windows gates capture on this (no per-feature
    /// OS prompts there); macOS relies on TCC instead and ignores it. Default off.
    #[serde(default)]
    pub consented: bool,
    /// Personal mode: the user chose "Just me" on the welcome screen and runs fully
    /// local with no backend account. Skips the login screen entirely. Default off.
    #[serde(default)]
    pub local_only: bool,
    /// Last organization whose managed collection owned newly-created local rows.
    /// Kept across logout so re-auth to the same organization does not discard an
    /// offline managed backlog.
    #[serde(default)]
    pub last_managed_business_id: Option<String>,
    /// True when local/unbound collection may have created rows since the last
    /// managed scope. The next managed binding suppresses those pending rows.
    #[serde(default)]
    pub collection_scope_dirty: bool,
    /// First-run onboarding flow finished (welcome → toggles → permissions). Default
    /// off so onboarding shows once per install.
    #[serde(default)]
    pub onboarding_completed: bool,
    /// Stable per-install device identifier (UUID), created on first run and never
    /// changed. Sent with auth + sync so the backend can attribute rows to a device.
    #[serde(default)]
    pub device_id: String,
    /// UI language code (e.g. "en", "zh", "ja"). Persisted so the native side
    /// (tray, notifications) can localize to match the in-app choice. Default "en".
    #[serde(default = "default_locale")]
    pub locale: String,
    /// Last server-confirmed membership collection state. Persisted so an offline
    /// restart never silently re-enables monitoring that an administrator disabled.
    #[serde(default = "default_true")]
    pub org_monitoring_enabled: bool,
}

fn default_true() -> bool {
    true
}

fn default_locale() -> String {
    "ru".into()
}

fn default_screenshot_mode() -> String {
    "privacy".into()
}

fn default_skip_apps() -> Vec<String> {
    crate::trackers::default_privacy_apps_flat()
}

/// Compile-time default backend, chosen by Cargo feature (see Cargo.toml `[features]`).
/// Resolution order: local > staging > production (the default). Not stored in
/// settings (so a stale settings.json can't pin it to the wrong env).
const DEFAULT_BACKEND_URL: &str = if cfg!(feature = "local") {
    "http://localhost:8090"
} else if cfg!(feature = "staging") {
    // Private pre-prod host — set via ACTILENS_BACKEND_URL at runtime, or edit locally.
    "https://staging.example.com"
} else {
    // production (default)
    "http://127.0.0.1:8081"
};

/// Compile-time environment label (matches the backend-URL feature resolution).
/// Reported to Sentry so events are grouped by deploy environment.
pub fn env_label() -> &'static str {
    if cfg!(feature = "local") {
        "local"
    } else if cfg!(feature = "staging") {
        "staging"
    } else {
        "production"
    }
}

#[cfg(target_os = "windows")]
fn windows_user_environment(name: &str) -> Option<String> {
    use winreg::enums::HKEY_CURRENT_USER;
    use winreg::RegKey;

    let hkcu = RegKey::predef(HKEY_CURRENT_USER);
    let environment = hkcu.open_subkey("Environment").ok()?;
    let value: String = environment.get_value(name).ok()?;
    let value = value.trim().to_string();
    (!value.is_empty()).then_some(value)
}

#[cfg(not(target_os = "windows"))]
fn windows_user_environment(_name: &str) -> Option<String> {
    None
}

/// Base URL of the sync backend. Resolution order:
/// process environment -> Windows HKCU user environment -> compile-time custom
/// build URL -> local fallback. Reading HKCU directly means an administrator can
/// provision a server URL without requiring a Windows sign-out or Explorer restart.
pub fn backend_base_url() -> String {
    std::env::var("ACTILENS_BACKEND_URL")
        .ok()
        .filter(|s| !s.trim().is_empty())
        .or_else(|| windows_user_environment("ACTILENS_BACKEND_URL"))
        .or_else(|| {
            option_env!("ACTILENS_BUILD_SERVER_URL")
                .map(str::to_string)
                .filter(|s| !s.is_empty())
        })
        .unwrap_or_else(|| DEFAULT_BACKEND_URL.to_string())
}

impl Default for Settings {
    fn default() -> Self {
        Settings {
            theme: "system".into(),
            idle_threshold_s: DEFAULT_IDLE_THRESHOLD_S,
            screenshot_interval_s: DEFAULT_SCREENSHOT_INTERVAL_S,
            screenshot_retention_days: DEFAULT_RETENTION_DAYS,
            domain_only: false,
            collect_app_activity: true,
            collect_window_titles: true,
            collect_browser_activity: true,
            hide_dock: false,
            capture_screenshots: true,
            screenshot_mode: default_screenshot_mode(),
            screenshot_capture_scope: "active_window".into(),
            screenshot_privacy_rules: Vec::new(),
            screenshot_skip_apps: default_skip_apps(),
            count_keystrokes: true,
            consented: false,
            local_only: false,
            last_managed_business_id: None,
            collection_scope_dirty: false,
            onboarding_completed: false,
            device_id: String::new(),
            locale: default_locale(),
            org_monitoring_enabled: true,
        }
    }
}

impl Settings {
    /// Whether pending local rows must be quarantined before binding to a managed
    /// organization. An unknown previous scope alone is not enough: clean installs
    /// and upgrades with no local-mode activity must not lose legitimate backlog.
    pub fn needs_managed_scope_suppression(&self, business_id: &str) -> bool {
        self.local_only
            || self.collection_scope_dirty
            || self
                .last_managed_business_id
                .as_deref()
                .is_some_and(|previous| previous != business_id)
    }
}

/// Load settings from `path`, falling back to defaults if missing/invalid.
pub fn load(path: &Path) -> Settings {
    let mut settings: Settings = std::fs::read_to_string(path)
        .ok()
        .and_then(|s| serde_json::from_str(&s).ok())
        .unwrap_or_default();
    // Upgrade older settings without changing their effective screenshot behavior.
    // The old "normal" mode captured every display; privacy/active_window captured
    // only the foreground window.
    if settings.screenshot_capture_scope.trim().is_empty() {
        settings.screenshot_capture_scope = match settings.screenshot_mode.as_str() {
            "normal" | "full_screen" => "all_displays".into(),
            _ => "active_window".into(),
        };
    }
    settings
}

/// Load settings and guarantee a stable `device_id`. On first run (or an upgrade
/// from a config that predates the field) a fresh UUID is generated and persisted
/// so it stays identical across restarts.
pub fn load_with_device_id(path: &Path) -> Settings {
    let mut s = load(path);
    if s.device_id.is_empty() {
        s.device_id = uuid::Uuid::new_v4().to_string();
        let _ = save(path, &s);
    }
    s
}

/// Persist settings to `path` (pretty JSON).
pub fn save(path: &Path, settings: &Settings) -> std::io::Result<()> {
    let json = serde_json::to_string_pretty(settings).unwrap_or_default();
    std::fs::write(path, json)
}

/// Push settings into the live `TrackerControl` the trackers + server read.
pub fn apply(s: &Settings, control: &crate::trackers::TrackerControl) {
    use std::sync::atomic::Ordering::Relaxed;
    control
        .org_monitoring_enabled
        .store(s.local_only || s.org_monitoring_enabled, Relaxed);
    control.idle_threshold_s.store(s.idle_threshold_s, Relaxed);
    control
        .screenshot_interval_s
        .store(s.screenshot_interval_s, Relaxed);
    control
        .screenshot_retention_days
        .store(s.screenshot_retention_days, Relaxed);
    control.domain_only.store(s.domain_only, Relaxed);
    control.collect_app_activity.store(s.collect_app_activity, Relaxed);
    control.collect_window_titles.store(s.collect_window_titles, Relaxed);
    control.collect_browser_activity.store(s.collect_browser_activity, Relaxed);
    let scope = if s.screenshot_capture_scope.trim().is_empty() {
        match s.screenshot_mode.as_str() {
            "normal" | "full_screen" => "all_displays",
            _ => "active_window",
        }
    } else {
        s.screenshot_capture_scope.as_str()
    };
    control
        .screenshot_mode
        .store(crate::trackers::shot_mode_from_str(scope), Relaxed);
    *control.screenshot_skip_apps.write().unwrap() = s.screenshot_skip_apps.clone();
    *control.screenshot_privacy_rules.write().unwrap() = s.screenshot_privacy_rules.clone();

    // Capture opt-outs. On Windows nothing captures until the user has consented
    // (there are no per-feature OS prompts); macOS relies on TCC and ignores consent.
    let consent_ok = !cfg!(target_os = "windows") || s.consented;
    control
        .capture_screenshots
        .store(s.capture_screenshots && consent_ok, Relaxed);
    control
        .count_keystrokes
        .store(s.count_keystrokes && consent_ok, Relaxed);
}

/// Apply one server policy snapshot atomically to persisted settings + live trackers.
/// Managed policy is authoritative; employee-local capture switches are ignored.
pub fn apply_managed_policy(
    state: &SettingsState,
    control: &crate::trackers::TrackerControl,
    policy: &crate::sync::client::Policy,
    monitoring_enabled: bool,
) -> CaptureManaged {
    use std::sync::atomic::Ordering::Relaxed;

    let status = CaptureManaged {
        managed: policy.managed,
        allow_employee_override: false,
        family: policy.kind.as_deref() == Some("family"),
        monitoring_enabled,
    };

    control.managed.store(policy.managed, Relaxed);
    control
        .org_monitoring_enabled
        .store(monitoring_enabled, Relaxed);
    *state.managed.lock().unwrap() = status;

    if policy.managed {
        let mut settings = state.current.lock().unwrap().clone();
        settings.local_only = false;
        settings.org_monitoring_enabled = monitoring_enabled;
        settings.collect_app_activity = policy.collect_app_activity;
        settings.collect_window_titles = policy.collect_window_titles;
        settings.collect_browser_activity = policy.collect_browser_activity;
        settings.capture_screenshots = policy.collect_screenshots;
        settings.count_keystrokes = policy.collect_keystroke_counts;

        if let Some(value) = policy.screenshot_interval_s {
            settings.screenshot_interval_s = value;
        }
        if let Some(value) = policy.idle_threshold_s {
            settings.idle_threshold_s = value;
        }
        // null server retention means indefinite. Local pruning must not become
        // more aggressive as a side effect, so keep the last finite local value.
        if let Some(value) = policy.screenshot_retention_days {
            settings.screenshot_retention_days = value;
        }
        if let Some(scope) = policy.screenshot_capture_scope.clone() {
            settings.screenshot_capture_scope = scope.clone();
            settings.screenshot_mode = if scope == "active_window" {
                "privacy".into()
            } else {
                "normal".into()
            };
        } else if let Some(mode) = policy.screenshot_mode.clone() {
            settings.screenshot_mode = mode.clone();
            settings.screenshot_capture_scope = match mode.as_str() {
                "active_display" => "active_display".into(),
                "normal" | "full_screen" | "all_displays" => "all_displays".into(),
                _ => "active_window".into(),
            };
        }
        if let Some(skip) = policy.screenshot_skip_apps.clone() {
            settings.screenshot_skip_apps = skip;
        }
        settings.screenshot_privacy_rules = policy.privacy_rules.clone();

        apply(&settings, control);
        let _ = save(&state.path, &settings);
        *state.current.lock().unwrap() = settings;
    } else {
        // Leaving managed mode restores the persisted local settings behavior.
        let settings = state.current.lock().unwrap().clone();
        apply(&settings, control);
    }

    status
}

/// Whether the org controls capture settings for the signed-in employee. Default
/// (unmanaged) lets the user edit freely — used for standalone users and before a
/// policy is fetched.
#[derive(Debug, Clone, Copy, Serialize)]
pub struct CaptureManaged {
    /// The user's org defines a capture policy.
    pub managed: bool,
    /// The org allows employees to override it anyway.
    pub allow_employee_override: bool,
    /// The org is a family (kind = 'family') — the onboarding shows "kid" copy.
    pub family: bool,
    /// Server-controlled membership collection switch.
    pub monitoring_enabled: bool,
}

impl Default for CaptureManaged {
    fn default() -> Self {
        Self {
            managed: false,
            allow_employee_override: false,
            family: false,
            monitoring_enabled: true,
        }
    }
}

impl CaptureManaged {
    /// Organization-managed collection policy is never locally overridable.
    pub fn locked(&self) -> bool {
        self.managed
    }
}

/// Managed state: the on-disk path, current in-memory settings, and the org policy
/// status applied at login.
pub struct SettingsState {
    pub path: std::path::PathBuf,
    pub current: Mutex<Settings>,
    pub managed: Mutex<CaptureManaged>,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn load_defaults_when_missing() {
        let s = load(Path::new("/nonexistent/actilens/settings.json"));
        assert_eq!(s.idle_threshold_s, DEFAULT_IDLE_THRESHOLD_S);
        assert!(!s.domain_only);
    }

    #[test]
    fn managed_policy_overrides_local_capture_switches() {
        use std::sync::atomic::Ordering::Relaxed;

        let dir = std::env::temp_dir().join(format!(
            "actilens_managed_policy_{}",
            uuid::Uuid::new_v4()
        ));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("settings.json");

        let mut local = Settings::default();
        local.consented = true;
        local.collect_app_activity = true;
        local.collect_window_titles = true;
        local.collect_browser_activity = true;
        local.capture_screenshots = true;
        local.count_keystrokes = true;
        local.screenshot_capture_scope = "all_displays".into();

        let state = SettingsState {
            path: path.clone(),
            current: Mutex::new(local),
            managed: Mutex::new(CaptureManaged::default()),
        };
        let control = crate::trackers::TrackerControl::new();
        let policy: crate::sync::client::Policy = serde_json::from_value(
            serde_json::json!({
                "managed": true,
                "business_id": "business-a",
                "collect_app_activity": false,
                "collect_window_titles": false,
                "collect_screenshots": false,
                "collect_browser_activity": false,
                "collect_keystroke_counts": false,
                "screenshot_interval_s": 120,
                "idle_threshold_s": 90,
                "screenshot_retention_days": 14,
                "kind": "team",
                "screenshot_capture_scope": "active_window",
                "privacy_rules": []
            }),
        )
        .unwrap();

        let status = apply_managed_policy(&state, &control, &policy, true);
        assert!(status.managed);
        assert!(status.locked());
        assert!(status.monitoring_enabled);

        let persisted = state.current.lock().unwrap().clone();
        assert!(!persisted.collect_app_activity);
        assert!(!persisted.collect_window_titles);
        assert!(!persisted.collect_browser_activity);
        assert!(!persisted.capture_screenshots);
        assert!(!persisted.count_keystrokes);
        assert_eq!(persisted.screenshot_interval_s, 120);
        assert_eq!(persisted.idle_threshold_s, 90);
        assert_eq!(persisted.screenshot_retention_days, 14);
        assert_eq!(persisted.screenshot_capture_scope, "active_window");

        assert!(control.managed.load(Relaxed));
        assert!(control.org_monitoring_enabled.load(Relaxed));
        assert!(!control.collect_app_activity.load(Relaxed));
        assert!(!control.collect_window_titles.load(Relaxed));
        assert!(!control.collect_browser_activity.load(Relaxed));
        assert!(!control.capture_screenshots.load(Relaxed));
        assert!(!control.count_keystrokes.load(Relaxed));
        assert_eq!(
            control.screenshot_mode.load(Relaxed),
            crate::trackers::SHOT_SCOPE_ACTIVE_WINDOW
        );

        let reloaded = load(&path);
        assert!(!reloaded.collect_app_activity);
        assert!(!reloaded.collect_window_titles);
        assert!(!reloaded.collect_browser_activity);
        assert!(!reloaded.capture_screenshots);
        assert!(!reloaded.count_keystrokes);

        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn managed_scope_suppression_only_crosses_privacy_boundaries() {
        let mut s = Settings::default();
        assert!(!s.needs_managed_scope_suppression("business-a"));

        s.local_only = true;
        assert!(s.needs_managed_scope_suppression("business-a"));

        s.local_only = false;
        s.last_managed_business_id = Some("business-a".into());
        assert!(!s.needs_managed_scope_suppression("business-a"));
        assert!(s.needs_managed_scope_suppression("business-b"));

        s.collection_scope_dirty = true;
        assert!(s.needs_managed_scope_suppression("business-a"));
    }

    #[test]
    fn save_then_load_round_trips() {
        let dir = std::env::temp_dir().join(format!("actilens_settings_{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("settings.json");
        let mut s = Settings::default();
        s.domain_only = true;
        s.screenshot_interval_s = 600;
        save(&path, &s).unwrap();
        let loaded = load(&path);
        assert!(loaded.domain_only);
        assert_eq!(loaded.screenshot_interval_s, 600);
        assert!(loaded.org_monitoring_enabled);
        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn monitoring_state_round_trips() {
        let dir = std::env::temp_dir().join(format!("actilens_monitoring_{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("settings.json");
        let mut s = Settings::default();
        s.org_monitoring_enabled = false;
        save(&path, &s).unwrap();
        assert!(!load(&path).org_monitoring_enabled);
        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn device_id_is_created_once_and_stable() {
        let dir = std::env::temp_dir().join(format!("actilens_devid_{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("settings.json");

        let first = load_with_device_id(&path);
        assert!(!first.device_id.is_empty());
        // Second load must return the exact same id (persisted, not regenerated).
        let second = load_with_device_id(&path);
        assert_eq!(first.device_id, second.device_id);

        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn backend_base_url_uses_compiled_env_default() {
        // No override env set → falls back to the compile-time env default.
        std::env::remove_var("ACTILENS_BACKEND_URL");
        assert_eq!(backend_base_url(), DEFAULT_BACKEND_URL);
        // Sanity: the default build targets production.
        if cfg!(all(
            feature = "production",
            not(feature = "local"),
            not(feature = "staging")
        )) {
            assert_eq!(backend_base_url(), "http://127.0.0.1:8081");
        }
    }
}
