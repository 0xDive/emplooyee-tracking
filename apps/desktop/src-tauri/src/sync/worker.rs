//! Background sync worker (task 53).
//!
//! Pushes pending local rows (`synced = 0`) to the backend on a timer (~5 min)
//! and once shortly after app start. One pass per table, batched. On a 2xx the
//! backend echoes the accepted `client_uuid`s and we flip exactly those to
//! `synced = 1` — never deleting local rows (docs/11). Failures back off
//! exponentially so a down backend isn't hammered.
//!
//! Offline / logged-out / no backend → the pass is a no-op and rows stay pending,
//! which is the whole point of local-first.

use std::sync::atomic::{AtomicI64, AtomicU64, Ordering};
use std::sync::{Arc, Mutex};
use std::thread;
use std::time::Duration;

use super::auth::AuthState;
use super::client::BackendClient;
use super::BATCH_LIMIT;
use crate::storage::{Db, PendingActivity, SyncTable};

/// Base interval between sync passes (5 min). Backoff multiplies this on failure.
const BASE_INTERVAL: Duration = Duration::from_secs(300);
/// Cap the backoff so we still retry roughly every 16 min when the backend is down.
const MAX_INTERVAL: Duration = Duration::from_secs(16 * 60);
/// Short delay before the first pass so startup isn't blocked.
const STARTUP_DELAY: Duration = Duration::from_secs(10);

fn apply_activity_collection_policy(
    activity: &mut [PendingActivity],
    collect_apps: bool,
    collect_titles: bool,
) {
    for sample in activity {
        if !collect_apps {
            sample.app_name.clear();
            sample.window_title = None;
            sample.pid = None;
        } else if !collect_titles {
            sample.window_title = None;
        }
    }
}

/// Sync status surfaced to the UI / menu bar (task 53).
#[derive(Default)]
pub struct SyncStatus {
    /// Unix seconds of the last *successful* pass (0 = never).
    pub last_sync_ts: AtomicI64,
    /// Rows still pending after the last pass.
    pub pending: AtomicU64,
    /// Last error message, if the last pass failed (empty = ok).
    pub last_error: Mutex<String>,
}

impl SyncStatus {
    fn record_success(&self, pending: i64) {
        self.last_sync_ts
            .store(crate::now_unix(), Ordering::Relaxed);
        self.pending.store(pending.max(0) as u64, Ordering::Relaxed);
        *self.last_error.lock().unwrap() = String::new();
    }

    fn record_error(&self, msg: String, pending: i64) {
        self.pending.store(pending.max(0) as u64, Ordering::Relaxed);
        *self.last_error.lock().unwrap() = msg;
    }
}

/// Everything a sync pass needs. Cloneable handles shared with the worker thread.
#[derive(Clone)]
pub struct SyncContext {
    pub db: Arc<Db>,
    pub auth: Arc<AuthState>,
    pub status: Arc<SyncStatus>,
    pub control: Arc<crate::trackers::TrackerControl>,
    /// Backend base URL + device_id are read fresh each pass so settings changes
    /// (and the first-run device_id) take effect without a restart.
    pub settings: Arc<crate::settings::SettingsState>,
}

/// Spawn the worker on its own thread with a dedicated single-thread tokio runtime
/// (mirrors `server::start`). Returns immediately.
pub fn start(ctx: SyncContext) {
    thread::spawn(move || {
        let rt = match tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
        {
            Ok(rt) => rt,
            Err(e) => {
                crate::log_warn!("sync", "runtime build failed: {e}");
                return;
            }
        };
        rt.block_on(run(ctx));
    });
}

async fn run(ctx: SyncContext) {
    tokio::time::sleep(STARTUP_DELAY).await;
    let mut backoff = BASE_INTERVAL;

    loop {
        match run_once(&ctx).await {
            PassOutcome::Ok => {
                backoff = BASE_INTERVAL;
            }
            PassOutcome::Skipped => {
                // Logged out / no backend configured — wait the normal interval.
                backoff = BASE_INTERVAL;
            }
            PassOutcome::Failed => {
                // Down/offline — grow the wait, capped.
                backoff = (backoff * 2).min(MAX_INTERVAL);
            }
        }
        tokio::time::sleep(backoff).await;
    }
}

pub enum PassOutcome {
    Ok,
    Skipped,
    Failed,
}

/// One full sync pass over all tables. Public so a command can trigger a manual
/// "sync now" later. Returns the outcome that drives the backoff.
pub async fn run_once(ctx: &SyncContext) -> PassOutcome {
    // Logged out → nothing to authenticate the push; stay pending.
    if !ctx.auth.is_logged_in() {
        return PassOutcome::Skipped;
    }

    let base_url = crate::settings::backend_base_url();
    let device_id = ctx.settings.current.lock().unwrap().device_id.clone();
    // business_id is resolved at login and lives on the session.
    let business_id = ctx.auth.session().and_then(|s| s.business_id);
    if base_url.is_empty() || device_id.is_empty() {
        return PassOutcome::Skipped;
    }

    let client = BackendClient::new(base_url, ctx.auth.clone());

    match client.fetch_policy(business_id.as_deref()).await {
        Ok(policy) => {
            let previous = ctx.settings.managed.lock().unwrap().monitoring_enabled;
            let enabled = if policy.managed {
                client
                    .monitoring_enabled(business_id.as_deref())
                    .await
                    .unwrap_or(previous)
            } else {
                true
            };
            crate::settings::apply_managed_policy(
                &ctx.settings,
                &ctx.control,
                &policy,
                enabled,
            );
        }
        Err(e) if policy_stops_collection(&e) => {
            // A server-authoritative lifecycle/security state must stop collection
            // even if the UI is closed. Preserve this fail-closed state offline.
            ctx.control.managed.store(true, Ordering::Relaxed);
            ctx.control
                .org_monitoring_enabled
                .store(false, Ordering::Relaxed);
            {
                let mut current = ctx.settings.current.lock().unwrap();
                current.org_monitoring_enabled = false;
                let _ = crate::settings::save(&ctx.settings.path, &current);
            }
            ctx.settings.managed.lock().unwrap().monitoring_enabled = false;
            ctx.status.record_error(e, pending_total(ctx));
            return PassOutcome::Skipped;
        }
        Err(e) => {
            // Network/server outage: retain the last server-confirmed policy instead
            // of widening collection with local defaults.
            crate::log_warn!("policy", "background policy refresh failed: {e}");
        }
    }

    if !ctx.control.org_monitoring_enabled.load(Ordering::Relaxed) {
        return PassOutcome::Skipped;
    }

    // Collection switches govern transmission as well as new local writes. Drop
    // pending upload eligibility for disabled optional categories so data queued
    // before a policy change cannot leak later if the category is re-enabled.
    if !ctx.control.count_keystrokes.load(Ordering::Relaxed) {
        let _ = ctx.db.suppress_pending(SyncTable::Keystroke);
    }
    if !ctx.control.collect_browser_activity.load(Ordering::Relaxed) {
        let _ = ctx.db.suppress_pending(SyncTable::Browser);
    }
    if !ctx.control.capture_screenshots.load(Ordering::Relaxed) {
        let _ = ctx.db.suppress_pending(SyncTable::Screenshot);
    }

    let mut failed = false;

    // --- JSON batch: activity + keystrokes + browser ---
    loop {
        let mut activity = match ctx.db.pending_activity(BATCH_LIMIT) {
            Ok(v) => v,
            Err(e) => {
                ctx.status
                    .record_error(format!("db: {e}"), pending_total(ctx));
                return PassOutcome::Failed;
            }
        };
        let keystrokes = ctx.db.pending_keystrokes(BATCH_LIMIT).unwrap_or_default();
        let browser = ctx.db.pending_browser(BATCH_LIMIT).unwrap_or_default();

        // Active/idle duration is mandatory for managed mode, but app identity and
        // titles are optional. Redact old pending rows at transmission time too,
        // because they may have been recorded before the administrator changed policy.
        let collect_apps = ctx.control.collect_app_activity.load(Ordering::Relaxed);
        let collect_titles = ctx.control.collect_window_titles.load(Ordering::Relaxed);
        apply_activity_collection_policy(&mut activity, collect_apps, collect_titles);

        if activity.is_empty() && keystrokes.is_empty() && browser.is_empty() {
            break;
        }

        match client
            .sync_batch(
                &device_id,
                business_id.as_deref(),
                &activity,
                &keystrokes,
                &browser,
            )
            .await
        {
            Ok(accepted) => {
                let _ = ctx.db.mark_synced(SyncTable::Activity, &accepted.activity);
                let _ = ctx
                    .db
                    .mark_synced(SyncTable::Keystroke, &accepted.keystrokes);
                let _ = ctx.db.mark_synced(SyncTable::Browser, &accepted.browser);

                // If the backend accepted nothing, break to avoid an infinite loop
                // on a poison row; it'll be retried next pass.
                if accepted.activity.is_empty()
                    && accepted.keystrokes.is_empty()
                    && accepted.browser.is_empty()
                {
                    break;
                }
                // Full batches likely mean more pending — loop again. Partial → done.
                if activity.len() < BATCH_LIMIT as usize
                    && keystrokes.len() < BATCH_LIMIT as usize
                    && browser.len() < BATCH_LIMIT as usize
                {
                    break;
                }
            }
            Err(e) => {
                ctx.status.record_error(e, pending_total(ctx));
                failed = true;
                break;
            }
        }
    }

    // --- Screenshots: one multipart upload each ---
    if !failed {
        match ctx.db.pending_screenshots(BATCH_LIMIT) {
            Ok(shots) => {
                for shot in shots {
                    match client
                        .sync_screenshot(&device_id, business_id.as_deref(), &shot)
                        .await
                    {
                        Ok(accepted) => {
                            let _ = ctx.db.mark_synced(SyncTable::Screenshot, &accepted);
                        }
                        // Only a genuine network failure (offline) should abort the
                        // pass and trigger backoff. A per-shot rejection or an
                        // unreadable file shouldn't block the other screenshots.
                        Err(e) if e.starts_with("network error") => {
                            ctx.status.record_error(e, pending_total(ctx));
                            failed = true;
                            break;
                        }
                        Err(e) => {
                            crate::log_warn!(
                                "sync",
                                "screenshot {} skipped: {e}",
                                shot.client_uuid
                            );
                            continue;
                        }
                    }
                }
            }
            Err(e) => {
                ctx.status
                    .record_error(format!("db: {e}"), pending_total(ctx));
                failed = true;
            }
        }
    }

    let pending = pending_total(ctx);
    if failed {
        PassOutcome::Failed
    } else {
        ctx.status.record_success(pending);
        PassOutcome::Ok
    }
}

fn policy_stops_collection(message: &str) -> bool {
    [
        "member_blocked",
        "member_removed",
        "organization_archived",
        "organization_deletion_pending",
    ]
    .iter()
    .any(|code| message.contains(code))
}

fn pending_total(ctx: &SyncContext) -> i64 {
    ctx.db.pending_count().unwrap_or(0)
}


#[cfg(test)]
mod tests {
    use super::*;

    fn pending_activity() -> PendingActivity {
        PendingActivity {
            client_uuid: "sample-1".into(),
            ts: 100,
            app_name: "Sensitive App".into(),
            window_title: Some("Sensitive Window".into()),
            pid: Some(42),
            duration_s: 15,
            updated_at: 100,
        }
    }

    #[test]
    fn app_collection_off_redacts_identity_before_transmission() {
        let mut rows = vec![pending_activity()];
        apply_activity_collection_policy(&mut rows, false, true);
        assert_eq!(rows[0].app_name, "");
        assert_eq!(rows[0].window_title, None);
        assert_eq!(rows[0].pid, None);
        assert_eq!(rows[0].duration_s, 15);
    }

    #[test]
    fn window_title_collection_off_preserves_app_but_redacts_title() {
        let mut rows = vec![pending_activity()];
        apply_activity_collection_policy(&mut rows, true, false);
        assert_eq!(rows[0].app_name, "Sensitive App");
        assert_eq!(rows[0].window_title, None);
        assert_eq!(rows[0].pid, Some(42));
        assert_eq!(rows[0].duration_s, 15);
    }

    #[test]
    fn enabled_activity_collection_keeps_identity() {
        let mut rows = vec![pending_activity()];
        apply_activity_collection_policy(&mut rows, true, true);
        assert_eq!(rows[0].app_name, "Sensitive App");
        assert_eq!(rows[0].window_title.as_deref(), Some("Sensitive Window"));
        assert_eq!(rows[0].pid, Some(42));
    }
}
