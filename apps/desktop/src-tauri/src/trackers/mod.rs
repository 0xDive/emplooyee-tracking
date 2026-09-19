//! Background trackers (see docs/01-architecture.md).
//!
//! Currently: active-window + idle (task 7). Keyboard (17) and screenshots (19)
//! plug in later. Each tracker reads `TrackerControl` for live settings and writes
//! through `crate::storage`.
//!
//! The decision logic lives in `WindowTracker::tick`, a pure function over
//! (active?, window, threshold, now) → optional sample-to-flush, so it's unit
//! tested without real timers or platform calls.

use std::sync::atomic::{AtomicBool, AtomicU64, AtomicU8, Ordering};
use std::sync::{Arc, RwLock};
use std::thread;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use std::path::Path;

use crate::platform::ActiveWindowInfo;
use crate::storage::{ActivitySample, Db, Screenshot};

/// How often the active-window/idle loop polls.
const POLL: Duration = Duration::from_secs(1);
/// Seconds of active time each tick represents.
const TICK_S: i64 = 1;
/// Default idle threshold — no input for this long pauses time counting.
pub const DEFAULT_IDLE_THRESHOLD_S: u64 = 60;
/// Flush an ongoing interval at least this often so the dashboard stays fresh and
/// a crash can't lose more than this much active time.
const MAX_CHUNK_S: i64 = 60;

/// Live, shareable control surface for the trackers. The UI flips these (pause,
/// idle threshold) and the loop reads them each tick.
pub struct TrackerControl {
    /// User-controlled pause. Ignored while organization policy is managed.
    pub paused: AtomicBool,
    /// Setup/onboarding safety gate. This is independent from the user pause so a
    /// managed installation can stay stopped until setup completes without giving
    /// the employee a way to pause organization monitoring afterwards.
    pub in_setup: AtomicBool,
    /// Managed installations cannot locally pause or weaken organization policy.
    pub managed: AtomicBool,
    /// Server-controlled collection switch.
    pub org_monitoring_enabled: AtomicBool,
    /// Active/idle is mandatory in managed mode; these govern identifying details.
    pub collect_app_activity: AtomicBool,
    pub collect_window_titles: AtomicBool,
    pub collect_browser_activity: AtomicBool,
    pub idle_threshold_s: AtomicU64,
    pub screenshot_interval_s: AtomicU64,
    pub screenshot_retention_days: AtomicU64,
    /// Store only the site origin (scheme://host) for browser visits, not full URLs.
    pub domain_only: AtomicBool,
    /// Capture periodic screenshots (user opt-out; Windows: also gated on consent).
    pub capture_screenshots: AtomicBool,
    /// SHOT_MODE_PRIVACY | SHOT_MODE_NORMAL.
    pub screenshot_mode: AtomicU8,
    /// Skip the whole capture tick while any of these apps is frontmost
    /// (case-insensitive whole-word match on the active window's app name).
    /// Prefilled with the curated sensitive-app rules by default; user-editable.
    pub screenshot_skip_apps: RwLock<Vec<String>>,
    /// Structured managed privacy rules evaluated before any screenshot bytes are created.
    pub screenshot_privacy_rules: RwLock<Vec<crate::sync::client::PrivacyRule>>,
    /// Count keystrokes (user opt-out; Windows: also gated on consent).
    pub count_keystrokes: AtomicBool,
}

impl TrackerControl {
    pub fn new() -> Self {
        TrackerControl {
            paused: AtomicBool::new(false),
            in_setup: AtomicBool::new(true),
            managed: AtomicBool::new(false),
            org_monitoring_enabled: AtomicBool::new(true),
            collect_app_activity: AtomicBool::new(true),
            collect_window_titles: AtomicBool::new(true),
            collect_browser_activity: AtomicBool::new(true),
            idle_threshold_s: AtomicU64::new(DEFAULT_IDLE_THRESHOLD_S),
            screenshot_interval_s: AtomicU64::new(DEFAULT_SCREENSHOT_INTERVAL_S),
            screenshot_retention_days: AtomicU64::new(DEFAULT_RETENTION_DAYS),
            domain_only: AtomicBool::new(false),
            capture_screenshots: AtomicBool::new(true),
            screenshot_mode: AtomicU8::new(SHOT_MODE_PRIVACY),
            screenshot_skip_apps: RwLock::new(default_privacy_apps_flat()),
            screenshot_privacy_rules: RwLock::new(Vec::new()),
            count_keystrokes: AtomicBool::new(true),
        }
    }

    pub fn effective_paused(&self) -> bool {
        if self.in_setup.load(Ordering::Relaxed)
            || !self.org_monitoring_enabled.load(Ordering::Relaxed)
        {
            return true;
        }
        !self.managed.load(Ordering::Relaxed) && self.paused.load(Ordering::Relaxed)
    }

    pub fn collection_allowed(&self) -> bool {
        !self.effective_paused()
    }
}

/// Screenshot capture scopes. The old privacy/normal names remain accepted for
/// local settings compatibility, but managed policy uses the explicit three-way scope.
pub const SHOT_SCOPE_ACTIVE_WINDOW: u8 = 0;
pub const SHOT_SCOPE_ACTIVE_DISPLAY: u8 = 1;
pub const SHOT_SCOPE_ALL_DISPLAYS: u8 = 2;

/// Legacy aliases kept for older call sites/tests while the UI migrates.
pub const SHOT_MODE_PRIVACY: u8 = SHOT_SCOPE_ACTIVE_WINDOW;
pub const SHOT_MODE_NORMAL: u8 = SHOT_SCOPE_ALL_DISPLAYS;

/// Map persisted/new scope strings to the live atomic value. Unknown values fail
/// closed to active-window, the least expansive capture scope.
pub fn shot_mode_from_str(s: &str) -> u8 {
    match s {
        "active_display" => SHOT_SCOPE_ACTIVE_DISPLAY,
        "all_displays" | "normal" | "full_screen" => SHOT_SCOPE_ALL_DISPLAYS,
        _ => SHOT_SCOPE_ACTIVE_WINDOW,
    }
}

/// Baked-in copy of the curated sensitive-app list, grouped by category —
/// the offline fallback for the backend's `/v1/public/screenshot-privacy-apps`
/// (single source of truth: `handlers/privacyapps.go` — keep in sync). In
/// privacy mode a capture tick is skipped while any of these is frontmost.
pub const DEFAULT_PRIVACY_APPS: &[(&str, &[&str])] = &[
    (
        "Chat",
        &[
            "Zalo",
            "WhatsApp",
            "Telegram",
            "Signal",
            "Viber",
            "WeChat",
            "Weixin",
            "QQ",
            "LINE",
            "KakaoTalk",
            "Discord",
            "Messages",
            "FaceTime",
            "Element",
            "Threema",
            "Wire",
            "Beeper",
            "Ferdium",
            "Rambox",
            "Caprine",
        ],
    ),
    (
        "Security",
        &[
            "1Password",
            "Bitwarden",
            "LastPass",
            "KeePass",
            "KeePassXC",
            "Keeper",
            "NordPass",
            "Proton Pass",
            "Enpass",
            "RoboForm",
            "Keychain Access",
            "Passwords",
            "Ledger Live",
            "Trezor Suite",
            "Exodus",
            "Electrum",
            "Sparrow",
            "Proton VPN",
            "NordVPN",
            "TeamViewer",
            "AnyDesk",
        ],
    ),
    (
        "Work",
        &[
            "Slack",
            "Microsoft Teams",
            "Zoom",
            "zoom.us",
            "Webex",
            "DingTalk",
            "Lark",
            "Feishu",
            "Mattermost",
            "Rocket.Chat",
        ],
    ),
    (
        "Mail",
        &[
            "Mail",
            "Outlook",
            "Thunderbird",
            "Spark",
            "Proton Mail",
            "eM Client",
            "Mailbird",
            "Superhuman",
            "Airmail",
        ],
    ),
];

/// The baked-in list flattened for the matcher.
pub fn default_privacy_apps_flat() -> Vec<String> {
    DEFAULT_PRIVACY_APPS
        .iter()
        .flat_map(|(_, apps)| apps.iter().map(|a| a.to_string()))
        .collect()
}

impl Default for TrackerControl {
    fn default() -> Self {
        Self::new()
    }
}

fn now_ts() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

/// In-progress active-window interval, accumulating ACTIVE seconds only.
#[derive(Clone)]
struct Interval {
    start_ts: i64,
    app_name: String,
    title: Option<String>,
    pid: Option<i64>,
    duration_s: i64,
}

impl Interval {
    fn sample(&self) -> ActivitySample {
        ActivitySample {
            ts: self.start_ts,
            app_name: self.app_name.clone(),
            window_title: self.title.clone(),
            pid: self.pid,
            duration_s: self.duration_s,
        }
    }
}

/// Pure decision core for active-window tracking. Holds the open interval; each
/// `tick` returns `Some(sample)` when an interval should be persisted.
#[derive(Default)]
pub struct WindowTracker {
    current: Option<Interval>,
}

impl WindowTracker {
    /// Advance one poll step.
    /// - `active`: is the user present (idle < threshold) and not paused?
    /// - `win`: the foreground window, if any.
    /// Returns a sample to flush, or `None`.
    fn tick(
        &mut self,
        active: bool,
        win: Option<ActiveWindowInfo>,
        threshold_s: i64,
        now: i64,
    ) -> Option<ActivitySample> {
        // Not active (idle / paused) or no foreground window → close any interval.
        if !active {
            return self.close_idle(threshold_s);
        }
        let win = match win {
            Some(w) => w,
            None => return self.close(),
        };

        match self.current {
            Some(ref mut iv) if iv.app_name == win.app_name && iv.title == win.title => {
                iv.duration_s += TICK_S;
                if iv.duration_s >= MAX_CHUNK_S {
                    // Persist the chunk, then keep counting the same window fresh.
                    let out = iv.sample();
                    iv.start_ts = now;
                    iv.duration_s = 0;
                    Some(out)
                } else {
                    None
                }
            }
            _ => {
                let flushed = self.current.take().map(|iv| iv.sample());
                self.current = Some(Interval {
                    start_ts: now,
                    app_name: win.app_name,
                    title: win.title,
                    pid: Some(win.pid),
                    duration_s: TICK_S,
                });
                flushed
            }
        }
    }

    /// Close the interval because we went idle: drop the grace window we counted
    /// before detecting idle (retroactive trim), then emit it.
    fn close_idle(&mut self, threshold_s: i64) -> Option<ActivitySample> {
        self.current.take().and_then(|mut iv| {
            iv.duration_s = (iv.duration_s - threshold_s).max(0);
            if iv.duration_s > 0 {
                Some(iv.sample())
            } else {
                None
            }
        })
    }

    /// Close the interval as-is (e.g. no foreground window).
    fn close(&mut self) -> Option<ActivitySample> {
        self.current
            .take()
            .filter(|iv| iv.duration_s > 0)
            .map(|iv| iv.sample())
    }
}

/// Spawn the active-window + idle tracker on a background thread.
pub fn start(db: Arc<Db>, control: Arc<TrackerControl>) {
    thread::spawn(move || run(db, control));
}

// ---------- keyboard counter (task 17) ----------

/// Keypress counts are flushed at this cadence.
const KEY_FLUSH: Duration = Duration::from_secs(15);
/// Counts are bucketed by this window (start-of-bucket epoch is the key).
const KEY_BUCKET_S: i64 = 60;

/// Default seconds between periodic screenshots.
pub const DEFAULT_SCREENSHOT_INTERVAL_S: u64 = 300;

/// Default days to keep screenshots before cleanup.
pub const DEFAULT_RETENTION_DAYS: u64 = 30;

/// Spawn the keyboard counter: a minimal CoreGraphics tap that only *counts* key
/// presses (the key is never decoded or stored — see `platform::run_keyboard_tap`),
/// plus a flusher that writes per-minute counts. Pause-aware.
pub fn start_keyboard(db: Arc<Db>, control: Arc<TrackerControl>) {
    // Flusher: move the tap's running count into the current bucket.
    {
        thread::spawn(move || loop {
            thread::sleep(KEY_FLUSH);
            let n = crate::platform::KEY_PRESS_COUNT.swap(0, Ordering::Relaxed);
            // Drop counts accumulated while paused or with keystroke counting opted
            // out / not yet consented (don't persist them).
            if n > 0
                && control.collection_allowed()
                && control.count_keystrokes.load(Ordering::Relaxed)
            {
                let now = now_ts();
                let bucket = now - now.rem_euclid(KEY_BUCKET_S);
                if let Err(e) = db.add_keystrokes(bucket, n as i64) {
                    crate::log_warn!("keyboard", "failed to write keystroke_bucket: {e}");
                }
            }
        });
    }

    // Tap: blocks while active; returns if it can't be created (permission not yet
    // granted) — retry so it starts as soon as the user grants Input Monitoring.
    // Only attempt once granted: the bare CGEventTapCreate attempt registers the
    // app with TCC and can surface the OS prompt, which must stay user-initiated.
    thread::spawn(|| loop {
        use crate::platform::{permission_status, Permission, PermissionState};
        if permission_status(Permission::InputMonitoring) == PermissionState::Granted {
            crate::platform::run_keyboard_tap();
        }
        thread::sleep(Duration::from_secs(3));
    });
}

// ---------- screenshots (task 19) ----------

/// Hard ceiling on a stored/uploaded screenshot. The backend enforces its own
/// (larger) guard; we keep every shot comfortably under this. See docs/11.
const SCREENSHOT_MAX_BYTES: usize = 50 * 1024;

/// Candidate long-edge sizes (px) and WebP qualities, tried in order. We step
/// quality down first, then resolution, until a shot fits SCREENSHOT_MAX_BYTES.
const SHOT_MAX_DIMS: [u32; 3] = [1366, 1152, 960];
const SHOT_QUALITIES: [f32; 5] = [55.0, 45.0, 35.0, 25.0, 20.0];

/// Downscale to a max long-edge and encode lossy WebP, returning the smallest
/// result that fits SCREENSHOT_MAX_BYTES. If even the floor (smallest dims +
/// lowest quality) exceeds the cap, the smallest encoding produced is returned.
/// Returns the encoded bytes plus the final (width, height).
fn compress_to_webp(img: &xcap::image::RgbaImage) -> (Vec<u8>, u32, u32) {
    use xcap::image::imageops::{self, FilterType};

    let mut smallest: Option<(Vec<u8>, u32, u32)> = None;
    for &max_dim in &SHOT_MAX_DIMS {
        let (ow, oh) = (img.width(), img.height());
        let long_edge = ow.max(oh);
        let resized;
        let (w, h) = if long_edge > max_dim {
            let scale = max_dim as f32 / long_edge as f32;
            let nw = (ow as f32 * scale).round().max(1.0) as u32;
            let nh = (oh as f32 * scale).round().max(1.0) as u32;
            resized = imageops::resize(img, nw, nh, FilterType::Triangle);
            (nw, nh)
        } else {
            // Already small enough; encode the original buffer at each quality.
            resized = img.clone();
            (ow, oh)
        };

        for &q in &SHOT_QUALITIES {
            let encoded = webp::Encoder::from_rgba(resized.as_raw(), w, h).encode(q);
            let bytes = encoded.to_vec();
            if bytes.len() <= SCREENSHOT_MAX_BYTES {
                return (bytes, w, h);
            }
            if smallest
                .as_ref()
                .map_or(true, |(b, ..)| bytes.len() < b.len())
            {
                smallest = Some((bytes, w, h));
            }
        }
    }
    // Nothing fit the cap (extremely detailed screen) — keep the smallest.
    smallest.expect("at least one encoding attempt")
}

/// The active app is on the privacy skip-list. Forgiving match: case-insensitive,
/// and the entry only has to appear as a whole word in the reported app name —
/// "Zalo" matches "zalo", "Zalo PC", and "zalo - the best app", while "LINE"
/// does not match "Outline".
fn should_skip(app_name: &str, skip_apps: &[String]) -> bool {
    let name = app_name.to_lowercase();
    skip_apps.iter().any(|s| {
        let pat = s.trim().to_lowercase();
        !pat.is_empty() && contains_word(&name, &pat)
    })
}

/// `pat` occurs in `name` bounded by non-alphanumeric characters (or the string
/// ends), so partial words never match.
fn contains_word(name: &str, pat: &str) -> bool {
    let mut from = 0;
    while let Some(off) = name[from..].find(pat) {
        let b = from + off;
        let e = b + pat.len();
        let before = name[..b].chars().next_back();
        let after = name[e..].chars().next();
        if before.is_none_or(|c| !c.is_alphanumeric()) && after.is_none_or(|c| !c.is_alphanumeric())
        {
            return true;
        }
        from = b + name[b..].chars().next().map_or(1, |c| c.len_utf8());
    }
    false
}

/// Match a structured organization privacy rule against the foreground window.
/// Matching is case-insensitive and happens before any capture call.
fn privacy_rule_matches(rule: &crate::sync::client::PrivacyRule, active: &ActiveWindowInfo) -> bool {
    if !rule.enabled {
        return false;
    }
    let pattern = rule.pattern.trim().to_lowercase();
    if pattern.is_empty() {
        return false;
    }
    let kind = rule.kind.trim().to_lowercase();
    let match_type = rule.match_type.trim().to_lowercase();
    match kind.as_str() {
        "app" => {
            let value = active.app_name.to_lowercase();
            match match_type.as_str() {
                "exact" => value == pattern,
                "contains" => value.contains(&pattern),
                _ => false,
            }
        }
        "window_title" if match_type == "contains" => active
            .title
            .as_deref()
            .map(|title| title.to_lowercase().contains(&pattern))
            .unwrap_or(false),
        _ => false,
    }
}

/// Resolve the xcap window that best represents the foreground window.
fn find_active_capture_window(active: &ActiveWindowInfo) -> Option<xcap::Window> {
    let windows = xcap::Window::all().ok()?;
    let title = active.title.as_deref().unwrap_or("");
    windows
        .into_iter()
        .filter(|w| {
            w.pid().map(|p| p as i64 == active.pid).unwrap_or(false)
                && !w.is_minimized().unwrap_or(true)
        })
        .max_by_key(|w| {
            let title_match = !title.is_empty() && w.title().map(|t| t == title).unwrap_or(false);
            (title_match, w.z().unwrap_or(i32::MIN))
        })
}

/// Active-window mode: capture only the frontmost window. Candidates are the
/// app's (pid-matched) non-minimized windows; among them prefer the one whose
/// title equals the active window's title, then the topmost by z-order — an app
/// can have several windows (e.g. two Chrome windows) and size says nothing
/// about which is in front. Returns `None` on any lookup/capture miss so the
/// caller falls back to a full-screen shot.
fn capture_active_window(
    db: &Db,
    dir: &Path,
    active: &ActiveWindowInfo,
    now: i64,
) -> Option<usize> {
    let win = find_active_capture_window(active)?;
    let img = win.capture_image().ok()?;
    let (bytes, w, h) = compress_to_webp(&img);
    let path = dir.join(format!("{now}_window.webp"));
    if let Err(e) = std::fs::write(&path, &bytes) {
        crate::log_warn!("screenshot", "save failed: {e}");
        return None;
    }
    let shot = Screenshot {
        ts: now,
        file_path: path.to_string_lossy().into_owned(),
        display_id: None,
        width: Some(w as i64),
        height: Some(h as i64),
    };
    if let Err(e) = db.insert_screenshot(&shot) {
        crate::log_warn!("screenshot", "db insert failed: {e}");
        return None;
    }
    Some(1)
}

/// Active-display mode: capture exactly the monitor containing the foreground
/// window. A lookup miss drops the tick rather than widening the scope.
fn capture_active_display(
    db: &Db,
    dir: &Path,
    active: &ActiveWindowInfo,
    now: i64,
) -> Option<usize> {
    let win = find_active_capture_window(active)?;
    let monitor = win.current_monitor().ok()?;
    let img = monitor.capture_image().ok()?;
    let (bytes, w, h) = compress_to_webp(&img);
    let path = dir.join(format!("{now}_active_display.webp"));
    if let Err(e) = std::fs::write(&path, &bytes) {
        crate::log_warn!("screenshot", "save failed: {e}");
        return None;
    }
    let shot = Screenshot {
        ts: now,
        file_path: path.to_string_lossy().into_owned(),
        display_id: None,
        width: Some(w as i64),
        height: Some(h as i64),
    };
    if let Err(e) = db.insert_screenshot(&shot) {
        crate::log_warn!("screenshot", "db insert failed: {e}");
        return None;
    }
    Some(1)
}

/// Take one capture tick: evaluate privacy exclusions first, then capture exactly
/// the configured scope. Scope misses never fall back to a broader capture.
/// skip-list; otherwise capture per the configured mode (active-window shots
/// fall back to full screen on a miss), compress to ≤50 KB WebP under `dir`,
/// and record each shot in the DB. Returns how many shots were saved.
/// Requires Screen Recording.
pub fn capture_once(db: &Db, dir: &Path, control: &TrackerControl) -> usize {
    if !control.collection_allowed() {
        return 0;
    }

    let scope = control.screenshot_mode.load(Ordering::Relaxed);
    let active = crate::platform::active_window();

    // Privacy rules are scope-independent and must be checked before a screenshot
    // is created. Keep the legacy exact-app list active as a compatibility layer.
    if let Some(ref win) = active {
        let matched_rule = {
            let rules = control.screenshot_privacy_rules.read().unwrap();
            rules.iter().any(|rule| privacy_rule_matches(rule, win))
        };
        let matched_legacy = {
            let skip = control.screenshot_skip_apps.read().unwrap();
            should_skip(&win.app_name, &skip)
        };
        if matched_rule || matched_legacy {
            crate::log_info!(
                "screenshot",
                "skipped tick: privacy rule matched {}",
                win.app_name
            );
            return 0;
        }
    }

    if let Err(e) = std::fs::create_dir_all(dir) {
        crate::log_warn!("screenshot", "create dir failed: {e}");
        return 0;
    }

    let now = now_ts();
    match scope {
        SHOT_SCOPE_ACTIVE_WINDOW => {
            let Some(ref win) = active else {
                crate::log_info!("screenshot", "active-window capture skipped: no foreground window");
                return 0;
            };
            match capture_active_window(db, dir, win, now) {
                Some(saved) => saved,
                None => {
                    crate::log_info!(
                        "screenshot",
                        "active-window capture missed; tick dropped without broader fallback"
                    );
                    0
                }
            }
        }
        SHOT_SCOPE_ACTIVE_DISPLAY => {
            let Some(ref win) = active else {
                crate::log_info!("screenshot", "active-display capture skipped: no foreground window");
                return 0;
            };
            match capture_active_display(db, dir, win, now) {
                Some(saved) => saved,
                None => {
                    crate::log_info!(
                        "screenshot",
                        "active-display capture missed; tick dropped without broader fallback"
                    );
                    0
                }
            }
        }
        _ => {
            let monitors = match xcap::Monitor::all() {
                Ok(m) => m,
                Err(e) => {
                    crate::log_warn!("screenshot", "enumerate monitors failed: {e}");
                    return 0;
                }
            };

            let capture_group_id = uuid::Uuid::new_v4().to_string();
            let mut saved = 0;
            for (i, monitor) in monitors.into_iter().enumerate() {
                let img = match monitor.capture_image() {
                    Ok(img) => img,
                    Err(e) => {
                        crate::log_warn!("screenshot", "capture failed: {e}");
                        continue;
                    }
                };
                let (bytes, w, h) = compress_to_webp(&img);
                let path = dir.join(format!("{now}_display{i}.webp"));
                if let Err(e) = std::fs::write(&path, &bytes) {
                    crate::log_warn!("screenshot", "save failed: {e}");
                    continue;
                }
                let shot = Screenshot {
                    ts: now,
                    file_path: path.to_string_lossy().into_owned(),
                    display_id: Some(i as i64),
                    width: Some(w as i64),
                    height: Some(h as i64),
                };
                if let Err(e) = db.insert_screenshot_with_group(&shot, Some(&capture_group_id)) {
                    crate::log_warn!("screenshot", "db insert failed: {e}");
                    continue;
                }
                saved += 1;
            }
            saved
        }
    }
}

/// Spawn the screenshot retention job (task 29): periodically delete screenshots
/// older than the configured age cap, removing both files and DB rows.
pub fn start_cleanup(db: Arc<Db>, control: Arc<TrackerControl>) {
    thread::spawn(move || loop {
        let days = control
            .screenshot_retention_days
            .load(Ordering::Relaxed)
            .max(1);
        let cutoff = now_ts() - (days as i64) * 86_400;
        match db.delete_screenshots_before(cutoff) {
            Ok(paths) => {
                for p in paths {
                    let _ = std::fs::remove_file(&p);
                }
            }
            Err(e) => crate::log_warn!("cleanup", "screenshot prune failed: {e}"),
        }
        // Run hourly (and once shortly after startup via the first sleep being short).
        thread::sleep(Duration::from_secs(3600));
    });
}

/// Spawn the periodic screenshot taker. Permission-gated and pause-aware.
pub fn start_screenshots(db: Arc<Db>, control: Arc<TrackerControl>, dir: std::path::PathBuf) {
    use crate::platform::{permission_status, Permission, PermissionState};
    thread::spawn(move || loop {
        let interval = control.screenshot_interval_s.load(Ordering::Relaxed).max(5);
        thread::sleep(Duration::from_secs(interval));
        if !control.collection_allowed() {
            continue;
        }
        // Opt-out (and, on Windows, consent) gate.
        if !control.capture_screenshots.load(Ordering::Relaxed) {
            continue;
        }
        if permission_status(Permission::ScreenRecording) != PermissionState::Granted {
            continue;
        }
        capture_once(&db, &dir, &control);
    });
}

fn run(db: Arc<Db>, control: Arc<TrackerControl>) {
    let mut tracker = WindowTracker::default();

    loop {
        thread::sleep(POLL);

        let threshold = control.idle_threshold_s.load(Ordering::Relaxed) as i64;
        // Idle covers screen-locked / display-asleep too: no input → idle grows.
        let idle = crate::platform::idle_seconds();
        let active = control.collection_allowed() && idle < threshold as f64;

        let collect_apps = control.collect_app_activity.load(Ordering::Relaxed);
        let collect_titles = control.collect_window_titles.load(Ordering::Relaxed);
        let win = if active {
            if collect_apps {
                crate::platform::active_window().map(|mut window| {
                    if !collect_titles {
                        window.title = None;
                    }
                    window
                })
            } else {
                // Preserve mandatory active/idle duration without collecting the
                // foreground application or window identity.
                Some(ActiveWindowInfo {
                    app_name: String::new(),
                    title: None,
                    pid: 0,
                })
            }
        } else {
            None
        };

        if let Some(sample) = tracker.tick(active, win, threshold, now_ts()) {
            if sample.duration_s > 0 {
                if let Err(e) = db.insert_activity_sample(&sample) {
                    crate::log_warn!("tracker", "failed to write activity_sample: {e}");
                    crate::obs::capture_error(&e);
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn win(app: &str, title: &str) -> ActiveWindowInfo {
        ActiveWindowInfo {
            app_name: app.to_string(),
            title: Some(title.to_string()),
            pid: 1,
        }
    }

    /// Build a synthetic "screen": a smooth gradient (compresses well) plus a
    /// noisy strip (hard to compress), at a large 2560x1440 so resolution must
    /// be reduced. Exercises the full quality+resolution fallback.
    fn synthetic_screen() -> xcap::image::RgbaImage {
        let (w, h) = (2560u32, 1440u32);
        xcap::image::RgbaImage::from_fn(w, h, |x, y| {
            let noisy = y < h / 4; // top quarter is high-frequency noise
            let n = if noisy {
                ((x.wrapping_mul(2654435761)
                    .wrapping_add(y.wrapping_mul(40503)))
                    & 0xFF) as u8
            } else {
                0
            };
            xcap::image::Rgba([
                ((x * 255 / w) as u8).wrapping_add(n),
                ((y * 255 / h) as u8).wrapping_add(n),
                128u8.wrapping_add(n),
                255,
            ])
        })
    }

    #[test]
    fn screenshot_compresses_under_cap() {
        let img = synthetic_screen();
        let (bytes, w, h) = compress_to_webp(&img);
        assert!(
            bytes.len() <= SCREENSHOT_MAX_BYTES,
            "compressed size {} exceeds cap {}",
            bytes.len(),
            SCREENSHOT_MAX_BYTES
        );
        // It's a valid WebP (RIFF....WEBP container).
        assert_eq!(&bytes[0..4], b"RIFF");
        assert_eq!(&bytes[8..12], b"WEBP");
        // Large source → downscaled to one of the candidate long edges.
        assert!(w.max(h) <= SHOT_MAX_DIMS[0], "not downscaled: {w}x{h}");
        assert!(w > 0 && h > 0);
    }

    #[test]
    fn small_screen_not_upscaled() {
        // A source already under the smallest candidate keeps its dimensions.
        let img =
            xcap::image::RgbaImage::from_pixel(800, 600, xcap::image::Rgba([20, 60, 90, 255]));
        let (bytes, w, h) = compress_to_webp(&img);
        assert!(bytes.len() <= SCREENSHOT_MAX_BYTES);
        assert_eq!((w, h), (800, 600));
    }

    #[test]
    fn screenshot_scope_parser_is_backward_compatible() {
        assert_eq!(shot_mode_from_str("active_window"), SHOT_SCOPE_ACTIVE_WINDOW);
        assert_eq!(shot_mode_from_str("privacy"), SHOT_SCOPE_ACTIVE_WINDOW);
        assert_eq!(shot_mode_from_str("active_display"), SHOT_SCOPE_ACTIVE_DISPLAY);
        assert_eq!(shot_mode_from_str("all_displays"), SHOT_SCOPE_ALL_DISPLAYS);
        assert_eq!(shot_mode_from_str("normal"), SHOT_SCOPE_ALL_DISPLAYS);
        assert_eq!(shot_mode_from_str("unknown"), SHOT_SCOPE_ACTIVE_WINDOW);
    }

    #[test]
    fn structured_privacy_rules_match_before_capture() {
        let active = ActiveWindowInfo {
            app_name: "Google Chrome".into(),
            title: Some("Payroll — Q3 review".into()),
            pid: 42,
        };
        let app_exact = crate::sync::client::PrivacyRule {
            id: "1".into(),
            kind: "app".into(),
            match_type: "exact".into(),
            pattern: "google chrome".into(),
            enabled: true,
        };
        let app_contains = crate::sync::client::PrivacyRule {
            id: "2".into(),
            kind: "app".into(),
            match_type: "contains".into(),
            pattern: "chrome".into(),
            enabled: true,
        };
        let title_contains = crate::sync::client::PrivacyRule {
            id: "3".into(),
            kind: "window_title".into(),
            match_type: "contains".into(),
            pattern: "PAYROLL".into(),
            enabled: true,
        };
        assert!(privacy_rule_matches(&app_exact, &active));
        assert!(privacy_rule_matches(&app_contains, &active));
        assert!(privacy_rule_matches(&title_contains, &active));

        let mut disabled = title_contains.clone();
        disabled.enabled = false;
        assert!(!privacy_rule_matches(&disabled, &active));
    }

    #[test]
    fn skip_list_matches_whole_words_case_insensitively() {
        let list = vec![
            "Zalo".to_string(),
            "keychain access".to_string(),
            "LINE".to_string(),
        ];
        assert!(should_skip("Zalo", &list));
        assert!(should_skip("zalo", &list));
        assert!(should_skip("ZALO", &list));
        assert!(should_skip("Zalo PC", &list));
        assert!(should_skip("zalo - the best app", &list));
        assert!(should_skip("zalo.exe", &list));
        assert!(should_skip("Keychain Access", &list));
        assert!(should_skip("line", &list));
        // Whole words only — no partial-word matching.
        assert!(!should_skip("Outline", &list));
        assert!(!should_skip("Zalos", &list));
        assert!(!should_skip("Slack", &list));
        assert!(!should_skip("Anything", &[]));
        // Blank entries never match everything.
        assert!(!should_skip("Anything", &["  ".to_string()]));
    }

    #[test]
    fn shot_mode_parses_with_safe_fallback() {
        assert_eq!(shot_mode_from_str("privacy"), SHOT_MODE_PRIVACY);
        assert_eq!(shot_mode_from_str("normal"), SHOT_MODE_NORMAL);
        // Pre-rename values from old settings.json / policies.
        assert_eq!(shot_mode_from_str("full_screen"), SHOT_MODE_NORMAL);
        assert_eq!(shot_mode_from_str("active_window"), SHOT_MODE_PRIVACY);
        // Unknown / future values fall back to privacy (captures the least).
        assert_eq!(shot_mode_from_str(""), SHOT_MODE_PRIVACY);
        assert_eq!(shot_mode_from_str("blur"), SHOT_MODE_PRIVACY);
    }

    #[test]
    fn baked_privacy_apps_cover_known_sensitive_apps() {
        let apps = default_privacy_apps_flat();
        assert!(apps.len() > 50);
        // Spot-check the matcher against reported-name variants.
        assert!(should_skip("Zalo", &apps));
        assert!(should_skip("Telegram Desktop", &apps));
        assert!(should_skip("zoom.us", &apps));
        assert!(should_skip("Weixin", &apps));
        assert!(should_skip("Microsoft Outlook", &apps));
        assert!(!should_skip("Google Chrome", &apps));
        assert!(!should_skip("Visual Studio Code", &apps));
    }

    #[test]
    fn accumulates_then_flushes_on_window_change() {
        let mut t = WindowTracker::default();
        // Same window for 3 ticks → no flush yet.
        assert!(t.tick(true, Some(win("Code", "a")), 60, 100).is_none());
        assert!(t.tick(true, Some(win("Code", "a")), 60, 101).is_none());
        assert!(t.tick(true, Some(win("Code", "a")), 60, 102).is_none());
        // Switch window → previous interval flushes with 3s.
        let flushed = t.tick(true, Some(win("Chrome", "b")), 60, 103).unwrap();
        assert_eq!(flushed.app_name, "Code");
        assert_eq!(flushed.duration_s, 3);
        assert_eq!(flushed.ts, 100);
    }

    #[test]
    fn idle_trims_grace_window() {
        let mut t = WindowTracker::default();
        let threshold = 5;
        // 8 active ticks on one window.
        for i in 0..8 {
            assert!(t
                .tick(true, Some(win("Code", "a")), threshold, 100 + i)
                .is_none());
        }
        // Go idle → trim `threshold` seconds of grace from the 8 counted.
        let flushed = t.tick(false, None, threshold, 108).unwrap();
        assert_eq!(flushed.duration_s, 8 - threshold);
    }

    #[test]
    fn short_interval_fully_idle_writes_nothing() {
        let mut t = WindowTracker::default();
        let threshold = 60;
        // Only 2s of activity, then idle: 2 - 60 clamps to 0 → no row.
        t.tick(true, Some(win("Code", "a")), threshold, 100);
        t.tick(true, Some(win("Code", "a")), threshold, 101);
        assert!(t.tick(false, None, threshold, 102).is_none());
    }

    #[test]
    fn long_interval_chunks_at_max() {
        let mut t = WindowTracker::default();
        let mut flushes = 0;
        // Run one window for MAX_CHUNK_S + a bit; expect a chunk flush at the cap.
        for i in 0..(MAX_CHUNK_S + 3) {
            if t.tick(true, Some(win("Code", "a")), 600, 100 + i).is_some() {
                flushes += 1;
            }
        }
        assert_eq!(flushes, 1, "should flush exactly one chunk at the cap");
    }

    #[test]
    fn no_foreground_window_closes_interval() {
        let mut t = WindowTracker::default();
        t.tick(true, Some(win("Code", "a")), 60, 100);
        t.tick(true, Some(win("Code", "a")), 60, 101);
        // Active but no foreground window → flush what we had.
        let flushed = t.tick(true, None, 60, 102).unwrap();
        assert_eq!(flushed.app_name, "Code");
        assert_eq!(flushed.duration_s, 2);
    }
}
