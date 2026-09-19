#!/usr/bin/env python3
"""Regenerate ActiLens product icons from the canonical brand raster seed.

The vector source of truth is assets/brand/svg/app-icon.svg. The checked-in
assets/brand/png/app-icon-256.png is the deterministic Pillow-compatible seed
used by this script so icon generation does not depend on an SVG renderer.

Writes:
  - apps/desktop/src-tauri/icons/ production desktop/Windows icon assets
  - apps/desktop/src-tauri/icons/tray/ compact state-aware ActiLens tray glyphs
  - apps/extension/icons/ browser-extension icons

Run:
    python3 scripts/gen-icons.py
    python3 scripts/gen-env-icons.py

Requires Pillow only. Windows ICO output intentionally uses a BMP-backed
32x32 frame because Tauri's strict icon decoder rejects malformed embedded PNG
frames and the 32px frame is sufficient for the executable/resource pipeline.
"""

from pathlib import Path
from PIL import Image, ImageDraw

ROOT = Path(__file__).resolve().parents[1]
BRAND_SEED = ROOT / "assets" / "brand" / "png" / "app-icon-256.png"
ICONS = ROOT / "apps" / "desktop" / "src-tauri" / "icons"
TRAY_DIR = ICONS / "tray"
EXT_DIR = ROOT / "apps" / "extension" / "icons"

RESAMPLE = Image.Resampling.LANCZOS
GREEN = (47, 168, 90, 255)
AMBER = (224, 164, 0, 255)
RED = (217, 69, 60, 255)


def brand_master(size=1024):
    """Return the canonical square ActiLens app icon at the requested size."""
    if not BRAND_SEED.exists():
        raise FileNotFoundError(
            f"Missing brand seed: {BRAND_SEED}. "
            "Restore it from assets/brand before generating icons."
        )
    return Image.open(BRAND_SEED).convert("RGBA").resize((size, size), RESAMPLE)


def draw_tray_mark(draw, size, color):
    """Draw a compact A + focus-point glyph for state-aware tray icons."""
    scale = size / 44.0
    width = max(2, round(4.6 * scale))
    points = [
        (10 * scale, 35 * scale),
        (22 * scale, 9 * scale),
        (34 * scale, 35 * scale),
    ]
    draw.line(points, fill=color, width=width, joint="curve")
    draw.line(
        [(26 * scale, 35 * scale), (37 * scale, 35 * scale)],
        fill=color,
        width=width,
    )
    radius = 3.2 * scale
    cx, cy = 22 * scale, 27 * scale
    draw.ellipse(
        [cx - radius, cy - radius, cx + radius, cy + radius],
        fill=color,
    )


def write_app_set(master):
    ICONS.mkdir(parents=True, exist_ok=True)
    sizes = {
        "32x32.png": 32,
        "64x64.png": 64,
        "128x128.png": 128,
        "128x128@2x.png": 256,
        "icon.png": 512,
        "StoreLogo.png": 50,
        "Square30x30Logo.png": 30,
        "Square44x44Logo.png": 44,
        "Square71x71Logo.png": 71,
        "Square89x89Logo.png": 89,
        "Square107x107Logo.png": 107,
        "Square142x142Logo.png": 142,
        "Square150x150Logo.png": 150,
        "Square284x284Logo.png": 284,
        "Square310x310Logo.png": 310,
    }
    for name, px in sizes.items():
        master.resize((px, px), RESAMPLE).save(ICONS / name, optimize=True)

    # Keep ICO conservative and decoder-safe for Tauri/Windows resources.
    master.resize((32, 32), RESAMPLE).save(
        ICONS / "icon.ico",
        format="ICO",
        bitmap_format="bmp",
        sizes=[(32, 32)],
    )

    # Pillow writes a valid multi-resolution ICNS without requiring macOS iconutil.
    master.save(ICONS / "icon.icns", format="ICNS")
    print("wrote production app icon set ->", ICONS)


def write_tray(size=44, supersample=8):
    TRAY_DIR.mkdir(parents=True, exist_ok=True)
    for name, color in (
        ("tracking", GREEN),
        ("idle", AMBER),
        ("paused", RED),
    ):
        canvas = size * supersample
        image = Image.new("RGBA", (canvas, canvas), (0, 0, 0, 0))
        draw_tray_mark(ImageDraw.Draw(image), canvas, color)
        image.resize((size, size), RESAMPLE).save(
            TRAY_DIR / f"tray-{name}.png",
            optimize=True,
        )
    print("wrote state-aware tray set ->", TRAY_DIR)


def write_extension(master):
    EXT_DIR.mkdir(parents=True, exist_ok=True)
    for px in (16, 32, 48, 128):
        master.resize((px, px), RESAMPLE).save(
            EXT_DIR / f"{px}.png",
            optimize=True,
        )
    print("wrote extension icon set ->", EXT_DIR)


if __name__ == "__main__":
    master = brand_master()
    write_app_set(master)
    write_tray()
    write_extension(master)
