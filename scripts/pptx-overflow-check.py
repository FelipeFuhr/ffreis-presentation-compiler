#!/usr/bin/env python3
"""Render a .pptx through a real reader and assert no text collides.

This exists because of a defect no other check in this repo could have seen.
A native slide's text box is sized from the element a browser laid out, but
the reader re-breaks the lines inside it with its own metrics -- and with its
own font, if the named face is absent. A cover title that CSS fits on one line
took two, grew past its box, and drew on top of the subtitle beneath it.

Nothing upstream catches that. `verify.go` checks the package, not the
rendering. `check` measures the DOM, which is the layout that was *correct*.
The only way to see it is to render the .pptx and look at where the glyphs
landed -- so that is what this does: convert with LibreOffice, read the word
boxes out of the PDF, and compare the vertical extents of consecutive lines.

What it is not: PowerPoint. LibreOffice's wrapping is its own, so this finds
*a* reader's overlap rather than *the* reader's. That is still far more than
guessing, and when the first native cover was measured this way the overlap it
reported was 25.8 points -- the same defect a human had just spotted by eye.
"""

from __future__ import annotations

import argparse
import os
import re
import shutil
import subprocess
import sys
import tempfile

# Two lines are considered to collide when the upper one's bottom reaches more
# than this far past the lower one's top. Not zero: PDF word boxes include
# font ascent and descent, so adjacent lines of tight leading routinely share
# a fraction of a point without a reader drawing anything on top of anything.
OVERLAP_TOLERANCE_PT = 1.0

# Words whose vertical origins differ by less than this belong to one line.
LINE_BAND_PT = 4.0

WORD = re.compile(
    r'<word xMin="([\d.]+)" yMin="([\d.]+)" xMax="([\d.]+)" yMax="([\d.]+)">([^<]*)</word>'
)
PAGE = re.compile(r"<page width=\"[\d.]+\" height=\"[\d.]+\">(.*?)</page>", re.S)


def need(tool: str) -> str:
    """Resolve a required tool, failing loudly rather than skipping.

    A skip that reads as a pass is how a gate stops gating, which this repo
    has been bitten by: `export-selftest` exits 0 without LibreOffice and so
    said nothing for the whole life of three packaging defects.
    """
    path = shutil.which(tool)
    if not path:
        sys.exit(f"pptx-overflow-check: {tool} is not installed; this check cannot run")
    return path


def to_pdf(pptx: str, workdir: str) -> str:
    subprocess.run(
        [need("soffice"), "--headless", "--norestore",
         f"-env:UserInstallation=file://{workdir}/profile",
         "--convert-to", "pdf", "--outdir", workdir, pptx],
        check=False, capture_output=True, timeout=900,
    )
    produced = [f for f in os.listdir(workdir) if f.endswith(".pdf")]
    if not produced:
        sys.exit(f"pptx-overflow-check: the reader could not open {pptx}")
    return os.path.join(workdir, produced[0])


def lines_per_page(pdf: str) -> list[list[dict]]:
    """Group each page's words into lines, in reading order."""
    xml = subprocess.run([need("pdftotext"), "-bbox", pdf, "-"],
                         check=True, capture_output=True, text=True).stdout
    pages = []
    for page in PAGE.findall(xml):
        words = [{"y0": float(m[1]), "y1": float(m[3]), "t": m[4]} for m in WORD.findall(page)]
        lines: list[dict] = []
        for w in sorted(words, key=lambda w: (w["y0"],)):
            if lines and abs(lines[-1]["y0"] - w["y0"]) < LINE_BAND_PT:
                lines[-1]["y1"] = max(lines[-1]["y1"], w["y1"])
                lines[-1]["words"].append(w["t"])
            else:
                lines.append({"y0": w["y0"], "y1": w["y1"], "words": [w["t"]]})
        pages.append(lines)
    return pages


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__,
                                 formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("pptx")
    ap.add_argument("--tolerance", type=float, default=OVERLAP_TOLERANCE_PT,
                    help="points of vertical interpenetration to allow (default %(default)s)")
    args = ap.parse_args()

    if not os.path.isfile(args.pptx):
        sys.exit(f"pptx-overflow-check: no such file: {args.pptx}")

    with tempfile.TemporaryDirectory() as work:
        pages = lines_per_page(to_pdf(args.pptx, work))

    findings = 0
    for n, lines in enumerate(pages, start=1):
        for upper, lower in zip(lines, lines[1:]):
            by = upper["y1"] - lower["y0"]
            if by > args.tolerance:
                findings += 1
                print(f"slide {n}: text overlaps by {by:.1f}pt")
                print(f"    {' '.join(upper['words'])[:70]!r}")
                print(f"  over {' '.join(lower['words'])[:70]!r}")

    if findings:
        print(f"\npptx-overflow-check: {findings} overlap(s) in {len(pages)} slides")
        print("A text box is drawing outside itself. Native text boxes should declare")
        print("normAutofit so the reader shrinks the text instead; see shapes.go.")
        return 1

    print(f"pptx-overflow-check: ok — no text overlaps across {len(pages)} slides")
    return 0


if __name__ == "__main__":
    sys.exit(main())
