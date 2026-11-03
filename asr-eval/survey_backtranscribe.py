"""Back-transcribe saved TTS WAVs after all synthesizers have exited."""

import argparse
import json
import os
from pathlib import Path

import yaml

from engines import create_engine
from survey_assets import ROOT
from survey_metrics import corpus_scores
from survey_samples import DEFAULT_SAMPLES_DIR, INDEX_PATH, verify_sample_files


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--engine", default="qwen17")
    parser.add_argument("--samples-dir", type=Path, default=DEFAULT_SAMPLES_DIR)
    args = parser.parse_args()
    os.chdir(ROOT)
    if os.getpriority(os.PRIO_PROCESS, 0) < 10:
        raise SystemExit("Run with nice -n 15")
    verify_sample_files(
        json.loads(INDEX_PATH.read_text()), args.samples_dir.expanduser()
    )
    config = yaml.safe_load((ROOT / "survey-2026-10/config.yaml").read_text())[
        "engines"
    ][args.engine]
    engine = create_engine(
        config["type"], **{k: v for k, v in config.items() if k not in ("type", "name")}
    )
    engine.load()
    results = []
    primary_voices = {}
    try:
        for path in sorted((ROOT / "survey-2026-10/runs").glob("tts-*.json")):
            if path.name == "tts-backtranscription.json":
                continue
            data = json.loads(path.read_text())
            if data.get("status") != "complete":
                raise ValueError(f"Incomplete synthesis run: {path}")
            primary_voices[data["model"]] = data["voices"]
            for row in data["samples"]:
                hyp = engine.transcribe(
                    str(args.samples_dir.expanduser() / Path(row["path"]).name),
                    language_hint=row["lang"],
                ).text
                result = {
                    "model": data["model"],
                    **row,
                    "hypothesis": hyp,
                    **corpus_scores([row["reference"]], [hyp]),
                }
                results.append(result)
                print(row["path"], hyp, flush=True)
    finally:
        engine.unload()
    summary = {}
    for model in sorted({r["model"] for r in results}):
        summary[model] = {}
        for lang in ["en", "zh"]:
            rows = [
                r
                for r in results
                if r["model"] == model
                and r["lang"] == lang
                and r["voice"] == primary_voices[model][lang]
            ]
            if rows:
                summary[model][lang] = corpus_scores(
                    [r["reference"] for r in rows], [r["hypothesis"] for r in rows]
                )
    encoded = (
        json.dumps(
            {
                "asr_engine": args.engine,
                "warning": "Intelligibility proxy, not a naturalness or speaker-similarity score",
                "summary_scope": "Same three sentences in the primary voice per language; secondary voice clips remain in samples",
                "summary": summary,
                "samples": results,
            },
            ensure_ascii=False,
            indent=2,
        )
        + "\n"
    )
    (ROOT / "survey-2026-10/runs/tts-backtranscription.json").write_text(encoded)
    (ROOT / "survey-2026-10/runs" / f"backtranscription-{args.engine}.json").write_text(
        encoded
    )


if __name__ == "__main__":
    main()
