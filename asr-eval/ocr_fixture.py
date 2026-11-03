"""Render 30 public, synthetic desktop text fixtures; never captures the screen."""

import json
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

from survey_assets import ROOT, sha256

# Original text composed for this evaluation. Common UI labels and example code
# do not contain data from this computer or any user's application session.
TEXTS = {
    "ui-en": [
        "Settings",
        "General",
        "Appearance",
        "Language: English",
        "Notifications",
        "Download models",
        "Save changes",
        "Cancel",
    ],
    "ui-zh": [
        "设置",
        "通用",
        "外观",
        "语言：简体中文",
        "通知",
        "下载模型",
        "保存更改",
        "取消",
    ],
    "code-go": [
        "package main",
        'import "fmt"',
        "func main() {",
        "    count := 42",
        '    fmt.Printf("count=%d", count)',
        "    if count != 0 {",
        '        fmt.Println("ready")',
        "    }",
        "}",
    ],
    "code-ts": [
        "type Job = { id: string; ready: boolean };",
        "const jobs: Job[] = [];",
        "export function findJob(id: string) {",
        "    return jobs.find((job) => job.id === id);",
        "}",
        "const value = 1 + 2 * 3;",
        "// Read local files after the user chooses them.",
        "console.log(value >= 7 && jobs.length === 0);",
    ],
    "mixed-zh": [
        "项目概览 Project overview",
        "下一步：检查 API 返回值",
        "请在下午三点之前完成代码审查。",
        "版本 v2.0.1 已准备就绪。",
        "文件名：config.yaml",
        "运行测试 go test ./...",
        "本地处理，无需上传截图。",
        "继续 Continue",
    ],
    "ui-en-dense": [
        "Repository",
        "Pull requests",
        "Files changed",
        "Review requested",
        "All checks have passed",
        "Search files by name",
        "main > src > index.ts",
        "Updated 3 minutes ago",
        "Open in editor",
    ],
}


def generate():
    dest = ROOT / "survey-2026-10" / "ocr-fixtures"
    dest.mkdir(parents=True, exist_ok=True)
    manifest = []
    for category, lines in TEXTS.items():
        for variant, (size, dark, scale) in enumerate(
            [
                (16, False, 1),
                (20, False, 1),
                (24, True, 1),
                (28, False, 1),
                (16, True, 2),
            ]
        ):
            image = Image.new(
                "RGB", (1600 * scale, 900 * scale), "#202124" if dark else "#f4f5f7"
            )
            draw = ImageDraw.Draw(image)
            draw.rounded_rectangle(
                (40 * scale, 40 * scale, 1560 * scale, 860 * scale),
                radius=14 * scale,
                fill="#292b2f" if dark else "white",
                outline="#555555" if dark else "#d2d4d8",
                width=scale,
            )
            path = (
                "/System/Library/Fonts/Menlo.ttc"
                if category.startswith("code")
                else "/System/Library/Fonts/Hiragino Sans GB.ttc"
            )
            font = ImageFont.truetype(path, size * scale)
            for i, line in enumerate(lines):
                draw.text(
                    (80 * scale, (80 + i * (size + 22)) * scale),
                    line,
                    font=font,
                    fill="#e6e7ea" if dark else "#242629",
                )
            name = f"{category}-{variant}.jpg"
            image.save(dest / name, quality=90)
            manifest.append(
                {
                    "id": name[:-4],
                    "path": name,
                    "category": category,
                    "font_size": size,
                    "dark": dark,
                    "scale": scale,
                    "width": image.width,
                    "height": image.height,
                    "reference": "\n".join(lines),
                    "sha256": sha256(dest / name),
                }
            )
    (dest / "manifest.json").write_text(
        json.dumps(
            {
                "provenance": "Original public test fixtures; common UI labels, Go/TypeScript examples and Chinese text. No live screenshots.",
                "licence": "CC0-1.0",
                "samples": manifest,
            },
            ensure_ascii=False,
            indent=2,
        )
        + "\n"
    )


if __name__ == "__main__":
    generate()
