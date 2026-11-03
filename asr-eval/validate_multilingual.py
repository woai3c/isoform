"""Recompute part-2 metrics and check coverage, provenance and external audio."""

import argparse
import json
import math
import statistics
import subprocess
from pathlib import Path

from multilingual_eval import ROOT, OUT, LANGUAGES, score, sha256
from multilingual_asr import SUPPORTED
from multilingual_run import ASR
from multilingual_tts import VOICES, brief_sentences, sentence_delivery_gaps
from multilingual_samples import build_index
from multilingual_selection import rank_asr, rank_voice, primary_briefs
from survey_samples import verify_sample_files
from survey_metrics import ocr_score
from validate_survey import validate_recommendations, walk_numbers


def validate(samples_dir=None):
    manifest = json.loads((ROOT / "manifest-draft.json").read_text())
    validate_recommendations(manifest)
    for model in manifest["models"]:
        assert isinstance(model["languages"], list), model["id"]
        assert len(model["languages"]) == len(set(model["languages"])), model["id"]
        assert model["language_evidence"] and model["language_scope"], model["id"]
    corpora = {
        lang: json.loads((OUT / "data" / f"{lang}.json").read_text())
        for lang in LANGUAGES
    }
    for lang, data in corpora.items():
        assert data["licence"] == "CC-BY-4.0"
        assert len(data["samples"]) == 100
        assert len({s["audio_path"] for s in data["samples"]}) == 100
        assert all(
            len(s["sha256"]) == 64 and s["duration_s"] > 0 for s in data["samples"]
        )
    actual_asr = {
        p.stem.removeprefix("asr-") for p in (OUT / "runs").glob("asr-*.json")
    }
    assert actual_asr == set(ASR), (actual_asr, ASR)
    actual_tts = {
        p.stem.removeprefix("tts-") for p in (OUT / "runs").glob("tts-*.json")
    }
    assert actual_tts == set(VOICES), (actual_tts, VOICES)
    observations = 0
    for path in sorted((OUT / "runs").glob("asr-*.json")):
        run = json.loads(path.read_text())
        assert run["nice"] >= 10
        model = run["model"]
        assert run["status"] == "complete", path
        assert set(run["datasets"]) == set(SUPPORTED[model]), path
        for lang, data in run["datasets"].items():
            rows = data["samples"]
            assert data["status"] == "complete" and len(rows) == 100, path
            assert [
                {k: r[k] for k in ["id", "reference", "sha256", "duration_s"]}
                for r in rows
            ] == [
                {k: r[k] for k in ["id", "reference", "sha256", "duration_s"]}
                for r in corpora[lang]["samples"]
            ], path
            assert data["accuracy"] == score(
                [r["reference"] for r in rows], [r["hypothesis"] for r in rows], lang
            ), path
            assert math.isclose(
                data["rtf"],
                sum(r["inference_s"] for r in rows)
                / sum(r["duration_s"] for r in rows),
            ), path
            assert data["failed_samples"] == sum(
                r["error"] is not None for r in rows
            ), path
            observations += len(rows)
    prompts = json.loads((OUT / "prompts.json").read_text())
    for path in (OUT / "runs").glob("tts-*.json"):
        run = json.loads(path.read_text())
        assert run["status"] == "complete", path
        assert run["nice"] >= 10
        model = run["model"]
        expected = {
            (lang, voice, i)
            for lang, voices in VOICES[model].items()
            for voice in voices
            for i in range(len(prompts[lang]))
        }
        actual = {
            (r["language"], r["voice"], r["sentence_index"]) for r in run["samples"]
        }
        assert actual == expected, path
        assert len(run["briefs"]) >= len(VOICES[model]), path
        assert len({(b["language"], b["voice"]) for b in run["briefs"]}) == len(
            run["briefs"]
        ), path
        assert {b["language"] for b in run["briefs"]} == set(VOICES[model]), path
        profiles = {(lang, voice) for lang, voice, _ in expected}
        assert profiles == {
            (r["language"], r["voice"]) for r in run["first_sentence_trials"]
        }, path
        assert all(
            len(r["seconds"]) == 3 and min(r["seconds"]) > 0
            for r in run["first_sentence_trials"]
        ), path
        for followup in run.get("voice_followups", []):
            snapshot = OUT / followup["original_run_snapshot"]
            assert sha256(snapshot) == followup["snapshot_sha256"], path
            original = json.loads(snapshot.read_text())
            assert original["samples"] == run["samples"], path
            assert original["briefs"] == run["briefs"][: len(original["briefs"])], path
            assert followup["nice"] >= 10
            if followup["selected"]:
                assert (
                    primary_briefs(run)[followup["language"]]["voice"]
                    == followup["voice"]
                )
        for brief in run["briefs"]:
            lang = brief["language"]
            assert [r["reference"] for r in brief["sentences"]] == brief_sentences(
                prompts[lang], lang
            ), path
            assert math.isclose(
                brief["total_s"], sum(r["synthesis_s"] for r in brief["sentences"])
            ), path
            assert math.isclose(
                brief["audio_s"], sum(r["audio_s"] for r in brief["sentences"])
            ), path
            assert math.isclose(brief["rtf"], brief["total_s"] / brief["audio_s"]), path
            gaps = sentence_delivery_gaps(brief["sentences"])
            assert math.isclose(brief["sentence_delivery_gap_s"], sum(gaps)), path
            assert math.isclose(brief["maximum_sentence_gap_s"], max(gaps)), path
            assert math.isclose(
                brief["first_sentence_s"], brief["sentences"][0]["synthesis_s"]
            ), path
            assert math.isclose(
                brief["first_informative_sentence_ready_s"],
                sum(r["synthesis_s"] for r in brief["sentences"][:2]),
            ), path
    index = json.loads((ROOT / "samples-2.json").read_text())
    assert index == build_index()
    back = json.loads((OUT / "runs" / "voice-back-asr.json").read_text())
    assert back["status"] == "complete"
    assert back["nice"] >= 10
    assert len(back["samples"]) == len(index["samples"])
    assert {r["file_name"] for r in back["samples"]} == {
        r["file_name"] for r in index["samples"]
    }
    for row in back["samples"]:
        assert row["verifier_truncated"] is False
        assert row["accuracy"] == score(
            [row["reference"]], [row["hypothesis"]], row["language"]
        )
    for key, accuracy in back["by_model_language_voice"].items():
        model, lang, voice, kind = key.split("/")
        rows = [
            r
            for r in back["samples"]
            if (r["model"], r["language"], r["voice"], r["kind"])
            == (model, lang, voice, kind)
        ]
        assert accuracy == score(
            [r["reference"] for r in rows], [r["hypothesis"] for r in rows], lang
        ), key
    asr_runs = {
        p.stem.removeprefix("asr-"): json.loads(p.read_text())
        for p in (OUT / "runs").glob("asr-*.json")
    }
    tts_runs = {
        p.stem.removeprefix("tts-"): json.loads(p.read_text())
        for p in (OUT / "runs").glob("tts-*.json")
    }
    selected = {m["job"]: m for m in manifest["models"] if m["recommended"]}
    host = json.loads((OUT / "host.json").read_text())
    for job, maximum_rtf in [
        ("live", host["asr_live_screening_rtf_max"]),
        ("after-call", host["asr_after_call_screening_rtf_max"]),
    ]:
        ranked = rank_asr(asr_runs, maximum_rtf)
        assert ranked and selected[job]["evaluation_profile"] == ranked[0][1], job
    cleared = {
        m["evaluation_profile"]
        for m in manifest["models"]
        if m["job"] == "voice"
        and m["licence_status"] == "cleared"
        and "evaluation_profile" in m
    }
    ranked = rank_voice(tts_runs, back, cleared)
    if ranked:
        assert selected["voice"]["evaluation_profile"] == ranked[0][1]
        selected_run = tts_runs[ranked[0][1]]
        assert selected["voice"].get("generation_config") == selected_run.get(
            "generation_config"
        )
        assert selected["voice"]["primary_voices"] == {
            lang: b["voice"] for lang, b in primary_briefs(selected_run).items()
        }
        assert selected["voice"]["sentence_streaming_required"] is True
        assert selected["voice"]["resident_model_required"] is True
    else:
        assert selected["voice"].get("delivery") == "disabled"
    if samples_dir:
        verify_sample_files(index, samples_dir)
        verify_sample_files({"samples": index["references"]}, samples_dir)
        assert {p.name for p in samples_dir.glob("*.wav")} == {
            r["file_name"] for r in index["samples"] + index["references"]
        }, "External sample directory must have no unindexed or missing WAVs"
    fixtures = OUT / "ocr-fixtures"
    images = json.loads((fixtures / "manifest.json").read_text())["samples"]
    assert len(images) == 30
    for row in images:
        assert sha256(fixtures / row["path"]) == row["sha256"]
    for mode in ["app", "multilingual", "per-language", "multilingual-warm"]:
        run = json.loads((OUT / "runs" / f"ocr-{mode}.json").read_text())
        assert run["status"] == "complete" and len(run["samples"]) == 30
        assert all(not r.get("error") for r in run["samples"])
        assert {r["id"] for r in run["samples"]} == {r["id"] for r in images}
        for row in run["samples"]:
            accuracy = ocr_score(row["reference"], row["text"])
            assert all(row[k] == v for k, v in accuracy.items()), (mode, row["id"])
        for lang, summary in run["by_language"].items():
            rows = [r for r in run["samples"] if r["language"] == lang]
            assert len(rows) == 6
            assert math.isclose(
                summary["character_accuracy"],
                1
                - sum(r["edits"] for r in rows)
                / sum(r["reference_characters"] for r in rows),
            ), (mode, lang)
            assert math.isclose(
                summary["mean_latency_ms"],
                statistics.mean(r["latency_ms"] for r in rows),
            ), (mode, lang)
    for path in OUT.rglob("*.json"):
        walk_numbers(json.loads(path.read_text()))
    tracked_audio = subprocess.check_output(
        [
            "git",
            "ls-files",
            "--",
            "asr-eval/*.wav",
            "asr-eval/*.mp3",
            "asr-eval/*.flac",
        ],
        cwd=ROOT.parent,
        text=True,
    )
    assert not tracked_audio.strip(), "Audio must not be committed"
    print(
        f'Validated {observations} multilingual ASR observations, {len(index["samples"])} synthetic voice files/back-ASR rows, 120 OCR observations and all language declarations'
    )


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("--samples-dir", type=Path)
    args = p.parse_args()
    validate(args.samples_dir.expanduser() if args.samples_dir else None)
