#!/usr/bin/env python3
"""Regenerate the raster app icons from the SVG masters in artwork/.

artwork/icon-gray.svg (grey wax seal) and artwork/icon-red.svg (red wax seal) are
the design sources. The small PNGs they render into are committed next to the Go
sources in internal/ui/assets because the runtime does not parse SVG: see
internal/ui/appicon.go. Only the small variants are embedded in the binary;
artwork/icon-red-512.png ships as the desktop icon.

The masters keep their 174x118 viewBox, which suits the design but not the
output: every PNG has to be square. Rendering such a document by width alone
gives a 174:118 pixmap, and handing librsvg both a width and a height stretches
the artwork. So each render rewrites the root tag instead: the width/height
attributes go away, and the viewBox grows into the square around the artwork.
The envelope already touches the box on all four sides, so extending the short
side only adds the padding a square cell needs, and nothing is cropped.

The two masters carry identical geometry and differ only in the seal fill, so
the notification area can flip between them without anything moving. The
title-bar brand mark takes the red master, because the red seal is what reads
at 36dp.

Usage:
    python3 scripts/gen_app_icons.py

Requires rsvg-convert (Debian package librsvg2-bin).
"""

import argparse
import re
import subprocess
import tempfile
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
ASSETS = REPO_ROOT / "internal" / "ui" / "assets"
ARTWORK = REPO_ROOT / "artwork"

# The desktop icon is not embedded in the binary, so it is written next to the
# masters in artwork/ instead of next to the small variants beside the Go code.
DESKTOP_PNGS = {"icon-red-512.png"}

# master SVG -> rendered PNG name -> edge length in pixels.
#
# The title-bar mark picks the smallest of the embedded sizes that still covers
# the device pixels, so a 1x window never stretches a 128px bitmap: Gio only has
# linear filtering, and shrinking by more than a few times looks muddy. The
# sizes are the ones 36dp hits at whole and fractional scales (36/45/54/72/108
# for 1x/1.25x/1.5x/2x/3x), which is why the mark is 36dp: the scale factor is
# exactly 1 and no interpolation happens. The 40/48/64/96/128 variants from the
# previous 32dp mark stay around: they still cover odd scale factors such as
# 1.1x (40px) or 1.75x (64px).
TARGETS = {
    "icon-gray.svg": {
        "icon-gray-44.png": 44,  # notification area, idle state
    },
    "icon-red.svg": {
        "icon-red-44.png": 44,  # notification area, unread state
        "icon-red-512.png": 512,  # desktop integration, not embedded
        "icon-red-32.png": 32,  # title-bar mark, embedded, chosen per device scale
        "icon-red-36.png": 36,  # 36dp at 1x
        "icon-red-40.png": 40,
        "icon-red-45.png": 45,  # 36dp at 1.25x
        "icon-red-54.png": 54,  # 36dp at 1.5x
        "icon-red-72.png": 72,  # 36dp at 2x
        "icon-red-48.png": 48,
        "icon-red-64.png": 64,
        "icon-red-96.png": 96,
        "icon-red-108.png": 108,  # 36dp at 3x
        "icon-red-128.png": 128,
    },
}

SVG_TAG_RE = re.compile(r"<svg\b[^>]*>", re.S)

VIEWBOX_RE = re.compile(r'viewBox="([^"]*)"')

ROOT_SIZE_RE = re.compile(r'\s(?:width|height)="[^"]*"')


def square_document(svg: str, name: str) -> str:
    """Return the document with a square viewBox and no root width/height.

    Both changes are what makes a width-only render square and unstretched: the
    attributes pin the document's pixel size, so the artwork would only be
    letterboxed inside it at the viewBox's own ratio.
    """
    root = SVG_TAG_RE.search(svg)
    if not root:
        raise SystemExit(f"{name}: no <svg> root tag")
    tag = ROOT_SIZE_RE.sub("", root.group(0))
    box = VIEWBOX_RE.search(tag)
    if not box:
        raise SystemExit(f"{name}: <svg> has no viewBox")
    try:
        vx, vy, vw, vh = (float(v) for v in box.group(1).replace(",", " ").split())
    except ValueError:
        raise SystemExit(f"{name}: cannot parse viewBox {box.group(1)!r}")
    side = max(vw, vh)
    cx, cy = vx + vw / 2, vy + vh / 2
    tag = VIEWBOX_RE.sub(
        f'viewBox="{cx - side / 2:g} {cy - side / 2:g} {side:g} {side:g}"', tag, count=1
    )
    return svg[: root.start()] + tag + svg[root.end() :]


def render(svg: str, name: str, width: int) -> bytes:
    """Rasterise SVG source text with the host's librsvg at the given width."""
    with tempfile.TemporaryDirectory() as tmp:
        path = Path(tmp) / name
        path.write_text(svg, encoding="utf-8")
        cmd = ["rsvg-convert", "-w", str(width), "-f", "png", str(path)]
        try:
            out = subprocess.run(cmd, check=True, capture_output=True)
        except FileNotFoundError:
            raise SystemExit("rsvg-convert not found; install librsvg2-bin")
        except subprocess.CalledProcessError as err:
            raise SystemExit(f"rsvg-convert failed for {name}:\n{err.stderr.decode()}")
    return out.stdout


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.parse_args()

    for master, variants in TARGETS.items():
        path = ARTWORK / master
        if not path.exists():
            raise SystemExit(f"missing icon master {path}")
        svg = square_document(path.read_text(encoding="utf-8"), master)
        for name, size in variants.items():
            out = (ARTWORK if name in DESKTOP_PNGS else ASSETS) / name
            out.write_bytes(render(svg, name, size))
            print(f"wrote {out.relative_to(REPO_ROOT)} ({size}x{size})")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
