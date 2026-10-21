#!/usr/bin/env python3
"""Regenerate the Material Symbols subset fonts in internal/ui/assets/.

The icon set is a subset of Material Symbols Rounded (Apache-2.0). Only the
glyphs referenced in internal/ui/icons.go are kept, which brings the shipped
font down to a few kilobytes. The FILL=1 instance only needs U+F09A (the solid
star), which shares its codepoint with the outlined glyph and differs solely
in the FILL axis.

Usage:
    python3 scripts/gen_icon_fonts.py [--font PATH] [--check]

Requires Python 3 with fonttools (pip install fonttools). The upstream
variable font is taken from --font or downloaded from the Google
material-design-icons repository.

icons_test.go fails when a generated font is missing a glyph, so adding an
icon in icons.go only requires re-running this script.
"""

import argparse
import io
import re
import sys
import urllib.request
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
ICONS_GO = REPO_ROOT / "internal" / "ui" / "icons.go"
ASSETS = REPO_ROOT / "internal" / "ui" / "assets"

UPSTREAM_URL = (
    "https://github.com/google/material-design-icons/raw/master/"
    "variablefont/MaterialSymbolsRounded%5BFILL%2CGRAD%2Copsz%2Cwght%5D.ttf"
)

OUTLINE_TTF = ASSETS / "material-symbols-rounded.ttf"
FILL_TTF = ASSETS / "material-symbols-rounded-fill.ttf"

# The solid star is the only glyph that needs the FILL=1 instance; see icons.go.
FILL_CODEPOINT = 0xF09A

# The instances the UI draws with (see icons.go for the provenance).
AXES = {"FILL": 0, "GRAD": 0, "opsz": 24, "wght": 400}
FILL_AXES = dict(AXES, FILL=1)

# Fixed timestamp so repeated runs produce byte-identical output.
FIXED_TIMESTAMP = 0

CODEPOINT_RE = re.compile(r"iconGlyph\s*=\s*0x([0-9a-fA-F]+)")


def parse_codepoints() -> list[int]:
    text = ICONS_GO.read_text(encoding="utf-8")
    cps = sorted({int(m, 16) for m in CODEPOINT_RE.findall(text)})
    if not cps:
        raise SystemExit(f"no icon codepoints found in {ICONS_GO}")
    if FILL_CODEPOINT not in cps:
        raise SystemExit(
            f"{ICONS_GO} no longer references U+{FILL_CODEPOINT:04X}; "
            "update FILL_CODEPOINT in this script"
        )
    return cps


def load_upstream(path: Path | None) -> bytes:
    if path is not None:
        return path.read_bytes()
    print(f"downloading upstream variable font from {UPSTREAM_URL} ...")
    with urllib.request.urlopen(UPSTREAM_URL) as resp:
        return resp.read()


def instantiate(data: bytes, axes: dict[str, float], unicodes: list[int]) -> bytes:
    from fontTools import ttLib
    from fontTools.subset import Options, Subsetter
    from fontTools.varLib import instancer

    # recalcTimestamp=False keeps head.modified fixed so repeated runs are
    # byte-identical; the instancer would otherwise bump it on every save.
    font = ttLib.TTFont(io.BytesIO(data), recalcTimestamp=False)
    instancer.instantiateVariableFont(font, axes, inplace=True)

    options = Options()
    options.layout_features = ["*"]  # keep ligature/feature tables intact
    options.name_IDs = ["*"]
    options.notdef_outline = True
    options.recalc_bounds = True
    subsetter = Subsetter(options=options)
    subsetter.populate(unicodes=unicodes)
    subsetter.subset(font)

    font["head"].modified = FIXED_TIMESTAMP
    buf = io.BytesIO()
    font.save(buf)
    return buf.getvalue()


def check_glyphs(data: bytes, unicodes: list[int]) -> None:
    from fontTools import ttLib

    cmap = ttLib.TTFont(io.BytesIO(data)).getBestCmap()
    missing = [cp for cp in unicodes if cp not in cmap]
    if missing:
        raise SystemExit(
            "generated font is missing codepoints: "
            + ", ".join(f"U+{cp:04X}" for cp in missing)
        )


def build(data: bytes, unicodes: list[int]) -> bytes:
    out = instantiate(data, AXES, unicodes)
    check_glyphs(out, unicodes)
    return out


def build_fill(data: bytes, unicodes: list[int]) -> bytes:
    out = instantiate(data, FILL_AXES, [FILL_CODEPOINT])
    check_glyphs(out, [FILL_CODEPOINT])
    return out


def write_atomic(path: Path, data: bytes) -> None:
    tmp = path.with_suffix(path.suffix + ".tmp")
    tmp.write_bytes(data)
    tmp.replace(path)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--font",
        type=Path,
        default=None,
        help="path to the upstream Material Symbols Rounded variable font "
        "(downloaded when omitted)",
    )
    parser.add_argument(
        "--check",
        action="store_true",
        help="verify the existing fonts match a fresh generation; exit 1 on drift",
    )
    args = parser.parse_args()

    unicodes = parse_codepoints()
    upstream = load_upstream(args.font)

    outline = build(upstream, unicodes)
    fill = build_fill(upstream, unicodes)

    if args.check:
        drift = []
        for path, want in ((OUTLINE_TTF, outline), (FILL_TTF, fill)):
            if not path.exists():
                drift.append(f"{path}: missing")
            elif path.read_bytes() != want:
                drift.append(f"{path}: out of date")
        if drift:
            for d in drift:
                print(d, file=sys.stderr)
            return 1
        print("icon fonts are up to date")
        return 0

    write_atomic(OUTLINE_TTF, outline)
    write_atomic(FILL_TTF, fill)
    print(
        f"wrote {OUTLINE_TTF.relative_to(REPO_ROOT)} "
        f"({len(outline)} bytes, {len(unicodes)} glyphs)"
    )
    print(
        f"wrote {FILL_TTF.relative_to(REPO_ROOT)} "
        f"({len(fill)} bytes, FILL=1 U+{FILL_CODEPOINT:04X})"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
