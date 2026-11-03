"""Build the setup manifest draft from pinned inventories and measured evidence.

Transport size and installed size are separate. System APIs and the disabled
choice have no invented downloadable weights. No URLs for nonexistent mirrors.
"""

import hashlib
import argparse
import json
import urllib.request
from pathlib import Path

from survey_assets import ROOT, RAW
from validate_survey import validate_recommendations

OUT = ROOT / "survey-2026-10"
APACHE = {"name": "Apache-2.0", "url": "https://www.apache.org/licenses/LICENSE-2.0"}
MIT = {"name": "MIT", "url": "https://spdx.org/licenses/MIT.html"}
FUNASR = {
    "name": "FunASR Model Open Source License Agreement v1.1",
    "url": "https://github.com/modelscope/FunASR/blob/main/MODEL_LICENSE",
}
ESPEAK = "https://github.com/espeak-ng/espeak-ng/blob/master/COPYING"
SPACY_MIT = (
    "https://github.com/explosion/spacy-models/releases/tag/en_core_web_sm-3.8.0"
)


def model_card(repo):
    snapshot = json.loads((OUT / "sources.json").read_text())
    source = next(
        s for s in snapshot["sources"] if s["url"] == "https://huggingface.co/" + repo
    )
    return f"https://huggingface.co/{repo}/blob/{source['revision']}/README.md"


def apply_licence_status(entry):
    """Classify the selected artifact and runtime route, not just its code licence."""
    model_id = entry["id"]
    evidence = []
    if entry["licence"]["url"]:
        evidence.append(
            {
                "url": entry["licence"]["url"],
                "reason": "Declared licence text or publisher declaration.",
            }
        )
    entry["gpl_components"] = []
    if model_id == "sensevoice":
        entry["licence_status"] = "pending"
        entry["licence_status_note"] = (
            "Commercial use and redistribution of these pinned ONNX files await licence-owner confirmation; v1.1 attribution and reference/learning wording remain unresolved."
        )
        entry["non_commercial"] = None
        entry["recommended"] = False
        entry["selectable"] = False
        evidence += [
            {
                "url": "https://github.com/QwenAudio/SenseVoice/issues/334",
                "reason": "Reopened August 29; the July reply is context, not final licence-owner/core-maintainer confirmation.",
            },
            {
                "url": "https://github.com/modelscope/FunASR/issues/3715",
                "reason": "Commercial use, applicable licence version and redistribution of the pinned INT8 ONNX/tokens files remain open.",
            },
        ]
    elif model_id in {"kokoro019", "kokoro11", "kokoro_mlx"}:
        entry["licence_status"] = "restricted"
        entry["licence_status_note"] = (
            "GPL component: needs source offer and notices if bundled"
        )
        entry["gpl_components"] = ["eSpeak-ng phonemizer and data"]
        entry["restriction_acceptance_flag"] = "gplComplianceAccepted"
        entry["component_licences"] = [
            {
                "component": "eSpeak-ng phonemizer and data",
                "name": "GPL-3.0-or-later",
                "url": ESPEAK,
                "obligations": entry["licence_status_note"],
            }
        ]
        evidence.append(
            {
                "url": ESPEAK,
                "reason": "Phonemization includes a GPL component separately from the Apache-2.0 Kokoro weights.",
            }
        )
        evidence.append(
            {
                "url": (
                    "https://huggingface.co/hexgrad/Kokoro-82M-v1.1-zh"
                    if model_id == "kokoro11"
                    else model_card("hexgrad/Kokoro-82M")
                ),
                "reason": "Publisher's Apache-2.0 weight declaration, separate from the phonemizer licence.",
            }
        )
        if model_id == "kokoro_mlx":
            entry["alternative_without_gpl"] = "ming_tts"
    else:
        sources = {
            "sensevoice_nano": lambda: model_card("FunAudioLLM/Fun-ASR-Nano-2512"),
            "funasr_nano": lambda: model_card("FunAudioLLM/Fun-ASR-Nano-2512"),
            "qwen06": lambda: model_card("Qwen/Qwen3-ASR-0.6B"),
            "qwen17": lambda: model_card("Qwen/Qwen3-ASR-1.7B"),
            "melo": lambda: model_card("myshell-ai/MeloTTS-Chinese"),
            "ming_tts": lambda: model_card("inclusionAI/Ming-3.0-tts-0.5B"),
            "mimo": lambda: inventory("mimo")["files"][0]["url"],
            "mimo-audio-tokenizer": lambda: inventory("mimo_tokenizer")["files"][0][
                "url"
            ],
            "parakeet-v2": lambda: entry["licence"]["url"],
            "whisper-turbo": lambda: entry["licence"]["url"],
            "kokoro-english-g2p": lambda: SPACY_MIT,
            "apple-vision": lambda: "https://www.apple.com/legal/sla/",
            "apple-documents": lambda: "https://www.apple.com/legal/sla/",
            "ppocr6": lambda: "https://github.com/PaddlePaddle/PaddleOCR/releases/tag/v3.7.0",
            "speaker-pack": lambda: entry["component_licences"][0]["url"],
            "after-call-none": lambda: "https://github.com/example/isoform/blob/508fe59f/docs/setup/handoff.md",
        }
        if model_id not in sources:
            raise ValueError(f"Missing artifact licence review: {model_id}")
        entry["licence_status"] = "cleared"
        entry["licence_status_note"] = (
            "Documented permissive artifact licence; preserve copyright, attribution and licence notices."
        )
        evidence.append(
            {
                "url": sources[model_id](),
                "reason": "Publisher's model declaration, selected distribution licence, or documented system/disabled delivery route.",
            }
        )
        if model_id.startswith("apple-"):
            entry["licence_status_note"] = (
                "Cleared for the system API route under macOS/SDK terms; no permission to redistribute Apple's private weights."
            )
        elif model_id == "after-call-none":
            entry["licence_status_note"] = (
                "No model or runtime is downloaded or redistributed."
            )
        elif model_id == "ming_tts":
            entry["licence_status_note"] = (
                "MIT weights and selected distribution; the evaluated text-tokenizer/AudioVAE path does not use eSpeak or another GPL phonemizer."
            )
        elif model_id == "mimo-audio-tokenizer":
            entry["licence_status_note"] = (
                "Pinned LICENSE is MIT; hub metadata says Apache-2.0. Retain the actual MIT file and conversion notices rather than treating the tag as the licence text."
            )
        for component in entry.get("component_licences", []):
            evidence.append(
                {"url": component["url"], "reason": "Selected component licence."}
            )
    entry["licence_evidence"] = evidence
    entry["licence_reviewed_on"] = "2026-10-23"


def inventory(asset_id):
    return json.loads((OUT / "assets" / f"{asset_id}.json").read_text())


def hub_metadata(repo):
    raw = RAW / (repo.replace("/", "--") + ".json")
    if raw.exists():
        return json.loads(raw.read_text())
    snapshot = json.loads((OUT / "sources.json").read_text())
    source = next(
        s for s in snapshot["sources"] if s["url"] == "https://huggingface.co/" + repo
    )
    return {
        "sha": source["revision"],
        "siblings": [
            {
                "rfilename": f["path"],
                "size": f["size_bytes"],
                "lfs": {"sha256": f["lfs_sha256"]} if f["lfs_sha256"] else None,
            }
            for f in source["files"]
        ],
    }


def from_asset(asset_id, job, name, phrase, licence, evidence, recommended=False):
    data = inventory(asset_id)
    if "transport" in data:
        transport = data["transport"]
        files = [
            {
                "path": transport["path"],
                "size_bytes": transport["size_bytes"],
                "sha256": transport["sha256"],
                "sources": [transport["url"]],
            }
        ]
    else:
        files = [
            {
                **{k: f[k] for k in ("path", "size_bytes", "sha256")},
                "sources": [f["url"]],
            }
            for f in data["files"]
        ]
    return {
        "id": asset_id,
        "job": job,
        "display_name": name,
        "line": phrase,
        "evidence_line": evidence,
        "recommended": recommended,
        "size_bytes": sum(f["size_bytes"] for f in files),
        "installed_size_bytes": data.get(
            "installed_size_bytes", sum(f["size_bytes"] for f in data["files"])
        ),
        "files": files,
        "sources": list(dict.fromkeys(url for f in files for url in f["sources"])),
        "licence": licence,
        "non_commercial": False,
        "inventory": f"survey-2026-10/assets/{asset_id}.json",
        "verification": "downloaded SHA-256",
    }


def from_hub_metadata(repo, model_id, job, name, phrase, licence, evidence):
    data = hub_metadata(repo)
    files = []
    for f in data["siblings"]:
        path = f["rfilename"]
        if path.startswith((".", "samples/")) or path.endswith((".md", ".png", ".jpg")):
            continue
        url = f"https://huggingface.co/{repo}/resolve/{data['sha']}/{path}"
        digest = (f.get("lfs") or {}).get("sha256")
        method = "Hugging Face LFS SHA-256 (not downloaded in October)"
        if not digest:
            content = urllib.request.urlopen(url, timeout=60).read()
            digest = hashlib.sha256(content).hexdigest()
            method = "downloaded small file SHA-256"
        files.append(
            {
                "path": path,
                "size_bytes": f["size"],
                "sha256": digest,
                "sources": [url],
                "verification": method,
            }
        )
    return {
        "id": model_id,
        "job": job,
        "display_name": name,
        "line": phrase,
        "evidence_line": evidence,
        "recommended": False,
        "size_bytes": sum(f["size_bytes"] for f in files),
        "installed_size_bytes": sum(f["size_bytes"] for f in files),
        "files": files,
        "sources": [f"https://huggingface.co/{repo}/tree/{data['sha']}"],
        "licence": licence,
        "non_commercial": False,
        "revision": data["sha"],
        "verification": "upstream LFS weight hashes; small files downloaded",
        "october_measured": False,
    }


def system_entry(model_id, name, phrase, evidence, recommended=False):
    return {
        "id": model_id,
        "job": "ocr",
        "display_name": name,
        "line": phrase,
        "evidence_line": evidence,
        "recommended": recommended,
        "size_bytes": 0,
        "installed_size_bytes": 0,
        "files": [],
        "sources": ["https://developer.apple.com/documentation/vision"],
        "licence": {
            "name": "Apple macOS software and SDK terms (system API)",
            "url": "https://www.apple.com/legal/sla/",
        },
        "non_commercial": False,
        "delivery": "system",
        "notes": "Use the OS framework. Do not redistribute or invent checksums for Apple's private model weights.",
    }


def asr_evidence(model_id):
    run = json.loads((OUT / "runs" / f"asr-{model_id}.json").read_text())
    if run.get("status") == "warmup_timeout":
        return "October attempt: loaded in 7.55s but warmup did not finish within 600s on the shared 16GB Mac with an 8GiB MLX limit; accuracy/RTF unavailable. Requires a separate 2.58GB audio tokenizer."
    data = run["results"][model_id]["datasets"]
    en = data["librispeech_test_clean"]["corpus_accuracy"]["token_error_rate"]
    zh = data["fleurs_zh"]["corpus_accuracy"]["character_error_rate"]
    mixed = data["ascend_mixed"]["corpus_accuracy"]["token_error_rate"]
    total_s = sum(d["performance"]["total_processing_s"] for d in data.values())
    audio_s = sum(d["performance"]["total_audio_s"] for d in data.values())
    return f"October pilot, 30 clips/set: English WER {en:.1%}; Mandarin CER {zh:.1%}; mixed MER {mixed:.1%}; corpus RTF {total_s/audio_s:.3f}."


def build_manifest(gpl_compliance_accepted=False):
    if type(gpl_compliance_accepted) is not bool:
        raise ValueError("gplComplianceAccepted must be a boolean")
    # Recommendation and TTS evidence are filled from the completed run summary.
    choices = json.loads((OUT / "recommendations.json").read_text())
    choices["recommended"]["voice"] = (
        "kokoro_mlx" if gpl_compliance_accepted else "ming_tts"
    )
    entries = []
    for asset_id, job, name, phrase, licence in [
        (
            "sensevoice",
            "live",
            "SenseVoice Small int8",
            "Fast bilingual audio on two CPU threads.",
            FUNASR,
        ),
        (
            "sensevoice_nano",
            "live",
            "SenseVoice FunASR-Nano encoder int8",
            "Compact CTC alternative; more errors in this pilot.",
            APACHE,
        ),
        (
            "qwen06",
            "live",
            "Qwen3-ASR 0.6B int8",
            "Cleared bilingual live choice, with higher CPU decode cost.",
            APACHE,
        ),
        (
            "funasr_nano",
            "after-call",
            "Fun-ASR-Nano int8",
            "Bilingual decoder with a native CPU runtime.",
            APACHE,
        ),
        (
            "qwen17",
            "after-call",
            "Qwen3-ASR 1.7B MLX 8-bit",
            "Re-read recordings with stronger bilingual accuracy.",
            APACHE,
        ),
    ]:
        entry = from_asset(
            asset_id,
            job,
            name,
            phrase,
            licence,
            asr_evidence(asset_id),
            choices["recommended"].get(job) == asset_id,
        )
        if asset_id == "sensevoice":
            pin = hub_metadata(
                "csukuangfj/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17"
            )["sha"]
            required = [
                f
                for f in inventory(asset_id)["files"]
                if f["path"] in ("model.int8.onnx", "tokens.txt")
            ]
            entry["files"] = [
                {
                    **f,
                    "sources": [
                        "https://model-assets.yansu.app/sherpa/" + f["path"],
                        f"https://huggingface.co/csukuangfj/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/resolve/{pin}/"
                        + f["path"],
                    ],
                }
                for f in required
            ]
            entry["size_bytes"] = entry["installed_size_bytes"] = sum(
                f["size_bytes"] for f in required
            )
            entry["sources"] = [
                "https://model-assets.yansu.app/sherpa/",
                f"https://huggingface.co/csukuangfj/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/tree/{pin}",
            ]
            entry["notes"] = (
                "Direct files match the app's canonical SHA-256 pins. Mirror order comes from bundled_sherpa.go; mirror bytes were not fetched in this run. Preserve upstream attribution and model name."
            )
        if job == "live":
            entry["requires"] = ["speaker-pack"]
        if asset_id == "sensevoice":
            entry["also_serves"] = ["after-call"]
        if asset_id == "qwen06":
            entry["also_serves"] = ["after-call"]
            entry["requires"] = ["speaker-pack"]
        entries.append(entry)
    if (OUT / "runs/asr-mimo.json").exists():
        entries.append(
            from_asset(
                "mimo",
                "after-call",
                "MiMo V2.5 ASR MLX 4-bit",
                "Accuracy-oriented Chinese and English alternative.",
                {
                    "name": "MIT (original weights); Apache-2.0 (MLX distribution)",
                    "url": model_card("XiaomiMiMo/MiMo-V2.5-ASR"),
                },
                asr_evidence("mimo"),
                choices["recommended"]["after-call"] == "mimo",
            )
        )
        entries[-1]["requires"] = ["mimo-audio-tokenizer"]
        entries[-1]["october_status"] = json.loads(
            (OUT / "runs/asr-mimo.json").read_text()
        ).get("status", "complete")
        if entries[-1]["october_status"] != "complete":
            entries[-1]["selectable"] = False
            entries[-1][
                "notes"
            ] = "Inventory retained for review; require a completed supported-runtime evaluation before offering this download."
        tokenizer = from_asset(
            "mimo_tokenizer",
            "after-call",
            "MiMo audio tokenizer",
            "Required audio encoder for MiMo V2.5 ASR.",
            {
                "name": "MIT",
                "url": "https://huggingface.co/mlx-community/MiMo-Audio-Tokenizer/blob/main/LICENSE",
            },
            "Pinned separate encoder weights; included in MiMo package totals.",
        )
        tokenizer["id"] = "mimo-audio-tokenizer"
        tokenizer["kind"] = "support_asset"
        tokenizer["selectable"] = False
        entries.append(tokenizer)
    for model_id, name, licence in [
        ("kokoro019", "Kokoro English v0.19", APACHE),
        ("melo", "MeloTTS zh_en", MIT),
        ("kokoro11", "Kokoro bilingual v1.1 int8", APACHE),
        ("kokoro_mlx", "Kokoro bilingual v1.0 MLX", APACHE),
        ("ming_tts", "Ming 3.0 TTS 0.5B MLX 8-bit", MIT),
    ]:
        if not (OUT / "assets" / f"{model_id}.json").exists():
            continue
        entry = from_asset(
            model_id,
            "voice",
            name,
            choices["voice_lines"][model_id],
            licence,
            choices["voice_evidence"][model_id],
            choices["recommended"]["voice"] == model_id,
        )
        entry["runtime_components"] = [
            "See requirements-october.txt for the Python evaluation environment; app runtime packaging is separate from these model bytes."
        ]
        if model_id.startswith("kokoro"):
            entry["component_licences"] = [
                {
                    "name": "eSpeak NG GPL-3.0-or-later (G2P/data)",
                    "url": "https://github.com/espeak-ng/espeak-ng/blob/master/COPYING",
                }
            ]
        if model_id in ("kokoro019", "melo"):
            archive = entry["files"][0]["path"]
            entry["files"][0]["sources"] = [
                f"https://model-assets.yansu.app/sherpa/tts/{archive}",
                f"https://release.yansu.app/sherpa/tts/{archive}",
                *entry["files"][0]["sources"],
            ]
            entry["sources"] = entry["files"][0]["sources"]
            entry["notes"] = (
                "Existing mirror URLs copied from bundled_tts.go; mirror bytes not fetched in this survey. All sources must satisfy the same SHA-256."
            )
        entries.append(entry)
    entries += [
        from_hub_metadata(
            "mlx-community/parakeet-tdt-0.6b-v2",
            "parakeet-v2",
            "live",
            "Parakeet TDT v2 MLX",
            "English only; keep the shared speaker pack.",
            {
                "name": "CC-BY-4.0",
                "url": model_card("nvidia/parakeet-tdt-0.6b-v2"),
            },
            "June 500-clip evaluation: English clean WER 2.49%; no Chinese. Not rerun in October.",
        ),
        from_hub_metadata(
            "mlx-community/whisper-large-v3-turbo",
            "whisper-turbo",
            "live",
            "Whisper large-v3-turbo MLX",
            "Broad language coverage; weaker code-switching evidence.",
            {
                "name": "MIT",
                "url": "https://github.com/openai/whisper/blob/main/LICENSE",
            },
            "June 500-clip evaluation: English clean WER 4.84%; Chinese WER 9.06%. Mixed normalization was invalid; not rerun in October.",
        ),
    ]
    g2p = from_asset(
        "kokoro-english-g2p",
        "voice",
        "Kokoro English pronunciation support",
        "English pronunciation support for the chosen Kokoro voice.",
        {
            "name": "MIT",
            "url": SPACY_MIT,
        },
        "en_core_web_sm 3.8.0; explicit pinned wheel, not an unannounced model download.",
    )
    g2p["kind"] = "support_asset"
    g2p["selectable"] = False
    entries.append(g2p)
    for entry in entries:
        if entry["id"] == "kokoro_mlx":
            entry["requires"] = ["kokoro-english-g2p"]
        if entry["id"] in ("parakeet-v2", "whisper-turbo"):
            entry["requires"] = ["speaker-pack"]
    for model_id, name, phrase, run in [
        (
            "apple-vision",
            "Apple Vision text recognition",
            "Continuous screen text through the built-in macOS API.",
            "app",
        ),
        (
            "apple-documents",
            "Apple Vision document recognition",
            "macOS 26 document structure for tables and lists.",
            "documents",
        ),
    ]:
        data = json.loads((OUT / "runs" / f"ocr-{run}.json").read_text())["summary"]
        entries.append(
            system_entry(
                model_id,
                name,
                phrase,
                f"October 30-screen pilot: {data['character_accuracy']:.1%} character accuracy; {data['latency_mean_ms']:.0f} ms/frame; {data['cpu_percent_mean']:.0f}% CPU during calls (100% = one core).",
                choices["recommended"]["ocr"] == model_id,
            )
        )
    pp = inventory("ppocr6")
    ocr = json.loads((OUT / "runs/ocr-ppocr6.json").read_text())["summary"]
    entries.append(
        from_asset(
            "ppocr6",
            "ocr",
            "PP-OCRv6 tiny ONNX",
            "Small downloadable OCR; higher CPU use than native Vision.",
            APACHE,
            f"October 30-screen pilot: {ocr['character_accuracy']:.1%} character accuracy; {ocr['latency_mean_ms']:.0f} ms/frame; {ocr['cpu_percent_mean']:.0f}% CPU during calls.",
        )
    )
    pack = from_asset(
        "speakers",
        "speaker-pack",
        "Silero VAD + pyannote segmentation + CAM++",
        "Required speech boundaries and speaker separation for every live engine.",
        {
            "name": "MIT (Silero/pyannote); Apache-2.0 (CAM++)",
            "url": "https://github.com/k2-fsa/sherpa-onnx",
        },
        "Same model SHA-256 pins as production; see the public two-speaker smoke test, not a meeting DER benchmark.",
    )
    pack["id"] = "speaker-pack"
    pack["component_licences"] = [
        {
            "component": "Silero VAD",
            "name": "MIT",
            "url": "https://github.com/snakers4/silero-vad/blob/master/LICENSE",
        },
        {
            "component": "pyannote segmentation 3.0",
            "name": "MIT",
            "url": "https://huggingface.co/pyannote/segmentation-3.0/blob/main/LICENSE",
        },
        {
            "component": "3D-Speaker CAM++",
            "name": "Apache-2.0",
            "url": "https://github.com/modelscope/3D-Speaker/blob/main/LICENSE",
        },
    ]
    entries.append(pack)
    entries.append(
        {
            "id": "after-call-none",
            "job": "after-call",
            "display_name": "None",
            "line": "Keep the live transcript.",
            "evidence_line": "No second-pass inference or download.",
            "recommended": False,
            "size_bytes": 0,
            "installed_size_bytes": 0,
            "files": [],
            "sources": [],
            "licence": {"name": "Not applicable", "url": None},
            "non_commercial": False,
            "delivery": "disabled",
        }
    )
    for entry in entries:
        apply_licence_status(entry)
        entry.setdefault(
            "runtime",
            choices.get("runtimes", {}).get(
                entry["id"],
                "system" if entry.get("delivery") == "system" else "see results report",
            ),
        )
    output = {
        "schema_version": 3,
        "status": "draft; evaluation only, not an app asset catalogue",
        "survey_date": "2026-10-23",
        "size_semantics": "size_bytes is exact download bytes for listed files; archives differ from installed_size_bytes. Runtime packaging is not included in model download bytes.",
        "scope": "Four surveyed jobs, existing setup alternatives and the mandatory speaker pack. Privacy/memory models belong to a separate app manifest.",
        "recommendations": choices["recommended"],
        "gplComplianceAccepted": gpl_compliance_accepted,
        "alternatives_without_gpl": {"voice": "ming_tts"},
        "gpl_component_scope": "Required phonemization components for the selected engine path, not a complete app/runtime SBOM. Package Ming separately from the survey environment's unused Kokoro/eSpeak dependencies.",
        "licence_status_semantics": {
            "cleared": "Documented permitted delivery/use route, with ordinary licence/attribution conditions retained.",
            "pending": "Unresolved permission; do not recommend or offer this download as cleared. non_commercial is null when the restriction is unknown.",
            "restricted": "A recommendation requires its named restriction_acceptance_flag to be explicitly true. Kokoro requires gplComplianceAccepted; this is distinct from a non-commercial restriction and does not itself fulfil GPL obligations.",
        },
        "models": entries,
    }
    from multilingual_manifest import apply_multilingual

    apply_multilingual(output)
    validate_recommendations(output)
    return output


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--gpl-compliance-accepted", action="store_true")
    parser.add_argument("--output", type=Path, default=ROOT / "manifest-draft.json")
    args = parser.parse_args()
    args.output.write_text(
        json.dumps(
            build_manifest(args.gpl_compliance_accepted), ensure_ascii=False, indent=2
        )
        + "\n"
    )


if __name__ == "__main__":
    main()
