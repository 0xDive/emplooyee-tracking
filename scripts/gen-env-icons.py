#!/usr/bin/env python3
"""Generate environment-badged ActiLens app icons.

DEV and STG builds use the same canonical ActiLens app icon as production, with
an explicit corner ribbon so installed environments remain distinguishable in
the dock, app switcher, Start menu, and Finder.

Run:
    python3 scripts/gen-env-icons.py

Requires Pillow only.
"""

from pathlib import Path
from PIL import Image, ImageDraw, ImageFont

ROOT = Path(__file__).resolve().parents[1]
ICONS = ROOT / "apps" / "desktop" / "src-tauri" / "icons"
SOURCE = ROOT / "assets" / "brand" / "png" / "app-icon-256.png"
RESAMPLE = Image.Resampling.LANCZOS

VARIANTS = [
    ("dev", "DEV", (224, 110, 0, 255)),
    ("staging", "STG", (126, 67, 213, 255)),
]

PNG_SIZES = {
    "32x32.png": 32,
    "128x128.png": 128,
    "128x128@2x.png": 256,
}


def font(px):
    candidates = (
        "/System/Library/Fonts/Supplemental/Arial Bold.ttf",
        "/System/Library/Fonts/Helvetica.ttc",
        "/Library/Fonts/Arial Bold.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
        "/usr/share/fonts/truetype/liberation2/LiberationSans-Bold.ttf",
        "C:/Windows/Fonts/arialbd.ttf",
    )
    for path in candidates:
        candidate = Path(path)
        if candidate.exists():
            try:
                return ImageFont.truetype(str(candidate), px)
            except OSError:
                pass
    return ImageFont.load_default()


def make_master(label, color, size=1024):
    if not SOURCE.exists():
        raise FileNotFoundError(f"Missing canonical brand raster: {SOURCE}")

    base = Image.open(SOURCE).convert("RGBA").resize((size, size), RESAMPLE)
    band_width = int(size * 0.90)
    band_height = int(size * 0.19)
    band = Image.new("RGBA", (band_width, band_height), (0, 0, 0, 0))
    draw = ImageDraw.Draw(band)
    radius = max(8, size // 40)
    draw.rounded_rectangle(
        [0, 0, band_width - 1, band_height - 1],
        radius=radius,
        fill=color,
        outline=(255, 255, 255, 230),
        width=max(2, size // 205),
    )

    face = font(int(band_height * 0.54))
    bounds = draw.textbbox((0, 0), label, font=face)
    draw.text(
        (
            (band_width - (bounds[2] - bounds[0])) / 2,
            (band_height - (bounds[3] - bounds[1])) / 2 - bounds[1],
        ),
        label,
        font=face,
        fill=(255, 255, 255, 255),
    )

    band = band.rotate(-45, expand=True, resample=Image.Resampling.BICUBIC)
    x = size - band.width + int(size * 0.29)
    y = size - band.height + int(size * 0.29)
    base.alpha_composite(band, (x, y))
    return base


def write_set(folder, master):
    out = ICONS / folder
    out.mkdir(parents=True, exist_ok=True)

    for name, px in PNG_SIZES.items():
        master.resize((px, px), RESAMPLE).save(out / name, optimize=True)

    # Same strict-decoder-safe strategy as production.
    master.resize((32, 32), RESAMPLE).save(
        out / "icon.ico",
        format="ICO",
        bitmap_format="bmp",
        sizes=[(32, 32)],
    )
    master.save(out / "icon.icns", format="ICNS")
    print(f"wrote {folder} icon set -> {out}")


if __name__ == "__main__":
    for folder, label, color in VARIANTS:
        write_set(folder, make_master(label, color))
