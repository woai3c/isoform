"""Validate committed evidence, manifest arithmetic and listening sample integrity."""

import hashlib
import argparse
import json
import math
import re
from pathlib import Path

from survey_assets import ROOT
from survey_samples import INDEX_PATH, build_sample_index, verify_sample_files


def walk_numbers(value):
    if isinstance(value, float):
        assert math.isfinite(value), value
    elif isinstance(value, dict):
        for v in value.values():
            walk_numbers(v)
    elif isinstance(value, list):
        for v in value:
            walk_numbers(v)


def validate_recommendations(manifest):
    """Reject every pending or unaccepted restricted default, regardless of job."""
    gpl_accepted = manifest.get("gplComplianceAccepted", False)
    if type(gpl_accepted) is not bool:
        raise ValueError("gplComplianceAccepted must be a boolean")
    models = manifest["models"]
    choices = manifest["recommendations"]
    for model in models:
        if model["recommended"] and choices.get(model["job"]) != model["id"]:
            raise ValueError(f"Unlisted recommended entry: {model['id']}")
    for job, model_id in choices.items():
        selected = [m for m in models if m["job"] == job and m["recommended"]]
        if len(selected) != 1 or selected[0]["id"] != model_id:
            raise ValueError(f"Expected one matching recommendation for {job}")
        model = selected[0]
        status = model["licence_status"]
        if status == "cleared":
            continue
        if (
            status == "restricted"
            and model.get("restriction_acceptance_flag") == "gplComplianceAccepted"
            and model.get("gpl_components")
            and model.get("non_commercial") is False
            and gpl_accepted
        ):
            continue
        raise ValueError(f"Unaccepted licence status for {job}: {model_id} ({status})")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path, default=ROOT / "manifest-draft.json")
    parser.add_argument(
        "--samples-dir",
        type=Path,
        help="Also verify the external WAV bytes and PCM headers",
    )
    args = parser.parse_args()
    manifest = json.loads(args.manifest.read_text())
    validate_recommendations(manifest)
    ids = {m["id"] for m in manifest["models"]}
    assert len(ids) == len(manifest["models"])
    for model in manifest["models"]:
        if manifest["schema_version"] >= 4:
            assert isinstance(model["languages"], list), model["id"]
            assert len(model["languages"]) == len(set(model["languages"])), model["id"]
            assert model["language_scope"] and model["language_evidence"], model["id"]
        assert model["size_bytes"] == sum(
            f["size_bytes"] for f in model["files"]
        ), model["id"]
        assert model["licence"]["name"]
        assert model["licence_status"] in {"cleared", "pending", "restricted"}
        assert model["licence_evidence"] and all(
            e["url"].startswith("https://") and e["reason"]
            for e in model["licence_evidence"]
        )
        if model["licence_status"] == "pending":
            assert model["non_commercial"] is None and not model["recommended"]
        else:
            assert isinstance(model["non_commercial"], bool)
        for dependency in model.get("requires", []):
            assert dependency in ids
        for f in model["files"]:
            assert re.fullmatch(r"[a-f0-9]{64}", f["sha256"]), f
            assert f["size_bytes"] > 0 and f["sources"]
    for job, model_id in manifest["recommendations"].items():
        assert model_id in ids
        recommended = [
            m for m in manifest["models"] if m["job"] == job and m["recommended"]
        ]
        assert len(recommended) == 1 and recommended[0]["id"] == model_id, job
        if job == "live":
            assert "speaker-pack" in recommended[0]["requires"]
    by_id = {m["id"]: m for m in manifest["models"]}
    assert by_id["sensevoice"]["licence_status"] == "pending"
    for model_id in ("kokoro019", "kokoro11", "kokoro_mlx"):
        assert by_id[model_id]["licence_status"] == "restricted"
        assert by_id[model_id]["gpl_components"]
        assert (
            by_id[model_id]["licence_status_note"]
            == "GPL component: needs source offer and notices if bundled"
        )
    alternative = by_id[manifest["alternatives_without_gpl"]["voice"]]
    assert (
        alternative["licence_status"] == "cleared"
        and alternative["gpl_components"] == []
    )
    index = json.loads(INDEX_PATH.read_text())
    assert index == build_sample_index()
    assert len(index["samples"]) == 34
    assert all(
        type(s["size_bytes"]) is int and s["size_bytes"] > 0 for s in index["samples"]
    )
    assert not list((ROOT / "tts-samples").glob("*.wav"))
    if args.samples_dir is not None:
        verify_sample_files(index, args.samples_dir.expanduser())
    runs = ROOT / "survey-2026-10/runs"
    for model_id in [
        "sensevoice",
        "sensevoice_nano",
        "qwen06",
        "qwen17",
        "funasr_nano",
    ]:
        data = json.loads((runs / f"asr-{model_id}.json").read_text())
        assert data["nice"] >= 10
        datasets = data["results"][model_id]["datasets"]
        assert len(datasets) == 5
        for ds in datasets.values():
            assert ds["num_samples"] == 30 and ds["failed_samples"] == 0
            assert ds["performance"]["num_samples"] == 30
    mimo = json.loads((runs / "asr-mimo.json").read_text())
    assert mimo["status"] == "warmup_timeout"
    assert (
        mimo["scored_utterances"] == 0
        and mimo["accuracy"] is None
        and mimo["rtf"] is None
    )
    assert mimo["elapsed_s"] >= 600 and mimo["mlx_memory_limit_bytes"] == 8 * 1024**3
    for name in ["app", "documents", "ppocr6"]:
        data = json.loads((runs / f"ocr-{name}.json").read_text())
        assert data["summary"]["count"] == 30
        assert data["summary"]["errors"] == 0
    clips = set()
    for path in runs.glob("tts-*.json"):
        if path.name == "tts-backtranscription.json":
            continue
        data = json.loads(path.read_text())
        assert data["status"] == "complete", path
        for row in data["samples"]:
            clips.add(row["path"])
        for brief in data["briefs"]:
            assert brief["characters_synthesized"] == len(brief["text"])
            assert "".join(brief["input_chunks"]) == brief["text"]
            if brief["lang"] == "en":
                assert brief["word_count"] == 300
        for lang in {r["lang"] for r in data["samples"]}:
            assert {
                r["prompt_index"] for r in data["samples"] if r["lang"] == lang
            } == {0, 1, 2}
    back = json.loads((runs / "tts-backtranscription.json").read_text())
    assert {r["path"] for r in back["samples"]} == clips
    pack = json.loads((runs / "speaker-pack.json").read_text())
    assert pack["six_turn_separation_passed"]
    fixtures = ROOT / "survey-2026-10/ocr-fixtures"
    data = json.loads((fixtures / "manifest.json").read_text())
    assert len(data["samples"]) == 30
    for row in data["samples"]:
        assert (
            hashlib.sha256((fixtures / row["path"]).read_bytes()).hexdigest()
            == row["sha256"]
        )
    for path in (ROOT / "survey-2026-10").rglob("*.json"):
        if "raw" not in path.parts:
            walk_numbers(json.loads(path.read_text()))
    print(
        f"Validated {len(ids)} manifest entries, five complete ASR runs plus one explicit deadline failure, three OCR runs, {len(clips)} external sample records and their back-transcriptions."
    )
    if args.samples_dir is not None:
        print("Verified all external WAV checksums and PCM headers")


if __name__ == "__main__":
    main()
