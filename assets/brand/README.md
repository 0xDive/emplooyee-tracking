# ActiLens brand assets

This directory is the source of truth for the ActiLens product identity.

## Vector masters

- `svg/mark.svg` — primary standalone mark.
- `svg/mark-white.svg` — inverse single-color mark.
- `svg/mark-mono.svg` — dark single-color mark.
- `svg/lockup.svg` — horizontal mark + wordmark for light surfaces.
- `svg/lockup-inverse.svg` — horizontal white lockup.
- `svg/app-icon.svg` — rounded-square application icon and raster export source.

## Raster and platform exports

- `png/app-icon-{16,32,48,128,180,256}.png` — standard web, extension, desktop, and Apple Touch sizes.
- `platform/favicon.ico` — strict-decoder-safe Windows/web ICO export.
- `platform/icon.icns` — macOS ICNS export.

Runtime copies live next to the applications that consume them so builds do not
depend on cross-workspace asset traversal. The source-of-truth assets stay here;
app-local copies are deployment artifacts.

## Regenerating product icons

`scripts/gen-icons.py` regenerates desktop, Windows, tray, and browser-extension
icons from the canonical raster seed in this directory. Then
`scripts/gen-env-icons.py` derives clearly badged DEV and STG variants from the
same brand. Both scripts require Pillow only.

Windows ICO files deliberately use a BMP-backed 32px frame. This is a
compatibility contract with Tauri's strict icon decoder and prevents embedded
PNG CRC issues from breaking Rust compilation or installer builds.

## Core palette

| Token | Value |
| --- | --- |
| Cyan | `#39C6FF` |
| Blue | `#1769FF` |
| Indigo | `#3C32EE` |
| Violet | `#7B2CF2` |
| Highlight violet | `#B45CFF` |
| Wordmark navy | `#0B1D51` |

The mark represents **Acti + Lens**: the folded A communicates activity/motion;
the circular focus point and curved fold communicate lens, observation, and focus.

Keep the aspect ratio and clear space. Do not redraw, rotate, add glows/shadows,
or change individual gradient stops in product code. At small sizes prefer the
square app icon. Raster installer/browser exports must be derived from the brand
masters here, never from screenshots or concept art.
