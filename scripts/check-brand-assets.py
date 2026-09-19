#!/usr/bin/env python3
"""Fail CI when active ActiLens branding drifts from the canonical identity."""

from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]

REQUIRED = [
    "assets/brand/README.md",
    "assets/brand/svg/mark.svg",
    "assets/brand/svg/mark-white.svg",
    "assets/brand/svg/mark-mono.svg",
    "assets/brand/svg/lockup.svg",
    "assets/brand/svg/lockup-inverse.svg",
    "assets/brand/svg/app-icon.svg",
    "assets/brand/png/app-icon-16.png",
    "assets/brand/png/app-icon-32.png",
    "assets/brand/png/app-icon-48.png",
    "assets/brand/png/app-icon-128.png",
    "assets/brand/png/app-icon-180.png",
    "assets/brand/png/app-icon-256.png",
    "assets/brand/platform/favicon.ico",
    "assets/brand/platform/icon.icns",
    "apps/web-admin/public/brand/mark.svg",
    "apps/web-admin/public/brand/app-icon.svg",
    "apps/web-admin/public/favicon.svg",
    "apps/web-admin/public/favicon.ico",
    "apps/web-admin/public/brand/apple-touch-icon.png",
    "apps/desktop/src/assets/brand-mark.svg",
    "apps/desktop/src/assets/brand-lockup.svg",
    "apps/desktop/src/assets/brand-lockup-inverse.svg",
    "apps/desktop/src-tauri/icons/32x32.png",
    "apps/desktop/src-tauri/icons/128x128.png",
    "apps/desktop/src-tauri/icons/128x128@2x.png",
    "apps/desktop/src-tauri/icons/icon.ico",
    "apps/desktop/src-tauri/icons/icon.icns",
    "apps/desktop/src-tauri/icons/tray/tray-tracking.png",
    "apps/desktop/src-tauri/icons/tray/tray-idle.png",
    "apps/desktop/src-tauri/icons/tray/tray-paused.png",
    "apps/extension/icons/16.png",
    "apps/extension/icons/32.png",
    "apps/extension/icons/48.png",
    "apps/extension/icons/128.png",
    "apps/extension/icons/brand-mark.svg",
]

LEGACY_MUST_NOT_EXIST = [
    "apps/desktop/src/assets/lockup-light.png",
    "apps/desktop/src/assets/lockup-dark.png",
    "apps/desktop/src/assets/mark-accent.png",
]

TEXT_EXPECTATIONS = {
    "apps/web-admin/src/components/AppShell.tsx": '/brand/mark.svg',
    "apps/web-admin/src/auth/AuthLayout.tsx": '/brand/mark.svg',
    "apps/desktop/src/App.tsx": 'assets/brand-mark.svg',
    "apps/desktop/src/ui.tsx": 'assets/brand-mark.svg',
    "apps/extension/popup.html": 'icons/brand-mark.svg',
    "scripts/gen-icons.py": 'assets" / "brand" / "png" / "app-icon-256.png',
    "scripts/gen-env-icons.py": 'assets" / "brand" / "png" / "app-icon-256.png',
}

# Exact fragments from the superseded pulse/heartbeat placeholder marks.
FORBIDDEN_TEXT = [
    "M3 12h4l2-5 4 10 2-5h6",
    "M4.5 12 h3.2 l1.8 -4.4 l2.4 8.8 l1.8 -4.4 h4.5",
    "PULSE = [(4.5, 12)",
]

ACTIVE_TEXT = [
    "apps/web-admin/src/components/AppShell.tsx",
    "apps/web-admin/src/auth/AuthLayout.tsx",
    "apps/desktop/src/App.tsx",
    "apps/desktop/src/ui.tsx",
    "apps/extension/popup.html",
    "scripts/gen-icons.py",
    "scripts/gen-env-icons.py",
]

PNG_MAGIC = b"\x89PNG\r\n\x1a\n"
PNG_FILES = [
    "apps/web-admin/public/brand/apple-touch-icon.png",
    "apps/desktop/src-tauri/icons/32x32.png",
    "apps/desktop/src-tauri/icons/128x128.png",
    "apps/desktop/src-tauri/icons/128x128@2x.png",
    "apps/desktop/src-tauri/icons/tray/tray-tracking.png",
    "apps/desktop/src-tauri/icons/tray/tray-idle.png",
    "apps/desktop/src-tauri/icons/tray/tray-paused.png",
    "apps/extension/icons/16.png",
    "apps/extension/icons/32.png",
    "apps/extension/icons/48.png",
    "apps/extension/icons/128.png",
]


def fail(message):
    raise SystemExit(f"Brand integrity check failed: {message}")


for relative in REQUIRED:
    path = ROOT / relative
    if not path.is_file() or path.stat().st_size == 0:
        fail(f"missing or empty required asset: {relative}")

for relative in LEGACY_MUST_NOT_EXIST:
    if (ROOT / relative).exists():
        fail(f"legacy brand asset must be removed: {relative}")

for relative, needle in TEXT_EXPECTATIONS.items():
    source = (ROOT / relative).read_text(encoding="utf-8")
    if needle not in source:
        fail(f"{relative} no longer references canonical branding ({needle!r})")

for relative in ACTIVE_TEXT:
    source = (ROOT / relative).read_text(encoding="utf-8")
    for needle in FORBIDDEN_TEXT:
        if needle in source:
            fail(f"legacy pulse mark found in {relative}: {needle}")

for relative in PNG_FILES:
    if (ROOT / relative).read_bytes()[:8] != PNG_MAGIC:
        fail(f"invalid PNG signature: {relative}")

for relative in (
    "assets/brand/platform/favicon.ico",
    "apps/web-admin/public/favicon.ico",
    "apps/desktop/src-tauri/icons/icon.ico",
    "apps/desktop/src-tauri/icons/dev/icon.ico",
    "apps/desktop/src-tauri/icons/staging/icon.ico",
):
    if (ROOT / relative).read_bytes()[:4] != b"\x00\x00\x01\x00":
        fail(f"invalid ICO header: {relative}")

for relative in (
    "assets/brand/platform/icon.icns",
    "apps/desktop/src-tauri/icons/icon.icns",
    "apps/desktop/src-tauri/icons/dev/icon.icns",
    "apps/desktop/src-tauri/icons/staging/icon.icns",
):
    if (ROOT / relative).read_bytes()[:4] != b"icns":
        fail(f"invalid ICNS header: {relative}")

for relative in ("scripts/gen-icons.py", "scripts/gen-env-icons.py"):
    source = (ROOT / relative).read_text(encoding="utf-8")
    if 'bitmap_format="bmp"' not in source:
        fail(f"{relative} must keep decoder-safe BMP-backed ICO generation")

print("ActiLens brand integrity: OK")
