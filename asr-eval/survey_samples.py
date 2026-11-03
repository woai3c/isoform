"""Index external listening WAVs without putting audio in the repository."""

import argparse
import hashlib
import json
import wave
from pathlib import Path

from survey_assets import ROOT

DEFAULT_SAMPLES_DIR = Path.home() / "src" / "model-survey-samples"
INDEX_PATH = ROOT / "tts-samples" / "samples.json"


def build_sample_index():
    samples = []
    for path in sorted((ROOT / "survey-2026-10/runs").glob("tts-*.json")):
        if path.name == "tts-backtranscription.json":
            continue
        run = json.loads(path.read_text())
        if run.get("status") != "complete":
            raise ValueError(f"Incomplete synthesis run: {path}")
        for row in run["samples"]:
            samples.append(
                {
                    "file_name": Path(row["path"]).name,
                    "sentence": row["reference"],
                    "language": row["lang"],
                    "model": run["model"],
                    "voice": row["voice"],
                    "prompt_index": row["prompt_index"],
                    "sha256": row["sha256"],
                    "size_bytes": row["size_bytes"],
                    "sample_rate": row["sample_rate"],
                    "duration_s": row["duration_s"],
                    "recorded_path": row["path"],
                }
            )
    names = [s["file_name"] for s in samples]
    if len(names) != len(set(names)):
        raise ValueError("Duplicate external sample filename")
    return {
        "schema_version": 2,
        "storage": "~/src/model-survey-samples/ on the survey Mac; WAVs are not stored in git. The hub sends them to Bo.",
        "recorded_path_semantics": "Historical logical identifier in raw runs; resolve file_name against the external samples directory.",
        "samples": samples,
    }


def write_sample_index():
    INDEX_PATH.write_text(
        json.dumps(build_sample_index(), ensure_ascii=False, indent=2) + "\n"
    )


def verify_sample_files(index, directory):
    for row in index["samples"]:
        if Path(row["file_name"]).name != row["file_name"]:
            raise ValueError("Sample filename must not contain a directory")
        wav = directory / row["file_name"]
        if wav.stat().st_size != row["size_bytes"]:
            raise ValueError(f"WAV byte size mismatch: {wav}")
        if hashlib.sha256(wav.read_bytes()).hexdigest() != row["sha256"]:
            raise ValueError(f"WAV checksum mismatch: {wav}")
        with wave.open(str(wav), "rb") as audio:
            assert audio.getsampwidth() == 2 and audio.getnchannels() == 1
            assert audio.getframerate() == row["sample_rate"]
            assert (
                abs(audio.getnframes() / audio.getframerate() - row["duration_s"])
                < 0.001
            )


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify-dir", type=Path)
    args = parser.parse_args()
    write_sample_index()
    if args.verify_dir is not None:
        verify_sample_files(build_sample_index(), args.verify_dir)
    print(f"Indexed {len(build_sample_index()['samples'])} external WAVs")


if __name__ == "__main__":
    main()
