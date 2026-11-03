"""Pairs each setup v2 state with the design prototype (or its still) for
docs/setup-v2-qa/: design on the left, the app on the right, and the share
of pixels that differ noticeably. Run after
`pnpm exec playwright test -c playwright.setup-v2.config.ts`."""

import sys
from pathlib import Path

from PIL import Image, ImageChops, ImageDraw

REPO = Path(__file__).resolve().parents[3]
QA = REPO / "docs/setup-v2-qa"
RAW = QA / "raw"
STILLS = REPO / "docs/handoff-2026-10-28/setup-2.1"
ORDER = [
    ("00", "on-this-mac"),
    ("01", "notices"),
    ("02", "ai-empty"),
    ("02", "ai-picked"),
    ("02", "ai-one"),
    ("02", "ai-none"),
    ("03", "sees"),
    ("03", "sees-on"),
    ("04", "runs"),
    ("04", "runs-off"),
    ("04", "runs-downloading"),
    ("04", "send"),
    ("05", "reaches"),
    ("05", "reaches-paired"),
    ("06", "finish-tile"),
]
W, H = 1200, 1120


def design(num, state, theme):
    proto = RAW / f"proto-{state}-{theme}.png"
    if proto.exists():
        return Image.open(proto).convert("RGB").resize((W, H)), "prototype"
    # The design stills frame the 600 x 560 window at (150, 58) on a 900 x 640 stage, @2x.
    still = STILLS / f"handoff-2026-10-28-1315-setup-v2.1.0-{num}-{state}-{theme}.png"
    img = Image.open(still).convert("RGB")
    sx = img.width / 900
    box = (round(150 * sx), round(58 * sx), round(750 * sx), round(618 * sx))
    return img.crop(box).resize((W, H)), "still"


def differ(a, b):
    diff = (
        ImageChops.difference(a, b).convert("L").point(lambda v: 255 if v > 40 else 0)
    )
    return sum(1 for v in diff.getdata() if v) / (W * H)


rows = []
for num, state in ORDER:
    for theme in ("light", "dark"):
        app_path = RAW / f"app-{state}-{theme}.png"
        if not app_path.exists():
            print(f"missing {app_path}", file=sys.stderr)
            continue
        app = Image.open(app_path).convert("RGB").resize((W, H))
        ref, source = design(num, state, theme)
        sheet = Image.new("RGB", (W * 2 + 24, H + 56), (128, 128, 128))
        sheet.paste(ref, (0, 56))
        sheet.paste(app, (W + 24, 56))
        draw = ImageDraw.Draw(sheet)
        draw.text((12, 18), f"design ({source})", fill=(255, 255, 255))
        draw.text((W + 36, 18), "app (WebKit)", fill=(255, 255, 255))
        name = f"{num}-{state}-{theme}.png"
        sheet.save(QA / name, optimize=True)
        rows.append((state, theme, source, differ(ref, app), name))

with open(QA / "diff.tsv", "w") as f:
    for state, theme, source, share, name in rows:
        f.write(f"{state}\t{theme}\t{source}\t{share:.4f}\t{name}\n")
        print(f"{state:15} {theme:5} {source:9} {share:6.2%}")
