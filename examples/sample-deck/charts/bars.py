#!/usr/bin/env python3
"""Bar chart generator honouring the compiler's generator contract.

    python3 bars.py --out <file.svg> --tokens <json> [--data <file.csv>]

Reads `label,value` rows and emits an SVG coloured with the deck's own design
tokens, so the chart cannot drift from the theme around it. Deliberately
dependency-free: `make smoke-check` must work on a bare CI image. A deck that
wants matplotlib can use it instead — the contract is the same.
"""
import argparse
import csv
import json
import sys
from xml.sax.saxutils import escape

WIDTH, HEIGHT = 640, 380
PAD_LEFT, PAD_RIGHT, PAD_TOP, PAD_BOTTOM = 56, 16, 28, 48


def fmt(value):
    """Render a value without inventing or hiding precision: 12400 -> 12,400
    and 54.5 -> 54.5, never 54."""
    text = f"{value:,.1f}"
    return text[:-2] if text.endswith(".0") else text


def read_rows(path):
    if not path:
        return []
    with open(path, newline="", encoding="utf-8") as fh:
        rows = []
        for row in csv.reader(fh):
            if not row or row[0].lstrip().startswith("#"):
                continue
            if len(row) < 2:
                sys.exit(f"bars.py: expected 'label,value', got {row!r}")
            try:
                rows.append((row[0].strip(), float(row[1])))
            except ValueError:
                sys.exit(f"bars.py: {row[1]!r} is not a number")
        return rows


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", required=True)
    ap.add_argument("--tokens", default="{}")
    ap.add_argument("--data")
    ap.add_argument("--title", default="")
    args = ap.parse_args()

    tokens = json.loads(args.tokens)
    accent = tokens.get("accent", "#2563eb")
    ink = tokens.get("ink-strong", tokens.get("ink", "#1f2d39"))
    muted = tokens.get("muted", "#667269")
    hairline = tokens.get("hairline", "rgba(24,29,26,0.08)")
    font = tokens.get("font-body", "system-ui, sans-serif")

    rows = read_rows(args.data)
    if not rows:
        sys.exit("bars.py: no data rows; pass --data with 'label,value' lines")

    top = max(v for _, v in rows)
    plot_w = WIDTH - PAD_LEFT - PAD_RIGHT
    plot_h = HEIGHT - PAD_TOP - PAD_BOTTOM
    slot = plot_w / len(rows)
    bar_w = slot * 0.56

    out = [
        f'<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 {WIDTH} {HEIGHT}" '
        f'role="img" aria-label="{escape(args.title or "Bar chart")}">',
        f'<style>text{{font-family:{escape(font)}}}</style>',
    ]

    # horizontal guides, drawn behind the bars
    for i in range(5):
        y = PAD_TOP + plot_h * i / 4
        value = top * (4 - i) / 4
        out.append(f'<line x1="{PAD_LEFT}" y1="{y:.1f}" x2="{WIDTH - PAD_RIGHT}" y2="{y:.1f}" '
                   f'stroke="{hairline}" stroke-width="1"/>')
        out.append(f'<text x="{PAD_LEFT - 10}" y="{y + 4:.1f}" text-anchor="end" '
                   f'font-size="12" fill="{muted}">{fmt(value)}</text>')

    for i, (label, value) in enumerate(rows):
        h = plot_h * (value / top)
        x = PAD_LEFT + slot * i + (slot - bar_w) / 2
        y = PAD_TOP + plot_h - h
        out.append(f'<rect x="{x:.1f}" y="{y:.1f}" width="{bar_w:.1f}" height="{h:.1f}" '
                   f'rx="3" fill="{accent}"/>')
        out.append(f'<text x="{x + bar_w / 2:.1f}" y="{y - 8:.1f}" text-anchor="middle" '
                   f'font-size="12.5" font-weight="700" fill="{ink}">{fmt(value)}</text>')
        out.append(f'<text x="{x + bar_w / 2:.1f}" y="{HEIGHT - PAD_BOTTOM + 20:.1f}" '
                   f'text-anchor="middle" font-size="12.5" fill="{muted}">{escape(label)}</text>')

    out.append("</svg>")
    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write("\n".join(out))


if __name__ == "__main__":
    main()
