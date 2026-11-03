"""Generated public multilingual UI screenshots against the native Vision API."""

import argparse
import json
import subprocess
import statistics
from PIL import Image, ImageDraw, ImageFont
from multilingual_eval import ROOT, OUT, CACHE, model_run, save, sha256
from survey_metrics import ocr_score

TEXTS = {
    "ja": [
        "設定とプライバシー",
        "言語：日本語",
        "新しいプロジェクトを作成",
        "会議は午後三時に始まります。",
        "変更を保存して続行してください。",
        "東京、大阪、京都の天気予報",
        "本日のお知らせはありません。",
        "バージョン 2.3.1 / API 2026",
    ],
    "ko": [
        "설정 및 개인정보 보호",
        "언어: 한국어",
        "새 프로젝트 만들기",
        "회의는 오후 세 시에 시작합니다.",
        "변경 사항을 저장하고 계속하세요.",
        "서울과 부산의 일기 예보",
        "오늘 새로운 알림이 없습니다.",
        "버전 2.3.1 / API 2026",
    ],
    "es": [
        "Configuración y privacidad",
        "Idioma: Español",
        "Crear un nuevo proyecto",
        "La reunión empieza a las tres de la tarde.",
        "Guarda los cambios para continuar.",
        "Información pública: mañana, acción y pingüino.",
        "No hay notificaciones nuevas.",
        "Versión 2.3.1 / API 2026",
    ],
    "fr": [
        "Paramètres et confidentialité",
        "Langue : Français",
        "Créer un nouveau projet",
        "La réunion commence à quinze heures.",
        "Enregistrez les modifications pour continuer.",
        "Texte public : été, cœur, façade et Noël.",
        "Aucune nouvelle notification.",
        "Version 2.3.1 / API 2026",
    ],
    "de": [
        "Einstellungen und Datenschutz",
        "Sprache: Deutsch",
        "Ein neues Projekt erstellen",
        "Die Besprechung beginnt um fünfzehn Uhr.",
        "Änderungen speichern und fortfahren.",
        "Öffentlicher Text: Größe, Straße und Grüße.",
        "Keine neuen Benachrichtigungen.",
        "Version 2.3.1 / API 2026",
    ],
}
LOCALES = {"ja": "ja-JP", "ko": "ko-KR", "es": "es-ES", "fr": "fr-FR", "de": "de-DE"}


def run():
    fixtures = OUT / "ocr-fixtures"
    fixtures.mkdir(exist_ok=True)
    samples = []
    for language, lines in TEXTS.items():
        font_path = {
            "ja": "/System/Library/Fonts/ヒラギノ角ゴシック W3.ttc",
            "ko": "/System/Library/Fonts/AppleSDGothicNeo.ttc",
        }.get(language, "/System/Library/Fonts/Helvetica.ttc")
        for i, (size, dark, scale) in enumerate(
            [
                (14, False, 1),
                (16, True, 1),
                (20, False, 1),
                (24, True, 1),
                (16, False, 2),
                (14, True, 2),
            ]
        ):
            image = Image.new(
                "RGB", (1400 * scale, 700 * scale), "#242629" if dark else "#ffffff"
            )
            draw = ImageDraw.Draw(image)
            font = ImageFont.truetype(font_path, size * scale)
            for j, text in enumerate(lines):
                draw.text(
                    (40 * scale, (40 + j * (size + 24)) * scale),
                    text,
                    font=font,
                    fill="#ededed" if dark else "#242629",
                )
            path = fixtures / f"{language}-{i}.jpg"
            image.save(path, quality=90)
            samples.append(
                {
                    "id": path.stem,
                    "language": language,
                    "path": path.name,
                    "reference": "\n".join(lines),
                    "font": font_path,
                    "font_size": size,
                    "dark": dark,
                    "scale": scale,
                    "sha256": sha256(path),
                }
            )
    save(
        fixtures / "manifest.json",
        {
            "licence": "CC0-1.0",
            "provenance": "Original generated public test text. No screen capture or application data.",
            "samples": samples,
        },
    )
    binary = CACHE / "vision-eval"
    subprocess.run(
        [
            "nice",
            "-n",
            "15",
            "swiftc",
            "-parse-as-library",
            "-O",
            str(ROOT / "vision_eval.swift"),
            "-o",
            str(binary),
        ],
        check=True,
    )
    worker = subprocess.Popen(
        [str(binary)], stdin=subprocess.PIPE, stdout=subprocess.PIPE, text=True
    )

    def request(path, mode):
        worker.stdin.write(json.dumps({"path": str(path), "mode": mode}) + "\n")
        worker.stdin.flush()
        return json.loads(worker.stdout.readline())

    try:
        supported = request(fixtures / samples[0]["path"], "languages")["text"].split(
            ","
        )
        save(OUT / "vision-languages.json", {"languages": supported})
        for mode in ["app", "multilingual", "per-language"]:
            request(
                fixtures / samples[0]["path"], "app" if mode == "per-language" else mode
            )
            output = {"mode": mode, "status": "running", "samples": []}
            for row in samples:
                result = request(
                    fixtures / row["path"],
                    (
                        "language:" + LOCALES[row["language"]]
                        if mode == "per-language"
                        else mode
                    ),
                )
                output["samples"].append(
                    {**row, **result, **ocr_score(row["reference"], result["text"])}
                )
            output["by_language"] = {}
            for lang in TEXTS:
                rows = [r for r in output["samples"] if r["language"] == lang]
                output["by_language"][lang] = {
                    "character_accuracy": 1
                    - sum(r["edits"] for r in rows)
                    / sum(r["reference_characters"] for r in rows),
                    "mean_latency_ms": statistics.mean(r["latency_ms"] for r in rows),
                    "errors": sum(r.get("error") is not None for r in rows),
                }
            output["status"] = "complete"
            save(OUT / "runs" / f"ocr-{mode}.json", output)
            print(mode, output["by_language"], flush=True)
    finally:
        worker.stdin.close()
        worker.wait(timeout=30)


def warm_language_probe():
    """Retain one explicit first-language call, then score all six variants."""
    fixtures = OUT / "ocr-fixtures"
    rows = json.loads((fixtures / "manifest.json").read_text())["samples"]
    worker = subprocess.Popen(
        [str(CACHE / "vision-eval")],
        stdin=subprocess.PIPE,
        stdout=subprocess.PIPE,
        text=True,
    )

    def request(row):
        worker.stdin.write(
            json.dumps({"path": str(fixtures / row["path"]), "mode": "multilingual"})
            + "\n"
        )
        worker.stdin.flush()
        return json.loads(worker.stdout.readline())

    output = {
        "mode": "multilingual; one excluded warmup per language",
        "status": "running",
        "warmups": [],
        "samples": [],
        "by_language": {},
    }
    try:
        for lang in TEXTS:
            selected = [r for r in rows if r["language"] == lang]
            output["warmups"].append({"language": lang, **request(selected[0])})
            for row in selected:
                result = request(row)
                output["samples"].append(
                    {**row, **result, **ocr_score(row["reference"], result["text"])}
                )
            measured = [r for r in output["samples"] if r["language"] == lang]
            output["by_language"][lang] = {
                "character_accuracy": 1
                - sum(r["edits"] for r in measured)
                / sum(r["reference_characters"] for r in measured),
                "mean_latency_ms": statistics.mean(r["latency_ms"] for r in measured),
                "errors": sum(r.get("error") is not None for r in measured),
            }
        output["status"] = "complete"
        save(OUT / "runs" / "ocr-multilingual-warm.json", output)
        print(output["by_language"], flush=True)
    finally:
        worker.stdin.close()
        worker.wait(timeout=30)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--warm-language", action="store_true")
    args = parser.parse_args()
    with model_run():
        warm_language_probe() if args.warm_language else run()
