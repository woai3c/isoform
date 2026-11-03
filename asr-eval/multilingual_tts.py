"""Sentence-by-sentence TTS, two voices/language, external PCM and latency traces.

Timings include materializing every sentence waveform in host memory. No audio
is played; readiness is measured at the PCM buffer, before a device's queue.
"""

import argparse
import json
import os
import platform
import resource
import time
from dataclasses import fields
from pathlib import Path

import numpy as np
import soundfile as sf

from multilingual_eval import (
    ROOT,
    OUT,
    CACHE,
    model_run,
    save,
    sha256,
    normalize,
    score,
)

SAMPLES = Path.home() / "src" / "model-survey-samples-2"
VOICES = {
    "ming_tts": {
        "en": ["UK_Charlotte", "US_James"],
        "zh": ["灵小柔", "灵信远"],
        "ja": ["UK_Charlotte", "US_James"],
        "es": ["UK_Charlotte", "US_James"],
        "fr": ["UK_Charlotte", "US_James"],
        "de": ["UK_Charlotte", "US_James"],
    },
    "kokoro_mlx": {
        "en": ["af_heart", "am_adam"],
        "zh": ["zf_xiaobei", "zm_yunxi"],
        "ja": ["jf_alpha", "jm_kumo"],
        "es": ["ef_dora", "em_alex"],
        "fr": ["ff_siwis"],
    },
    "melo": {"en": ["0"], "zh": ["0"]},
    "chatterbox": {
        lang: ["synthetic_charlotte", "synthetic_james"]
        for lang in ["en", "zh", "ja", "es", "fr", "de"]
    },
    "cosyvoice3": {
        lang: ["synthetic_charlotte", "synthetic_james"]
        for lang in ["en", "zh", "ja", "es", "fr", "de"]
    },
}
VOICES["ming_fast"] = VOICES["ming_tts"]
VOICES["chatterbox_fp16"] = VOICES["chatterbox"]


def prepare_references():
    old = json.loads((ROOT / "tts-samples" / "samples.json").read_text())
    records = []
    SAMPLES.mkdir(parents=True, exist_ok=True)
    for voice, preset in [
        ("synthetic_charlotte", "UK_Charlotte"),
        ("synthetic_james", "US_James"),
    ]:
        row = next(
            r
            for r in old["samples"]
            if preset in r["file_name"] and r["file_name"].endswith("-1.wav")
        )
        source = Path.home() / "src" / "model-survey-samples" / row["file_name"]
        if sha256(source) != row["sha256"]:
            raise ValueError("Reference sample hash differs from first survey")
        path = SAMPLES / (voice + "-reference.wav")
        path.write_bytes(source.read_bytes())
        records.append(
            {
                "voice": voice,
                "file_name": path.name,
                "sha256": sha256(path),
                "size_bytes": path.stat().st_size,
                "sample_rate": row["sample_rate"],
                "duration_s": row["duration_s"],
                "reference": row.get("sentence", row.get("reference")),
                "source": "First-survey Ming synthetic preset; not a real-person voice clone",
                "source_file": row["file_name"],
            }
        )
    save(OUT / "voice-references.json", {"samples": records})
    return records


def load_synth(name):
    import mlx.core as mx

    is_chatterbox = name in ("chatterbox", "chatterbox_fp16")
    checkpoint = "chatterbox" if is_chatterbox else name

    if name == "melo":
        import sherpa_onnx as s

        d = CACHE / name
        model = s.OfflineTts(
            s.OfflineTtsConfig(
                model=s.OfflineTtsModelConfig(
                    vits=s.OfflineTtsVitsModelConfig(
                        model=str(d / "model.onnx"),
                        lexicon=str(d / "lexicon.txt"),
                        tokens=str(d / "tokens.txt"),
                    ),
                    num_threads=2,
                ),
                rule_fsts=",".join(
                    str(d / n)
                    for n in ["phone.fst", "date.fst", "number.fst"]
                    if (d / n).exists()
                ),
                max_num_sentences=1,
            )
        )

        def synth(text, lang, voice):
            result = model.generate(text, sid=int(voice), speed=1.0)
            return np.asarray(result.samples), result.sample_rate

        return model, synth
    if is_chatterbox:
        # Construct from explicit local inventories. Avoid the runtime's hidden
        # S3Tokenizer snapshot download and pin its weights independently.
        from mlx_audio.tts.models.chatterbox.chatterbox import Model
        from mlx_audio.tts.models.chatterbox.config import ModelConfig
        from mlx_audio.tts.models.chatterbox.s3tokenizer import S3TokenizerV2

        config = json.loads((CACHE / checkpoint / "config.json").read_text())
        model = Model(ModelConfig.from_dict(config))
        weights = mx.load(str(CACHE / checkpoint / "model.safetensors"))
        for prefix, component, strict in [
            ("ve.", model.ve, True),
            ("t3.", model.t3, False),
            ("s3gen.", model.s3gen, False),
        ]:
            values = {
                k[len(prefix) :]: v for k, v in weights.items() if k.startswith(prefix)
            }
            if hasattr(component, "sanitize"):
                values = component.sanitize(values)
            component.load_weights(list(values.items()), strict=strict)
        model._s3_tokenizer = S3TokenizerV2("speech_tokenizer_v2_25hz")
        tokenizer_weights = mx.load(str(CACHE / "s3tokenizer" / "model.safetensors"))
        if hasattr(model._s3_tokenizer, "sanitize"):
            tokenizer_weights = model._s3_tokenizer.sanitize(tokenizer_weights)
        model._s3_tokenizer.load_weights(list(tokenizer_weights.items()), strict=False)
        model._init_text_tokenizers(CACHE / checkpoint)
        if name == "chatterbox_fp16":
            # Public parameter conversion; source checkpoint files stay intact.
            model.set_dtype(mx.float16)
            model._s3_tokenizer.set_dtype(mx.float16)
        mx.eval(model.parameters(), model._s3_tokenizer.parameters())
        if name == "chatterbox_fp16":
            from mlx.utils import tree_flatten

            views = {}
            for label, component in [("model", model), ("codec", model._s3_tokenizer)]:
                counts = {}
                for _, value in tree_flatten(component.parameters()):
                    dtype = str(value.dtype)
                    counts[dtype] = counts.get(dtype, 0) + value.nbytes
                    if mx.issubdtype(value.dtype, mx.floating):
                        assert value.dtype == mx.float16
                views[label] = counts
            save(
                OUT / "chatterbox-fp16-precision.json",
                {
                    "source_artifact": "assets/chatterbox.json",
                    "source_files_unchanged": True,
                    "conversion": "Public Module.set_dtype(mx.float16), including codec; runtime conversion only",
                    "parameter_views_bytes": views,
                    "note": "Model and codec views may overlap; do not sum these views. Operation intermediates may still promote to F32 for numerical stability.",
                },
            )
        model.eval()
    else:
        from mlx_audio.tts.utils import load_model

        model = load_model(str(CACHE / ("ming_tts" if name == "ming_fast" else name)))
        if name == "cosyvoice3":
            from mlx_audio.codec.models.s3tokenizer import S3TokenizerV3

            model._s3_tokenizer = S3TokenizerV3("speech_tokenizer_v3")
            model._s3_tokenizer.load_weights(
                list(mx.load(str(CACHE / "s3tokenizer3" / "model.safetensors")).items())
            )
            mx.eval(model._s3_tokenizer.parameters())
            model._s3_tokenizer.eval()
        if name == "kokoro_mlx":
            import unidic
            import unidic_lite

            unidic.DICDIR = unidic_lite.DICDIR
            model.repo_id = str(CACHE / name)
    refs = (
        {r["voice"]: r for r in prepare_references()}
        if is_chatterbox or name == "cosyvoice3"
        else {}
    )

    conditionals = {}
    conditioning_times = {}

    def synth(text, lang, voice):
        mx.random.seed(42)
        kwargs = {"text": text, "verbose": False}
        if name == "kokoro_mlx":
            kwargs.update(
                voice=str(CACHE / name / "voices" / (voice + ".safetensors")),
                lang_code={"en": "a", "zh": "z", "ja": "j", "es": "e", "fr": "f"}[lang],
            )
        elif name in ("ming_tts", "ming_fast"):
            kwargs.update(voice=voice, lang_code=lang, temperature=0.7, max_tokens=4096)
            if name == "ming_fast":
                kwargs["ddpm_steps"] = 6
        else:
            row = refs[voice]
            kwargs.update(ref_audio=str(SAMPLES / row["file_name"]), lang_code=lang)
            if is_chatterbox:
                if voice not in conditionals:
                    prepared_at = time.perf_counter()
                    conditionals[voice] = model.prepare_conditionals(
                        str(SAMPLES / row["file_name"]), 24000, exaggeration=0.1
                    )
                    if name == "chatterbox_fp16":
                        prepared = conditionals[voice]

                        def cast(value):
                            if isinstance(value, mx.array) and mx.issubdtype(
                                value.dtype, mx.floating
                            ):
                                return value.astype(mx.float16)
                            return value

                        for field in fields(prepared.t3):
                            setattr(
                                prepared.t3,
                                field.name,
                                cast(getattr(prepared.t3, field.name)),
                            )
                        prepared.gen = {
                            key: cast(value) for key, value in prepared.gen.items()
                        }
                    conditioning_times[voice] = time.perf_counter() - prepared_at
                kwargs.pop("ref_audio")
                kwargs.update(
                    conds=conditionals[voice],
                    max_new_tokens=1000,
                    temperature=0.8,
                    # Match the cached scalar exactly: the public generator
                    # otherwise replaces rounded F16 emotion conditioning with F32.
                    exaggeration=(
                        float(np.float16(0.1)) if name == "chatterbox_fp16" else 0.1
                    ),
                )
            else:
                from mlx_audio.utils import load_audio

                kwargs.update(
                    ref_text=row["reference"],
                    ref_audio=load_audio(
                        str(SAMPLES / row["file_name"]), sample_rate=24000
                    ),
                    seed=42,
                )
        results = list(model.generate(**kwargs))
        if not results:
            raise ValueError("No generated sentence")
        arrays = [np.asarray(r.audio).reshape(-1) for r in results]
        return np.concatenate(arrays), results[0].sample_rate

    synth.conditioning_times = conditioning_times
    return model, synth


def write_audio(
    name, lang, voice, sentence, text, audio, rate, elapsed, kind="sentence"
):
    if not len(audio) or not np.isfinite(audio).all():
        raise ValueError("Missing or nonfinite audio")
    peak = np.max(np.abs(audio))
    if peak:
        audio = audio * (0.95 / peak)
    path = SAMPLES / f"{name}-{lang}-{voice}-{sentence}-{kind}.wav"
    sf.write(path, audio, rate, subtype="PCM_16")
    return {
        "model": name,
        "language": lang,
        "voice": voice,
        "sentence_index": sentence,
        "kind": kind,
        "reference": text,
        "file_name": path.name,
        "sha256": sha256(path),
        "size_bytes": path.stat().st_size,
        "sample_rate": rate,
        "duration_s": len(audio) / rate,
        "synthesis_s": elapsed,
    }


def brief_sentences(prompts, lang):
    sentences = []
    while True:
        sentence = prompts[len(sentences) % len(prompts)]
        sentences.append(sentence)
        text = (" " if lang not in ("zh", "ja") else "").join(sentences)
        length = (
            len("".join(normalize(text)))
            if lang in ("zh", "ja")
            else len(normalize(text))
        )
        if length >= (552 if lang in ("zh", "ja") else 300):
            return sentences


def sentence_delivery_gaps(traces):
    """Audio becomes playable only after its whole sentence has materialized."""
    elapsed_decode = 0.0
    playback_end = traces[0]["synthesis_s"]
    gaps = []
    for trace in traces:
        elapsed_decode += trace["synthesis_s"]
        gaps.append(max(0.0, elapsed_decode - playback_end))
        playback_end = max(elapsed_decode, playback_end) + trace["audio_s"]
    return gaps


def measure_brief(name, lang, voice, pieces, synth):
    traces, waveforms = [], []
    started = time.perf_counter()
    for i, text in enumerate(pieces):
        before = time.perf_counter()
        audio, rate = synth(text, lang, voice)
        ready = time.perf_counter()
        waveforms.append(audio)
        traces.append(
            {
                "sentence_index": i,
                "reference": text,
                "synthesis_s": ready - before,
                "audio_s": len(audio) / rate,
            }
        )
    total = sum(r["synthesis_s"] for r in traces)
    playback = sum(r["audio_s"] for r in traces)
    gaps = sentence_delivery_gaps(traces)
    reference = ("" if lang in ("zh", "ja") else " ").join(pieces)
    return {
        "language": lang,
        "voice": voice,
        "reference": reference,
        "first_sentence_s": traces[0]["synthesis_s"],
        "first_informative_sentence_ready_s": sum(r["synthesis_s"] for r in traces[:2]),
        "total_s": total,
        "audio_s": playback,
        "rtf": total / playback,
        "sentence_delivery_gap_s": sum(gaps),
        "maximum_sentence_gap_s": max(gaps),
        "wall_s": time.perf_counter() - started,
        "sentences": traces,
        **write_audio(
            name,
            lang,
            voice,
            0,
            reference,
            np.concatenate(waveforms),
            rate,
            total,
            kind="brief",
        ),
    }


def followup_brief_voice(name, lang, voice):
    """Append a measured alternate brief and preserve the original primary data."""
    import mlx.core as mx
    from multilingual_selection import primary_briefs, qualify_voice

    mx.set_cache_limit(128 * 1024**2)
    mx.set_memory_limit(8 * 1024**3)
    path = OUT / "runs" / f"tts-{name}.json"
    output = json.loads(path.read_text())
    assert output["status"] == "complete" and voice in VOICES[name][lang]
    assert not any(
        b["language"] == lang and b["voice"] == voice for b in output["briefs"]
    )
    original = {l: b["voice"] for l, b in primary_briefs(output).items()}
    snapshot = OUT / f"{name}-primary-before-{lang}-followup.json"
    if snapshot.exists():
        if sha256(snapshot) != sha256(path):
            raise ValueError(
                "Preserve the existing follow-up snapshot before another run"
            )
    else:
        snapshot.write_bytes(path.read_bytes())
    back_path = OUT / "runs" / "voice-back-asr.json"
    back = json.loads(back_path.read_text())
    assert back["status"] == "complete"
    basis = {}
    for candidate in VOICES[name][lang]:
        rows = [
            r
            for r in back["samples"]
            if r["model"] == name
            and r["language"] == lang
            and r["voice"] == candidate
            and r["kind"] == "sentence"
        ]
        assert len(rows) == 5
        basis[candidate] = {
            "files": [r["file_name"] for r in rows],
            "accuracy": score(
                [r["reference"] for r in rows], [r["hypothesis"] for r in rows], lang
            ),
        }
    started = time.perf_counter()
    model, synth = load_synth(name)
    load_s = time.perf_counter() - started
    prompts = json.loads((OUT / "prompts.json").read_text())
    started = time.perf_counter()
    synth(prompts[lang][0], lang, voice)
    cold_s = time.perf_counter() - started
    trials = []
    for _ in range(3):
        started = time.perf_counter()
        synth(prompts[lang][0], lang, voice)
        trials.append(time.perf_counter() - started)
    brief = measure_brief(
        name, lang, voice, brief_sentences(prompts[lang], lang), synth
    )
    output["briefs"].append(brief)
    output["warmups"].append(
        {
            "language": lang,
            "voice": voice,
            "cold_first_sentence_s": cold_s,
            "followup": True,
        }
    )
    output["first_sentence_trials"].append(
        {"language": lang, "voice": voice, "seconds": trials, "followup": True}
    )
    output["primary_voices"] = {**original, lang: voice}
    if not qualify_voice(output):
        output["primary_voices"] = original
    output.setdefault("voice_followups", []).append(
        {
            "language": lang,
            "voice": voice,
            "unix": time.time(),
            "nice": os.getpriority(os.PRIO_PROCESS, 0),
            "load_s": load_s,
            "original_primary_voices": original,
            "selected": output["primary_voices"][lang] == voice,
            "original_run_snapshot": snapshot.name,
            "snapshot_sha256": sha256(snapshot),
            "selection_back_asr_sha256_before_followup": sha256(back_path),
            "selection_observations": basis,
            "reason": "The alternate voice improved the five common short-sentence errors; greeting and full brief were remeasured before choosing it. This is a disclosed selection follow-up, not independent held-out quality evidence.",
            "process_peak_rss_bytes": resource.getrusage(
                resource.RUSAGE_SELF
            ).ru_maxrss,
            "metal_peak_bytes": mx.get_peak_memory(),
        }
    )
    save(path, output)
    print(
        "FOLLOWUP",
        name,
        lang,
        voice,
        brief["first_sentence_s"],
        brief["total_s"],
        brief["audio_s"],
        flush=True,
    )


def run(name, languages=None):
    import mlx.core as mx

    mx.set_cache_limit(128 * 1024**2)
    mx.set_memory_limit(8 * 1024**3)
    mx.random.seed(42)
    SAMPLES.mkdir(parents=True, exist_ok=True)
    if SAMPLES.resolve().is_relative_to(ROOT.parent):
        raise ValueError("WAVs must stay outside repository")
    prompts = json.loads((OUT / "prompts.json").read_text())
    path = OUT / "runs" / f"tts-{name}.json"
    output = (
        json.loads(path.read_text())
        if path.exists()
        else {"model": name, "samples": [], "briefs": [], "warmups": []}
    )
    output.update(
        status="running",
        host=platform.platform(),
        nice=os.getpriority(os.PRIO_PROCESS, 0),
        sentence_streaming="Sequential complete-sentence PCM; model stays loaded; no device playback",
        started_unix=time.time(),
    )
    if name in ("ming_tts", "ming_fast"):
        output["generation_config"] = {
            "artifact": "ming_tts",
            "ddpm_steps": 6 if name == "ming_fast" else 10,
            "temperature": 0.7,
            "max_tokens": 4096,
            "seed_per_sentence": 42,
        }
    elif name == "chatterbox_fp16":
        output["generation_config"] = {
            "artifact": "chatterbox",
            "floating_parameter_dtype": "float16",
            "conditioning_float_dtype": "float16",
            "parameter_conversion": "Module.set_dtype",
            "include_codec": True,
            "source_checkpoint_precision": "float32",
            "integer_conditioning": "unchanged token IDs and lengths",
            "temperature": 0.8,
            "max_new_tokens": 1000,
            "requested_exaggeration": 0.1,
            "exaggeration": float(np.float16(0.1)),
            "seed_per_sentence": 42,
        }
    save(path, output)
    started = time.perf_counter()
    try:
        model, synth = load_synth(name)
        output["load_s"] = time.perf_counter() - started
        output["voice_preparation"] = (
            "Chatterbox public conds API cached per synthetic reference voice; the first excluded warmup includes preparation. Other models use the published generate path."
        )
        for lang in languages or VOICES[name]:
            for voice in VOICES[name][lang]:
                started = time.perf_counter()
                synth(prompts[lang][0], lang, voice)
                output["warmups"].append(
                    {
                        "language": lang,
                        "voice": voice,
                        "cold_first_sentence_s": time.perf_counter() - started,
                    }
                )
                output["conditioning_prepare_s"] = dict(
                    getattr(synth, "conditioning_times", {})
                )
                trials = []
                for _ in range(3):
                    started = time.perf_counter()
                    synth(prompts[lang][0], lang, voice)
                    trials.append(time.perf_counter() - started)
                output.setdefault("first_sentence_trials", []).append(
                    {"language": lang, "voice": voice, "seconds": trials}
                )
                save(path, output)
                for i, text in enumerate(prompts[lang]):
                    if any(
                        r["language"] == lang
                        and r["voice"] == voice
                        and r["sentence_index"] == i
                        for r in output["samples"]
                    ):
                        continue
                    started = time.perf_counter()
                    audio, rate = synth(text, lang, voice)
                    elapsed = time.perf_counter() - started
                    output["samples"].append(
                        write_audio(name, lang, voice, i, text, audio, rate, elapsed)
                    )
                    save(path, output)
                    print(name, lang, voice, i, round(elapsed, 3), flush=True)
            if any(r["language"] == lang for r in output["briefs"]):
                continue
            voice = VOICES[name][lang][0]
            pieces = brief_sentences(prompts[lang], lang)
            brief = measure_brief(name, lang, voice, pieces, synth)
            output["briefs"].append(brief)
            save(path, output)
            print("BRIEF", name, lang, brief["total_s"], brief["audio_s"], flush=True)
        output["status"] = (
            "complete"
            if set(VOICES[name]) <= {b["language"] for b in output["briefs"]}
            else "partial"
        )
    except Exception as error:
        output.update(status="failed", error=str(error))
        raise
    finally:
        output["process_peak_rss_bytes"] = resource.getrusage(
            resource.RUSAGE_SELF
        ).ru_maxrss
        output["metal_peak_bytes"] = mx.get_peak_memory()
        save(path, output)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("model", choices=VOICES)
    p.add_argument("--languages", nargs="+")
    p.add_argument("--brief-voice", nargs=2, metavar=("LANGUAGE", "VOICE"))
    a = p.parse_args()
    with model_run():
        if a.brief_voice:
            followup_brief_voice(a.model, *a.brief_voice)
        else:
            run(a.model, a.languages)
