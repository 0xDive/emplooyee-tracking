//! Tauri commands — the bridge the web UI calls into.
//!
//! Queries over stored data (activity, screenshots, browser visits), permission
//! status, settings, pause/resume, and export. Filled in across later tasks.

use std::collections::HashMap;
use std::sync::atomic::Ordering;
use std::sync::Arc;

use serde::Serialize;
use tauri::State;

use crate::platform::{self, CapabilityRow, Permission};
use crate::storage::{Db, SyncTable};
use crate::trackers::TrackerControl;

/// Temporary smoke-test command kept from the scaffold; remove once real
/// commands exist (task 9+).
#[tauri::command]
pub fn ping() -> String {
    "actilens: ok".to_string()
}

/// Pause or resume all tracking. Routed through the tray helper so the menu bar
/// indicator and the dashboard pill stay in sync.
#[tauri::command]
pub fn set_paused(paused: bool, app: tauri::AppHandle) {
    crate::tray::set_paused(&app, paused);
}

/// UI reports whether the user is still on the setup surfaces (welcome/login/
/// onboarding). Pauses tracking there and gates the tray Start item.
#[tauri::command]
pub fn set_in_setup(in_setup: bool, app: tauri::AppHandle) {
    crate::tray::set_in_setup(&app, in_setup);
}

/// Product analytics from the web UI (e.g. `app_active`, `ui_click`). Reuses the same
/// Aptabase pipeline as `app_started` — per-launch session, batching, offline
/// queue. Fire-and-forget: never fails the caller. `props` carry event-specific fields.
#[tauri::command]
pub fn track_event(
    name: String,
    props: Option<serde_json::Value>,
    app: tauri::AppHandle,
    settings: State<Arc<crate::settings::SettingsState>>,
    session: State<crate::analytics::AnalyticsSession>,
) {
    use tauri::Manager;
    let locale = settings.current.lock().unwrap().locale.clone();
    let Ok(data_dir) = app.path().app_data_dir() else {
        return;
    };
    crate::analytics::track_event(
        name,
        locale,
        session.0.clone(),
        data_dir.join("analytics-queue"),
        props,
    );
}

#[tauri::command]
pub fn is_paused(control: State<Arc<TrackerControl>>) -> bool {
    control.effective_paused()
}

/// Current tracking state for the UI: "tracking" | "idle" | "paused".
#[tauri::command]
pub fn tracking_state(control: State<Arc<TrackerControl>>) -> String {
    if control.effective_paused() {
        "paused"
    } else if platform::idle_seconds() >= control.idle_threshold_s.load(Ordering::Relaxed) as f64 {
        "idle"
    } else {
        "tracking"
    }
    .to_string()
}

// ---------- permissions ----------

/// The setup/consent rows for the current OS (see docs/12 §2). macOS returns the 3
/// TCC rows with live grant/deny state; Windows returns capture/consent rows derived
/// from the user's opt-out settings. The React screen renders whatever it's given.
/// Cheap; the UI polls it.
#[tauri::command]
pub fn permissions_status(
    settings: State<Arc<crate::settings::SettingsState>>,
) -> Vec<CapabilityRow> {
    let s = settings.current.lock().unwrap().clone();
    platform::capability_rows(&s)
}

/// Open the System Settings pane for the given permission key
/// (`accessibility` | `input_monitoring` | `screen_recording`).
#[tauri::command]
pub fn open_permission_settings(which: String) -> Result<(), String> {
    let p = Permission::from_key(&which).ok_or_else(|| format!("unknown permission: {which}"))?;
    platform::open_settings(p);
    Ok(())
}

/// Trigger the first-time Screen Recording prompt (the only one requestable in-app).
#[tauri::command]
pub fn request_screen_recording() -> bool {
    platform::request_screen_recording()
}

/// Request Input Monitoring — registers the app in the list and prompts.
#[tauri::command]
pub fn request_input_monitoring() -> bool {
    platform::request_input_monitoring()
}

/// Request Accessibility — registers the app in the list and prompts.
#[tauri::command]
pub fn request_accessibility() -> bool {
    platform::request_accessibility()
}

// ---------- settings ----------

#[tauri::command]
pub fn get_settings(
    state: State<Arc<crate::settings::SettingsState>>,
) -> crate::settings::Settings {
    state.current.lock().unwrap().clone()
}

/// Persist the chosen UI locale and re-translate the native tray to match. Called
/// by the in-app language switcher so the menu-bar item follows the app's language.
#[tauri::command]
pub fn set_locale(
    locale: String,
    app: tauri::AppHandle,
    state: State<Arc<crate::settings::SettingsState>>,
) -> Result<(), String> {
    {
        let mut cur = state.current.lock().unwrap();
        cur.locale = locale;
        crate::settings::save(&state.path, &cur).map_err(err)?;
    }
    crate::tray::relabel(&app);
    Ok(())
}

/// Persist new settings and apply them live to the trackers + ingest server.
#[tauri::command]
pub fn set_settings(
    mut value: crate::settings::Settings,
    app: tauri::AppHandle,
    state: State<Arc<crate::settings::SettingsState>>,
    control: State<Arc<TrackerControl>>,
) -> Result<(), String> {
    // The UI payload doesn't own locale or the server-controlled monitoring
    // state, so preserve both instead of letting serde defaults reset them.
    let current = state.current.lock().unwrap().clone();
    value.locale = current.locale;
    value.last_managed_business_id = current.last_managed_business_id.clone();
    value.collection_scope_dirty =
        current.collection_scope_dirty || current.local_only || value.local_only;
    value.org_monitoring_enabled = if value.local_only {
        true
    } else {
        current.org_monitoring_enabled
    };
    // When the org controls capture settings, ignore changes to those fields —
    // the rest (theme, dock, etc.) still apply.
    if state.managed.lock().unwrap().locked() {
        let cur = state.current.lock().unwrap().clone();
        value.screenshot_interval_s = cur.screenshot_interval_s;
        value.idle_threshold_s = cur.idle_threshold_s;
        value.screenshot_retention_days = cur.screenshot_retention_days;
        value.collect_app_activity = cur.collect_app_activity;
        value.collect_window_titles = cur.collect_window_titles;
        value.collect_browser_activity = cur.collect_browser_activity;
        value.capture_screenshots = cur.capture_screenshots;
        value.count_keystrokes = cur.count_keystrokes;
        value.screenshot_mode = cur.screenshot_mode;
        value.screenshot_capture_scope = cur.screenshot_capture_scope;
        value.screenshot_privacy_rules = cur.screenshot_privacy_rules;
        value.screenshot_skip_apps = cur.screenshot_skip_apps;
    }
    crate::settings::apply(&value, &control);
    crate::apply_dock_policy(&app, value.hide_dock);
    crate::settings::save(&state.path, &value).map_err(err)?;
    *state.current.lock().unwrap() = value;
    Ok(())
}

/// Fetch the org capture policy and, if it's locked (managed and override not
/// allowed), apply it to the live settings + trackers. Returns the managed status so
/// the UI can lock the corresponding controls. Standalone users keep local defaults.
#[tauri::command]
pub async fn apply_org_policy(
    settings: State<'_, Arc<crate::settings::SettingsState>>,
    auth: State<'_, Arc<AuthState>>,
    control: State<'_, Arc<TrackerControl>>,
) -> Result<crate::settings::CaptureManaged, String> {
    let client = BackendClient::new(backend_url(), auth.inner().clone());
    let business_id = auth.session().and_then(|session| session.business_id);
    let policy = client.fetch_policy(business_id.as_deref()).await?;

    let previous = settings.managed.lock().unwrap().monitoring_enabled;
    let monitoring_enabled = if policy.managed {
        client
            .monitoring_enabled(business_id.as_deref())
            .await
            .unwrap_or(previous)
    } else {
        true
    };

    Ok(crate::settings::apply_managed_policy(
        &settings,
        &control,
        &policy,
        monitoring_enabled,
    ))
}

/// Current org capture-policy status for the UI (to lock/unlock the controls).
#[tauri::command]
pub fn capture_policy(
    settings: State<Arc<crate::settings::SettingsState>>,
) -> crate::settings::CaptureManaged {
    *settings.managed.lock().unwrap()
}

/// The curated sensitive-app list for the Settings UI (grouped by category):
/// skip-list suggestions and the privacy-mode prefill. Fetched from the backend
/// so the rules stay server-controlled; falls back to the baked-in copy offline.
#[tauri::command]
pub async fn privacy_apps() -> Result<Vec<crate::sync::client::PrivacyAppCategory>, String> {
    Ok(crate::fetch_privacy_apps_or_baked().await)
}

/// Browser ingest link info for Settings (active port + whether a token exists).
#[derive(Serialize)]
pub struct BrowserLinkInfo {
    pub port: Option<u16>,
    pub token_active: bool,
}

#[tauri::command]
pub fn browser_link(link: State<crate::server::BrowserLink>) -> BrowserLinkInfo {
    BrowserLinkInfo {
        port: link.port,
        token_active: !link.token.is_empty(),
    }
}

/// Capture a screenshot of every display right now. Returns how many were saved.
#[tauri::command]
pub fn capture_now(
    app: tauri::AppHandle,
    db: State<Arc<Db>>,
    control: State<Arc<TrackerControl>>,
) -> Result<usize, String> {
    use tauri::Manager;
    let dir = app.path().app_data_dir().map_err(err)?.join("screenshots");
    Ok(crate::trackers::capture_once(&db, &dir, &control))
}

// ---------- dashboard ----------

#[derive(Serialize)]
pub struct AppTotal {
    pub app_name: String,
    pub total_s: i64,
}

#[derive(Serialize)]
pub struct Seg {
    pub ts: i64,
    pub app_name: String,
    pub duration_s: i64,
}

#[derive(Serialize)]
pub struct DashboardData {
    pub total_active_s: i64,
    pub top_app: Option<String>,
    pub by_app: Vec<AppTotal>,
    pub timeline: Vec<Seg>,
    pub keypresses: i64,
    pub screenshots: i64,
}

/// Aggregated activity for the half-open window `[from_ts, to_ts)`.
#[tauri::command]
pub fn dashboard_data(
    from_ts: i64,
    to_ts: i64,
    db: State<Arc<Db>>,
) -> Result<DashboardData, String> {
    let samples = db.activity_between(from_ts, to_ts).map_err(err)?;

    let mut by_app_map: HashMap<String, i64> = HashMap::new();
    let mut total_active_s = 0i64;
    let mut timeline = Vec::with_capacity(samples.len());
    for s in &samples {
        *by_app_map.entry(s.app_name.clone()).or_insert(0) += s.duration_s;
        total_active_s += s.duration_s;
        timeline.push(Seg {
            ts: s.ts,
            app_name: s.app_name.clone(),
            duration_s: s.duration_s,
        });
    }

    let mut by_app: Vec<AppTotal> = by_app_map
        .into_iter()
        .map(|(app_name, total_s)| AppTotal { app_name, total_s })
        .collect();
    by_app.sort_by(|a, b| b.total_s.cmp(&a.total_s));
    let top_app = by_app.first().map(|a| a.app_name.clone());

    let keypresses = db
        .keystrokes_between(from_ts, to_ts)
        .map_err(err)?
        .iter()
        .map(|(_, c)| *c)
        .sum();
    let screenshots = db.screenshots_between(from_ts, to_ts).map_err(err)?.len() as i64;

    Ok(DashboardData {
        total_active_s,
        top_app,
        by_app,
        timeline,
        keypresses,
        screenshots,
    })
}

/// Screenshots captured in `[from_ts, to_ts)` (newest first), for the gallery.
#[tauri::command]
pub fn screenshot_list(
    from_ts: i64,
    to_ts: i64,
    db: State<Arc<Db>>,
) -> Result<Vec<crate::storage::Screenshot>, String> {
    let mut rows = db.screenshots_between(from_ts, to_ts).map_err(err)?;
    rows.reverse(); // newest first
    Ok(rows)
}

/// Browser visits in `[from_ts, to_ts)` (newest first).
#[tauri::command]
pub fn browser_visits(
    from_ts: i64,
    to_ts: i64,
    db: State<Arc<Db>>,
) -> Result<Vec<crate::storage::BrowserVisit>, String> {
    let mut rows = db.browser_visits_between(from_ts, to_ts).map_err(err)?;
    rows.reverse();
    Ok(rows)
}

/// Per-minute keystroke buckets `[ts_bucket, count]` in `[from_ts, to_ts)`.
#[tauri::command]
pub fn keystroke_buckets(
    from_ts: i64,
    to_ts: i64,
    db: State<Arc<Db>>,
) -> Result<Vec<(i64, i64)>, String> {
    db.keystrokes_between(from_ts, to_ts).map_err(err)
}

#[derive(Serialize)]
struct KeystrokeRow {
    ts_bucket: i64,
    count: i64,
}

/// Export all tables to a single JSON document under `dir`, limited to
/// `[from_ts, to_ts)`. User-triggered only.
#[tauri::command]
pub fn export_json(
    dir: String,
    from_ts: i64,
    to_ts: i64,
    db: State<Arc<Db>>,
) -> Result<ExportSummary, String> {
    export_json_to_dir(&db, &dir, from_ts, to_ts)
}

/// Testable core of [`export_json`].
pub fn export_json_to_dir(
    db: &Db,
    dir: &str,
    from_ts: i64,
    to_ts: i64,
) -> Result<ExportSummary, String> {
    use std::path::Path;
    let activity = db.activity_between(from_ts, to_ts).map_err(err)?;
    let keystrokes: Vec<KeystrokeRow> = db
        .keystrokes_between(from_ts, to_ts)
        .map_err(err)?
        .into_iter()
        .map(|(ts_bucket, count)| KeystrokeRow { ts_bucket, count })
        .collect();
    let screenshots = db.screenshots_between(from_ts, to_ts).map_err(err)?;
    let visits = db.browser_visits_between(from_ts, to_ts).map_err(err)?;

    let rows = activity.len() + keystrokes.len() + screenshots.len() + visits.len();
    let doc = serde_json::json!({
        "activity_sample": serde_json::to_value(&activity).map_err(err)?,
        "keystroke_bucket": serde_json::to_value(&keystrokes).map_err(err)?,
        "screenshot": serde_json::to_value(&screenshots).map_err(err)?,
        "browser_visit": serde_json::to_value(&visits).map_err(err)?,
    });

    let path = Path::new(dir).join("actilens_export.json");
    std::fs::write(&path, serde_json::to_string_pretty(&doc).map_err(err)?).map_err(err)?;

    Ok(ExportSummary {
        dir: dir.to_string(),
        files: vec![FileResult {
            name: "actilens_export.json".into(),
            rows,
        }],
    })
}

fn err<E: std::fmt::Display>(e: E) -> String {
    e.to_string()
}

// ---------- auth / session (task 51) ----------

use crate::sync::auth::{AuthState, Session};
use crate::sync::client::{BackendClient, LoginAttempt, MembershipState, PublicBusiness};

/// The backend base URL (compile-time default; env override for dev).
fn backend_url() -> String {
    crate::settings::backend_base_url()
}

/// The web signup wizard URL, opened in the system browser from the desktop
/// welcome/login screens. The web admin is served under `/admin` on the backend.
#[tauri::command]
pub fn signup_url() -> String {
    format!("{}/admin/signup", backend_url().trim_end_matches('/'))
}

/// `GET /v1/public/businesses` — the login picker's list of companies/owners.
#[tauri::command]
pub async fn list_businesses(
    auth: State<'_, Arc<AuthState>>,
) -> Result<Vec<PublicBusiness>, String> {
    let client = BackendClient::new(backend_url(), auth.inner().clone());
    client.list_businesses().await
}

#[derive(Serialize)]
pub struct DesktopLoginResult {
    pub status: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub session: Option<Session>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub challenge_token: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub business_id: Option<String>,
    #[serde(default)]
    pub organizations: Vec<MembershipState>,
}

fn managed_policy_rejects(message: &str) -> bool {
    [
        "member_blocked",
        "member_removed",
        "organization_archived",
        "organization_deletion_pending",
    ]
    .iter()
    .any(|code| message.contains(code))
}

fn suppress_pre_managed_backlog(db: &Db) -> Result<(), String> {
    for table in [
        SyncTable::Activity,
        SyncTable::Keystroke,
        SyncTable::Browser,
        SyncTable::Screenshot,
    ] {
        db.suppress_pending(table).map_err(err)?;
    }
    Ok(())
}

async fn activate_authenticated_session(
    session: Session,
    auth: &Arc<AuthState>,
    settings: &Arc<crate::settings::SettingsState>,
    control: &Arc<TrackerControl>,
    db: &Arc<Db>,
) -> Result<DesktopLoginResult, String> {
    let managed_business_id = session.business_id.clone();

    if let Some(business_id) = managed_business_id.as_deref() {
        let suppress = settings
            .current
            .lock()
            .unwrap()
            .needs_managed_scope_suppression(business_id);
        if suppress {
            // Privacy boundary: rows collected in personal/unbound mode or for a
            // different organization remain local and are never silently uploaded.
            suppress_pre_managed_backlog(db)?;
        }
    }

    auth.store(session.clone())?;

    if let Some(business_id) = managed_business_id.as_deref() {
        control.managed.store(true, Ordering::Relaxed);
        control
            .org_monitoring_enabled
            .store(false, Ordering::Relaxed);
        {
            let mut current = settings.current.lock().unwrap();
            current.local_only = false;
            current.last_managed_business_id = Some(business_id.to_string());
            current.collection_scope_dirty = false;
            current.org_monitoring_enabled = false;
            let _ = crate::settings::save(&settings.path, &current);
        }
        {
            let mut status = settings.managed.lock().unwrap();
            status.managed = true;
            status.allow_employee_override = false;
            status.monitoring_enabled = false;
        }

        let client = BackendClient::new(backend_url(), auth.clone());
        match client.fetch_policy(Some(business_id)).await {
            Ok(policy) => {
                let enabled = client
                    .monitoring_enabled(Some(business_id))
                    .await
                    .unwrap_or(false);
                crate::settings::apply_managed_policy(settings, control, &policy, enabled);
            }
            Err(e) => {
                crate::log_warn!("policy", "initial managed policy fetch failed: {e}");
            }
        }
    } else {
        control.managed.store(false, Ordering::Relaxed);
        control
            .org_monitoring_enabled
            .store(true, Ordering::Relaxed);
        *settings.managed.lock().unwrap() = crate::settings::CaptureManaged::default();
        let mut current = settings.current.lock().unwrap();
        // Rows created by an authenticated-but-unbound account are still outside
        // any organization privacy boundary and must not later flow into one.
        current.collection_scope_dirty = true;
        current.org_monitoring_enabled = true;
        crate::settings::apply(&current, control);
        let _ = crate::settings::save(&settings.path, &current);
    }

    Ok(DesktopLoginResult {
        status: "authenticated".into(),
        session: Some(session),
        challenge_token: None,
        business_id: None,
        organizations: Vec::new(),
    })
}

/// Password login. Multi-organization accounts select the governing organization
/// before a session is created; MFA is completed in a separate command.
#[tauri::command]
pub async fn login(
    email: String,
    password: String,
    business_id: Option<String>,
    auth: State<'_, Arc<AuthState>>,
    settings: State<'_, Arc<crate::settings::SettingsState>>,
    control: State<'_, Arc<TrackerControl>>,
    db: State<'_, Arc<Db>>,
) -> Result<DesktopLoginResult, String> {
    let client = BackendClient::new(backend_url(), auth.inner().clone());
    match client
        .login(&email, &password, business_id.as_deref())
        .await?
    {
        LoginAttempt::Authenticated(session) => {
            activate_authenticated_session(
                session,
                auth.inner(),
                settings.inner(),
                control.inner(),
                db.inner(),
            )
            .await
        }
        LoginAttempt::MFARequired {
            challenge_token,
            business_id,
        } => Ok(DesktopLoginResult {
            status: "mfa_required".into(),
            session: None,
            challenge_token: Some(challenge_token),
            business_id,
            organizations: Vec::new(),
        }),
        LoginAttempt::OrganizationRequired { organizations } => Ok(DesktopLoginResult {
            status: "organization_required".into(),
            session: None,
            challenge_token: None,
            business_id: None,
            organizations,
        }),
    }
}

#[tauri::command]
pub async fn complete_mfa_login(
    challenge_token: String,
    code: String,
    email: String,
    business_id: Option<String>,
    auth: State<'_, Arc<AuthState>>,
    settings: State<'_, Arc<crate::settings::SettingsState>>,
    control: State<'_, Arc<TrackerControl>>,
    db: State<'_, Arc<Db>>,
) -> Result<DesktopLoginResult, String> {
    let client = BackendClient::new(backend_url(), auth.inner().clone());
    let session = client
        .complete_mfa_login(
            &challenge_token,
            &code,
            &email,
            business_id.as_deref(),
        )
        .await?;
    activate_authenticated_session(
        session,
        auth.inner(),
        settings.inner(),
        control.inner(),
        db.inner(),
    )
    .await
}

/// Clear the stored session and release any organization-controlled
/// collection gate. A future organization login will fetch its own policy again.
#[tauri::command]
pub fn logout(
    auth: State<Arc<AuthState>>,
    settings: State<Arc<crate::settings::SettingsState>>,
    control: State<Arc<TrackerControl>>,
) -> Result<(), String> {
    auth.clear()?;
    control.in_setup.store(true, Ordering::Relaxed);
    control
        .org_monitoring_enabled
        .store(true, Ordering::Relaxed);
    {
        let mut current = settings.current.lock().unwrap();
        current.org_monitoring_enabled = true;
        crate::settings::save(&settings.path, &current).map_err(err)?;
    }
    *settings.managed.lock().unwrap() = crate::settings::CaptureManaged::default();
    control.managed.store(false, Ordering::Relaxed);
    Ok(())
}

/// Return the current session. On a first managed launch, a one-time
/// ACTILENS_ENROLL_TOKEN (or --enroll-token) is redeemed before the UI leaves
/// its startup gate, so deployment does not need an employee password.
#[tauri::command]
pub async fn current_session(
    auth: State<'_, Arc<AuthState>>,
    settings: State<'_, Arc<crate::settings::SettingsState>>,
    control: State<'_, Arc<TrackerControl>>,
    db: State<'_, Arc<Db>>,
) -> Result<Option<Session>, String> {
    if let Some(session) = auth.session() {
        if let Some(business_id) = session.business_id.as_deref() {
            let suppress = settings
                .current
                .lock()
                .unwrap()
                .needs_managed_scope_suppression(business_id);
            if suppress {
                suppress_pre_managed_backlog(db.inner())?;
            }
            {
                let mut current = settings.current.lock().unwrap();
                current.local_only = false;
                current.last_managed_business_id = Some(business_id.to_string());
                current.collection_scope_dirty = false;
                let _ = crate::settings::save(&settings.path, &current);
            }
            control.managed.store(true, Ordering::Relaxed);
            {
                let mut managed = settings.managed.lock().unwrap();
                managed.managed = true;
                managed.allow_employee_override = false;
                managed.monitoring_enabled =
                    settings.current.lock().unwrap().org_monitoring_enabled;
            }

            let client = BackendClient::new(backend_url(), auth.inner().clone());
            match client.fetch_policy(session.business_id.as_deref()).await {
                Ok(policy) => {
                    let previous = settings.managed.lock().unwrap().monitoring_enabled;
                    let enabled = client
                        .monitoring_enabled(session.business_id.as_deref())
                        .await
                        .unwrap_or(previous);
                    crate::settings::apply_managed_policy(
                        settings.inner(),
                        control.inner(),
                        &policy,
                        enabled,
                    );
                }
                Err(e) if managed_policy_rejects(&e) => {
                    control
                        .org_monitoring_enabled
                        .store(false, Ordering::Relaxed);
                    {
                        let mut current = settings.current.lock().unwrap();
                        current.org_monitoring_enabled = false;
                        let _ = crate::settings::save(&settings.path, &current);
                    }
                    settings.managed.lock().unwrap().monitoring_enabled = false;
                }
                Err(e) => {
                    crate::log_warn!("policy", "startup policy refresh failed: {e}");
                }
            }
        } else {
            control.managed.store(false, Ordering::Relaxed);
            let mut current = settings.current.lock().unwrap();
            current.collection_scope_dirty = true;
            let _ = crate::settings::save(&settings.path, &current);
        }
        return Ok(Some(session));
    }

    let token = match crate::sync::enrollment::pending_token() {
        Some(token) => token,
        None => return Ok(None),
    };

    // An enrollment token means this installation is being provisioned for
    // an organization. Fail closed until the server resolves the membership.
    control
        .org_monitoring_enabled
        .store(false, Ordering::Relaxed);
    {
        let mut current = settings.current.lock().unwrap();
        current.org_monitoring_enabled = false;
        let _ = crate::settings::save(&settings.path, &current);
    }
    settings.managed.lock().unwrap().monitoring_enabled = false;

    let session = match crate::sync::enrollment::redeem(&backend_url(), &token).await {
        Ok(session) => session,
        Err(e) => {
            crate::log_warn!("enrollment", "automatic enrollment failed: {e}");
            return Ok(None);
        }
    };

    // An enrollment creates the privacy boundary between personal/local history
    // and organization-managed collection.
    if let Some(business_id) = session.business_id.as_deref() {
        let suppress = settings
            .current
            .lock()
            .unwrap()
            .needs_managed_scope_suppression(business_id);
        if suppress {
            suppress_pre_managed_backlog(db.inner())?;
        }
    }

    if let Err(e) = auth.store(session.clone()) {
        crate::log_warn!("enrollment", "could not persist enrolled session: {e}");
        return Err(e);
    }
    if let Some(business_id) = session.business_id.as_deref() {
        let mut current = settings.current.lock().unwrap();
        current.local_only = false;
        current.last_managed_business_id = Some(business_id.to_string());
        current.collection_scope_dirty = false;
        current.org_monitoring_enabled = false;
        let _ = crate::settings::save(&settings.path, &current);
    }

    // Do not keep the raw secret in this process after it has been consumed.
    std::env::remove_var("ACTILENS_ENROLL_TOKEN");

    let client = BackendClient::new(backend_url(), auth.inner().clone());
    control.managed.store(true, Ordering::Relaxed);
    match client.fetch_policy(session.business_id.as_deref()).await {
        Ok(policy) => {
            let previous = settings.managed.lock().unwrap().monitoring_enabled;
            let enabled = client
                .monitoring_enabled(session.business_id.as_deref())
                .await
                .unwrap_or(previous);
            crate::settings::apply_managed_policy(&settings, &control, &policy, enabled);
        }
        Err(e) => {
            crate::log_warn!("policy", "enrollment policy fetch failed: {e}");
        }
    }

    Ok(Some(session))
}

// ---------- sync status (task 53) ----------

#[derive(Serialize)]
pub struct SyncStatusView {
    pub last_sync_ts: i64,
    pub pending: u64,
    pub last_error: String,
}

/// Last sync time, pending count, and last error for the UI / menu bar.
#[tauri::command]
pub fn sync_status(status: State<Arc<crate::sync::worker::SyncStatus>>) -> SyncStatusView {
    use std::sync::atomic::Ordering;
    SyncStatusView {
        last_sync_ts: status.last_sync_ts.load(Ordering::Relaxed),
        pending: status.pending.load(Ordering::Relaxed),
        last_error: status.last_error.lock().unwrap().clone(),
    }
}

// ---------- export ----------

#[derive(Serialize)]
pub struct FileResult {
    pub name: String,
    pub rows: usize,
}

#[derive(Serialize)]
pub struct ExportSummary {
    pub dir: String,
    pub files: Vec<FileResult>,
}

fn opt_i(v: Option<i64>) -> String {
    v.map(|n| n.to_string()).unwrap_or_default()
}

/// Export every table to one CSV each under `dir`, limited to `[from_ts, to_ts)`.
/// User-triggered only — the sole path by which data leaves the machine.
#[tauri::command]
pub fn export_csv(
    dir: String,
    from_ts: i64,
    to_ts: i64,
    db: State<Arc<Db>>,
) -> Result<ExportSummary, String> {
    export_to_dir(&db, &dir, from_ts, to_ts)
}

/// Testable core of [`export_csv`] — no Tauri state, just a DB + destination.
pub fn export_to_dir(
    db: &Db,
    dir: &str,
    from_ts: i64,
    to_ts: i64,
) -> Result<ExportSummary, String> {
    use std::path::Path;
    let base = Path::new(dir);
    let mut files = Vec::new();

    // activity_sample
    {
        let rows = db.activity_between(from_ts, to_ts).map_err(err)?;
        let mut w = csv::Writer::from_path(base.join("activity_sample.csv")).map_err(err)?;
        w.write_record(["ts", "app_name", "window_title", "pid", "duration_s"])
            .map_err(err)?;
        for r in &rows {
            w.write_record([
                r.ts.to_string(),
                r.app_name.clone(),
                r.window_title.clone().unwrap_or_default(),
                opt_i(r.pid),
                r.duration_s.to_string(),
            ])
            .map_err(err)?;
        }
        w.flush().map_err(err)?;
        files.push(FileResult {
            name: "activity_sample.csv".into(),
            rows: rows.len(),
        });
    }

    // keystroke_bucket
    {
        let rows = db.keystrokes_between(from_ts, to_ts).map_err(err)?;
        let mut w = csv::Writer::from_path(base.join("keystroke_bucket.csv")).map_err(err)?;
        w.write_record(["ts_bucket", "count"]).map_err(err)?;
        for (ts_bucket, count) in &rows {
            w.write_record([ts_bucket.to_string(), count.to_string()])
                .map_err(err)?;
        }
        w.flush().map_err(err)?;
        files.push(FileResult {
            name: "keystroke_bucket.csv".into(),
            rows: rows.len(),
        });
    }

    // screenshot
    {
        let rows = db.screenshots_between(from_ts, to_ts).map_err(err)?;
        let mut w = csv::Writer::from_path(base.join("screenshot.csv")).map_err(err)?;
        w.write_record(["ts", "file_path", "display_id", "width", "height"])
            .map_err(err)?;
        for r in &rows {
            w.write_record([
                r.ts.to_string(),
                r.file_path.clone(),
                opt_i(r.display_id),
                opt_i(r.width),
                opt_i(r.height),
            ])
            .map_err(err)?;
        }
        w.flush().map_err(err)?;
        files.push(FileResult {
            name: "screenshot.csv".into(),
            rows: rows.len(),
        });
    }

    // browser_visit
    {
        let rows = db.browser_visits_between(from_ts, to_ts).map_err(err)?;
        let mut w = csv::Writer::from_path(base.join("browser_visit.csv")).map_err(err)?;
        w.write_record(["ts", "url", "page_title", "browser", "duration_s"])
            .map_err(err)?;
        for r in &rows {
            w.write_record([
                r.ts.to_string(),
                r.url.clone(),
                r.page_title.clone().unwrap_or_default(),
                r.browser.clone().unwrap_or_default(),
                r.duration_s.to_string(),
            ])
            .map_err(err)?;
        }
        w.flush().map_err(err)?;
        files.push(FileResult {
            name: "browser_visit.csv".into(),
            rows: rows.len(),
        });
    }

    Ok(ExportSummary {
        dir: dir.to_string(),
        files,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::storage::ActivitySample;

    #[test]
    fn export_quotes_tricky_fields() {
        let db = Db::open_in_memory().unwrap();
        // A window title with comma, quote, newline, and emoji.
        let nasty = "a, \"b\"\nc 🚀";
        db.insert_activity_sample(&ActivitySample {
            ts: 100,
            app_name: "Code".into(),
            window_title: Some(nasty.into()),
            pid: Some(7),
            duration_s: 12,
        })
        .unwrap();

        let dir = std::env::temp_dir().join(format!("actilens_export_test_{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        let summary = export_to_dir(&db, dir.to_str().unwrap(), 0, i64::MAX).unwrap();
        assert_eq!(summary.files.len(), 4);

        // Read activity_sample.csv back with a CSV parser — escaping must round-trip.
        let mut rdr = csv::Reader::from_path(dir.join("activity_sample.csv")).unwrap();
        let rec = rdr.records().next().unwrap().unwrap();
        assert_eq!(&rec[1], "Code");
        assert_eq!(&rec[2], nasty); // exact field preserved through quoting
        assert_eq!(&rec[4], "12");

        std::fs::remove_dir_all(&dir).ok();
    }

    #[test]
    fn json_export_is_valid_and_complete() {
        let db = Db::open_in_memory().unwrap();
        db.insert_activity_sample(&ActivitySample {
            ts: 100,
            app_name: "Code".into(),
            window_title: None,
            pid: None,
            duration_s: 5,
        })
        .unwrap();
        db.add_keystrokes(60, 9).unwrap();

        let dir = std::env::temp_dir().join(format!("actilens_json_test_{}", std::process::id()));
        std::fs::create_dir_all(&dir).unwrap();
        export_json_to_dir(&db, dir.to_str().unwrap(), 0, i64::MAX).unwrap();

        let text = std::fs::read_to_string(dir.join("actilens_export.json")).unwrap();
        let v: serde_json::Value = serde_json::from_str(&text).unwrap();
        assert_eq!(v["activity_sample"][0]["app_name"], "Code");
        assert_eq!(v["keystroke_bucket"][0]["count"], 9);
        assert!(v["screenshot"].is_array() && v["browser_visit"].is_array());

        std::fs::remove_dir_all(&dir).ok();
    }
}
