// actilens popup — shows link status, current page, today's count, pause toggle.

const CANDIDATE_PORTS = [47615, 48291, 49377, 50603, 51719, 52837];

const $ = (id) => document.getElementById(id);

async function probe() {
  for (const port of CANDIDATE_PORTS) {
    try {
      const res = await fetch(`http://127.0.0.1:${port}/whoami`);
      if (res.ok) {
        const j = await res.json();
        if (j && j.app === "actilens") {
          return {
            port,
            managed: !!j.managed,
            browserTrackingEnabled: j.browser_tracking_enabled !== false,
          };
        }
      }
    } catch (_) {}
  }
  return null;
}

async function render() {
  const link = await probe();
  const statusEl = $("status");
  if (link) {
    statusEl.textContent = "Connected";
    statusEl.className = "pill ok";
    $("port").textContent = String(link.port);
  } else {
    statusEl.textContent = "App not found";
    statusEl.className = "pill bad";
    $("port").textContent = "—";
  }

  const { current } = await chrome.storage.session.get("current");
  if (current && current.url) {
    $("page").textContent = current.title || current.url;
    try {
      $("pageMeta").textContent = new URL(current.url).hostname;
    } catch (_) {
      $("pageMeta").textContent = "";
    }
  } else {
    $("page").textContent = "—";
    $("pageMeta").textContent = "";
  }

  const { count, countDay } = await chrome.storage.local.get(["count", "countDay"]);
  $("count").textContent = countDay === new Date().toDateString() ? count || 0 : 0;

  const { paused } = await chrome.storage.local.get("paused");
  const managed = !!link?.managed;
  const policyEnabled = link?.browserTrackingEnabled !== false;
  const effectivePaused = managed ? !policyEnabled : !!paused;
  const toggle = $("toggle");

  toggle.className = effectivePaused ? "switch off" : "switch";
  toggle.setAttribute("aria-pressed", String(!effectivePaused));
  toggle.disabled = managed || !policyEnabled;

  const help = $("trackingHelp");
  if (managed) {
    help.textContent = policyEnabled
      ? "Managed by your organization"
      : "Disabled by your organization";
  } else {
    help.textContent = "Send active-tab activity to the local app";
  }
}

$("toggle").addEventListener("click", async () => {
  const link = await probe();
  if (link?.managed || link?.browserTrackingEnabled === false) return;
  const { paused } = await chrome.storage.local.get("paused");
  await chrome.storage.local.set({ paused: !paused });
  render();
});

render();
