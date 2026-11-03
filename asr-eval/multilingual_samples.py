"""Build and verify the external, hashed part-2 voice sample inventory."""

import argparse
import json
from pathlib import Path
from multilingual_eval import ROOT, OUT, save, sha256
from survey_samples import verify_sample_files

INDEX = ROOT / "samples-2.json"


def build_index():
    rows = []
    for path in sorted((OUT / "runs").glob("tts-*.json")):
        run = json.loads(path.read_text())
        for sample in run.get("samples", []) + run.get("briefs", []):
            rows.append({k: v for k, v in sample.items() if k != "sentences"})
    names = [r["file_name"] for r in rows]
    if len(names) != len(set(names)):
        raise ValueError("Duplicate sample filenames")
    return {
        "schema_version": 1,
        "directory": "~/src/model-survey-samples-2/",
        "provenance": "Synthetic evaluation speech from the committed public prompts; no private recordings. Reference voices, where used, are prior-survey synthetic Ming presets.",
        "references": json.loads((OUT / "voice-references.json").read_text())[
            "samples"
        ],
        "samples": rows,
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--verify-dir", type=Path)
    args = parser.parse_args()
    index = build_index()
    if args.verify_dir:
        verify_sample_files(index, args.verify_dir.expanduser())
        verify_sample_files(
            {"samples": index["references"]}, args.verify_dir.expanduser()
        )
    save(INDEX, index)
    print(len(index["samples"]), "indexed external voice files")


if __name__ == "__main__":
    main()
