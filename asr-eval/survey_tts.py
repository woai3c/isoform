"""Sequential TTS pilot: bilingual matching prompts, voices, RTF, 300-word brief.

Synthesis and back-transcription run in separate processes to avoid holding two
models. Never plays samples automatically. WAVs are PCM16 and peak-normalized.
"""

import argparse
import json
import os
import platform
import resource
import time
from pathlib import Path

import numpy as np
import soundfile as sf

from metrics.performance import PerformanceTracker
from survey_assets import CACHE, ROOT, sha256
from survey_metrics import speech_chunks
from survey_samples import DEFAULT_SAMPLES_DIR, write_sample_index


def sherpa_model(model_id):
    import sherpa_onnx as s

    d = CACHE / model_id
    if model_id == "melo":
        vits = s.OfflineTtsVitsModelConfig(
            model=str(d / "model.onnx"),
            lexicon=str(d / "lexicon.txt"),
            tokens=str(d / "tokens.txt"),
        )
        model = s.OfflineTtsModelConfig(vits=vits, num_threads=2)
        fsts = [
            d / name
            for name in ["phone.fst", "date.fst", "number.fst"]
            if (d / name).exists()
        ]
    else:
        multi = model_id == "kokoro11"
        kokoro = s.OfflineTtsKokoroModelConfig(
            model=str(d / ("model.int8.onnx" if multi else "model.onnx")),
            voices=str(d / "voices.bin"),
            tokens=str(d / "tokens.txt"),
            data_dir=str(d / "espeak-ng-data"),
            lexicon=(
                ",".join(str(d / n) for n in ["lexicon-us-en.txt", "lexicon-zh.txt"])
                if multi
                else ""
            ),
        )
        model = s.OfflineTtsModelConfig(kokoro=kokoro, num_threads=2)
        fsts = [
            d / name
            for name in ["phone-zh.fst", "date-zh.fst", "number-zh.fst"]
            if (d / name).exists()
        ]
    tts = s.OfflineTts(
        s.OfflineTtsConfig(
            model=model, rule_fsts=",".join(map(str, fsts)), max_num_sentences=2
        )
    )

    def synth(text, lang, voice):
        result = tts.generate(text, sid=int(voice), speed=1.0)
        return np.asarray(result.samples), result.sample_rate

    return tts, synth


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "model",
        choices=["kokoro019", "melo", "kokoro11", "ming_tts", "kokoro_mlx"],
    )
    parser.add_argument("--en-voice", default="0")
    parser.add_argument("--zh-voice", default="0")
    parser.add_argument("--extra-en-voice")
    parser.add_argument("--extra-zh-voice")
    parser.add_argument("--brief-only", action="store_true")
    parser.add_argument("--samples-dir", type=Path, default=DEFAULT_SAMPLES_DIR)
    args = parser.parse_args()
    if os.getpriority(os.PRIO_PROCESS, 0) < 10:
        raise SystemExit("Run with nice -n 15")
    samples_dir = args.samples_dir.expanduser().resolve()
    if samples_dir.is_relative_to(ROOT.parent):
        raise ValueError(
            "Listening WAVs must be saved outside the evaluation repository"
        )
    samples_dir.mkdir(parents=True, exist_ok=True)
    prompts = json.loads((ROOT / "tts_prompts.json").read_text())
    start = time.perf_counter()
    if args.model in ("ming_tts", "kokoro_mlx"):
        import mlx.core as mx
        from mlx_audio.tts.utils import load_model

        mx.random.seed(42)
        mx.set_cache_limit(128 * 1024 * 1024)
        model = load_model(str(CACHE / args.model))
        if args.model == "kokoro_mlx":
            model.repo_id = str(CACHE / args.model)

        def synth(text, lang, voice):
            mx.random.seed(42)
            results = list(
                model.generate(
                    text=text,
                    voice=(
                        str(CACHE / args.model / "voices" / (voice + ".safetensors"))
                        if args.model == "kokoro_mlx"
                        else voice
                    ),
                    lang_code=(
                        {"en": "a", "zh": "z"}
                        if args.model == "kokoro_mlx"
                        else {"en": "en", "zh": "zh"}
                    )[lang],
                    temperature=0.7,
                    max_tokens=4096,
                    verbose=False,
                )
            )
            arrays = [np.asarray(r.audio) for r in results]
            if not arrays or not sum(len(a) for a in arrays):
                raise ValueError("Empty generated audio")
            return np.concatenate(arrays), results[0].sample_rate

    else:
        model, synth = sherpa_model(args.model)
    output = {
        "status": "running",
        "model": args.model,
        "host": platform.platform(),
        "load_s": time.perf_counter() - start,
        "samples": [],
        "briefs": [],
        "voices": {"en": args.en_voice, "zh": args.zh_voice},
        "nice": os.getpriority(os.PRIO_PROCESS, 0),
    }
    if args.model in ("kokoro019", "kokoro11", "melo"):
        output["num_speakers"] = model.num_speakers
    dest = ROOT / "survey-2026-10" / "runs"
    dest.mkdir(parents=True, exist_ok=True)
    if args.brief_only:
        prior = json.loads((dest / f"tts-{args.model}.json").read_text())
        output["samples"] = prior["samples"]
        diagnostics = ROOT / "survey-2026-10" / "diagnostics"
        diagnostics.mkdir(exist_ok=True)
        backup = diagnostics / f"tts-{args.model}-{int(time.time())}.json"
        backup.write_text(
            json.dumps(
                {
                    "status": "superseded; earlier brief chunking, do not use for recommendations",
                    "run": prior,
                },
                ensure_ascii=False,
                indent=2,
            )
            + "\n"
        )
        output["prior_run"] = str(backup.relative_to(ROOT))
    if args.model in ("ming_tts", "kokoro_mlx"):
        output["mlx_cache_limit_bytes"] = 128 * 1024 * 1024

    def checkpoint():
        (dest / f"tts-{args.model}.json").write_text(
            json.dumps(output, ensure_ascii=False, indent=2) + "\n"
        )

    languages = ["en"] if args.model == "kokoro019" else ["en", "zh"]
    for lang in languages:
        voice = args.en_voice if lang == "en" else args.zh_voice
        start = time.perf_counter()
        synth(prompts[lang][0], lang, voice)
        output.setdefault("warmup_s", {})[lang] = time.perf_counter() - start
        cases = [(i, text, voice) for i, text in enumerate(prompts[lang])]
        extra = args.extra_en_voice if lang == "en" else args.extra_zh_voice
        if extra is not None:
            cases.append((0, prompts[lang][0], extra))
        if args.brief_only:
            cases = []
        for i, text, speaker in cases:
            tracker = PerformanceTracker()
            with tracker.measure(1):
                audio, rate = synth(text, lang, speaker)
            duration = len(audio) / rate
            if duration <= 0:
                raise ValueError("No audio generated")
            measurement = tracker.samples[0]
            measurement.audio_duration_s = duration
            peak = float(np.max(np.abs(audio)))
            if peak:
                audio = audio * (0.95 / peak)
            path = samples_dir / f"{args.model}-{lang}-{speaker}-{i + 1}.wav"
            sf.write(path, audio, rate, subtype="PCM_16")
            row = {
                "path": "tts-samples/" + path.name,
                "lang": lang,
                "voice": speaker,
                "prompt_index": i,
                "reference": text,
                "sample_rate": rate,
                "duration_s": duration,
                "synthesis_s": measurement.processing_time_s,
                "rtf": measurement.rtf,
                "cpu_percent": measurement.cpu_percent,
                "rss_mb": measurement.peak_memory_mb,
                "sha256": sha256(path),
                "size_bytes": path.stat().st_size,
            }
            output["samples"].append(row)
            checkpoint()
            print(args.model, lang, speaker, i, round(row["rtf"], 3), flush=True)
        # Full-brief throughput; do not commit the large WAV to the repo.
        if lang == "en":
            words = (" ".join(prompts["en"]) + " ").split()
            brief = " ".join((words * 10)[:300])
        else:
            brief = "".join(prompts["zh"] * 8)
        tracker = PerformanceTracker()
        chunks = speech_chunks(brief)
        generated = []
        chunk_times = []
        with tracker.measure(1):
            for chunk in chunks:
                started = time.perf_counter()
                audio, rate = synth(chunk, lang, voice)
                if not len(audio):
                    raise ValueError("TTS returned empty audio for a brief chunk")
                chunk_times.append(time.perf_counter() - started)
                generated.append(audio)
        audio = np.concatenate(generated)
        m = tracker.samples[0]
        duration = len(audio) / rate
        output["briefs"].append(
            {
                "lang": lang,
                "text": brief,
                "word_count": len(brief.split()) if lang == "en" else None,
                "characters": len(brief),
                "duration_s": duration,
                "synthesis_s": m.processing_time_s,
                "rtf": m.processing_time_s / duration,
                "rss_mb": m.peak_memory_mb,
                "input_chunks": chunks,
                "characters_synthesized": sum(len(c) for c in chunks),
                "first_chunk_s": chunk_times[0],
                "chunk_synthesis_s": chunk_times,
                "mode": "bounded sentence groups, maximum 200 Latin or 80 Han characters; all input retained",
            }
        )
        brief_dir = CACHE / "briefs"
        brief_dir.mkdir(exist_ok=True)
        sf.write(brief_dir / f"{args.model}-{lang}.wav", audio, rate, subtype="PCM_16")
        checkpoint()
    output["status"] = "complete"
    output["process_peak_rss_bytes"] = resource.getrusage(
        resource.RUSAGE_SELF
    ).ru_maxrss
    if args.model in ("ming_tts", "kokoro_mlx"):
        output["metal_peak_bytes"] = mx.get_peak_memory()
    dest = ROOT / "survey-2026-10" / "runs"
    dest.mkdir(parents=True, exist_ok=True)
    (dest / f"tts-{args.model}.json").write_text(
        json.dumps(output, ensure_ascii=False, indent=2) + "\n"
    )
    write_sample_index()


if __name__ == "__main__":
    main()
