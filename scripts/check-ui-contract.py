#!/usr/bin/env python3
from __future__ import annotations

import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
LOCALES = ROOT / "apps" / "web-admin" / "src" / "i18n" / "locales"
THEME = ROOT / "apps" / "web-admin" / "src" / "theme"

PLURAL_SUFFIX = re.compile(r"_(zero|one|two|few|many|other)$")
TOKEN_DEF = re.compile(r"^\s*(--ds-[\w-]+)\s*:", re.MULTILINE)
TOKEN_USE = re.compile(r"var\((--ds-[\w-]+)")
TOKEN_VALUE = re.compile(r"^\s*(--ds-[\w-]+)\s*:\s*([^;]+);", re.MULTILINE)


def flatten(value: object, prefix: str = "") -> set[str]:
    out: set[str] = set()
    if isinstance(value, dict):
        for key, child in value.items():
            path = f"{prefix}.{key}" if prefix else key
            out.update(flatten(child, path))
        return out
    return {prefix}


def normalized_keys(path: Path) -> set[str]:
    data = json.loads(path.read_text(encoding="utf-8"))
    return {PLURAL_SUFFIX.sub("", key) for key in flatten(data)}


def check_locale_parity() -> list[str]:
    errors: list[str] = []
    en_files = {p.name for p in (LOCALES / "en").glob("*.json")}
    ru_files = {p.name for p in (LOCALES / "ru").glob("*.json")}
    if en_files != ru_files:
        errors.append(
            "EN/RU locale files differ: "
            f"en_only={sorted(en_files - ru_files)} "
            f"ru_only={sorted(ru_files - en_files)}"
        )
    for name in sorted(en_files & ru_files):
        en = normalized_keys(LOCALES / "en" / name)
        ru = normalized_keys(LOCALES / "ru" / name)
        if en != ru:
            errors.append(
                f"{name}: locale key mismatch; "
                f"en_only={sorted(en - ru)} ru_only={sorted(ru - en)}"
            )
    return errors


def block(source: str, pattern: str, label: str) -> str:
    match = re.search(pattern, source, re.DOTALL)
    if not match:
        raise SystemExit(f"Could not locate {label} theme token block")
    return match.group(1)


def parse_hex(value: str) -> tuple[float, float, float] | None:
    value = value.strip()
    match = re.fullmatch(r"#([0-9a-fA-F]{6})", value)
    if not match:
        return None
    raw = match.group(1)
    return tuple(int(raw[index:index + 2], 16) / 255 for index in (0, 2, 4))


def relative_luminance(color: tuple[float, float, float]) -> float:
    def channel(value: float) -> float:
        return value / 12.92 if value <= 0.04045 else ((value + 0.055) / 1.055) ** 2.4

    red, green, blue = (channel(value) for value in color)
    return 0.2126 * red + 0.7152 * green + 0.0722 * blue


def contrast_ratio(foreground: str, background: str) -> float | None:
    fg = parse_hex(foreground)
    bg = parse_hex(background)
    if fg is None or bg is None:
        return None
    lighter, darker = sorted(
        (relative_luminance(fg), relative_luminance(bg)),
        reverse=True,
    )
    return (lighter + 0.05) / (darker + 0.05)


def check_theme_contract() -> list[str]:
    errors: list[str] = []
    foundation = (THEME / "foundation.css").read_text(encoding="utf-8")
    light = block(
        foundation,
        r':root,\s*:root\[data-theme="light"\]\s*\{(.*?)\n\}',
        "light",
    )
    dark = block(
        foundation,
        r':root\[data-theme="dark"\]\s*\{(.*?)\n\}',
        "dark",
    )

    light_tokens = set(TOKEN_DEF.findall(light))
    dark_tokens = set(TOKEN_DEF.findall(dark))
    if light_tokens != dark_tokens:
        errors.append(
            "Light/dark semantic token sets differ: "
            f"light_only={sorted(light_tokens - dark_tokens)} "
            f"dark_only={sorted(dark_tokens - light_tokens)}"
        )

    light_values = dict(TOKEN_VALUE.findall(light))
    contrast_checks = {
        "--ds-text-primary": ("--ds-bg-surface", 7.0),
        "--ds-text-secondary": ("--ds-bg-surface", 4.5),
        "--ds-text-tertiary": ("--ds-bg-surface", 4.5),
        "--ds-brand": ("--ds-bg-surface", 4.5),
        "--ds-success": ("--ds-success-soft", 4.5),
        "--ds-warning": ("--ds-warning-soft", 4.5),
        "--ds-danger": ("--ds-danger-soft", 4.5),
        "--ds-info": ("--ds-info-soft", 4.5),
    }
    for foreground, (background, minimum) in contrast_checks.items():
        ratio = contrast_ratio(
            light_values.get(foreground, ""),
            light_values.get(background, ""),
        )
        if ratio is None:
            errors.append(
                f"Could not evaluate light-theme contrast for {foreground} on {background}"
            )
        elif ratio < minimum:
            errors.append(
                f"Light-theme contrast is too low for {foreground} on {background}: "
                f"{ratio:.2f}:1 < {minimum:.1f}:1"
            )

    defined = set(TOKEN_DEF.findall(foundation))
    used: set[str] = set()
    for css_path in THEME.glob("*.css"):
        used.update(TOKEN_USE.findall(css_path.read_text(encoding="utf-8")))
    missing = used - defined
    if missing:
        errors.append(f"Undefined design-system tokens used: {sorted(missing)}")
    return errors


def main() -> None:
    errors = check_locale_parity() + check_theme_contract()
    if errors:
        raise SystemExit("UI acceptance contract failed:\n- " + "\n- ".join(errors))
    print("UI acceptance contract: RU/EN parity, theme tokens and light-theme contrast OK")


if __name__ == "__main__":
    main()
