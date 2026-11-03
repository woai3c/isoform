"""Sequential, checkpointed seven-language FLEURS evaluation (100/language)."""

import argparse
import os
import platform
import resource
import time
import json

from multilingual_eval import ROOT, OUT, CACHE, LANGUAGES, model_run, save, score

SUPPORTED = {
    "whisper": list(LANGUAGES),
    "qwen17": list(LANGUAGES),
    "qwen06": list(LANGUAGES),
    "sensevoice": ["en", "zh", "ja", "ko"],
    "sensevoice_nano": ["en", "zh", "ja"],
    "funasr_nano": ["en", "zh", "ja"],
    "granite_ctc": ["en", "ja", "es", "fr", "de"],
    "granite2b": ["en", "ja", "es", "fr", "de"],
}


def load_engine(name):
    d = CACHE / name
    if name == "whisper":
        import mlx_whisper

        def transcribe(path):
            # Automatic language detection for every model: no ground-truth hint.
            return mlx_whisper.transcribe(
                str(path),
                path_or_hf_repo=str(d),
                word_timestamps=False,
                temperature=0.0,
            )["text"].strip()

        return transcribe
    if name == "qwen17":
        from engines.qwen3_asr import Qwen3ASREngine

        engine = Qwen3ASREngine(model=str(d))
    elif name == "qwen06":
        from engines.sherpa_qwen3 import SherpaQwen3Engine

        engine = SherpaQwen3Engine(model_dir=str(d), num_threads=2)
    elif name == "funasr_nano":
        from engines.sherpa_funasr_nano import SherpaFunASRNanoEngine

        engine = SherpaFunASRNanoEngine(model_dir=str(d), num_threads=2)
    elif name in ("sensevoice", "sensevoice_nano"):
        from engines.sherpa_sensevoice import SherpaSenseVoiceEngine

        layout = CACHE / (name + "-layout")
        layout.mkdir(exist_ok=True)
        link = layout / "sense-voice"
        if not link.exists():
            link.symlink_to(d, target_is_directory=True)
        engine = SherpaSenseVoiceEngine(model_dir=str(layout), num_threads=2)
    elif name.startswith("granite"):
        from mlx_audio.stt.utils import load_model

        engine = load_model(str(d))
        return lambda path: engine.generate(
            str(path), max_tokens=512, verbose=False
        ).text.strip()
    else:
        raise ValueError(name)
    engine.load()
    return lambda path: engine.transcribe(str(path)).text


def run(name, languages=None):
    import numpy as np
    import mlx.core as mx

    mx.set_cache_limit(128 * 1024**2)
    mx.set_memory_limit(8 * 1024**3)
    mx.random.seed(42)
    os.chdir(ROOT)
    output_path = OUT / "runs" / f"asr-{name}.json"
    output = (
        json.loads(output_path.read_text())
        if output_path.exists()
        else {"model": name, "datasets": {}}
    )
    output.update(
        {
            "status": "running",
            "host": platform.platform(),
            "nice": os.getpriority(os.PRIO_PROCESS, 0),
            "language_hint": (
                "none; shared-vocabulary greedy CTC"
                if name == "granite_ctc"
                else "none; automatic detection"
            ),
            "started_unix": time.time(),
            "mlx_memory_limit_bytes": 8 * 1024**3,
            "decoder_window_s": 18 if name == "qwen06" else None,
            "word_timestamps": False if name == "whisper" else None,
            "num_threads": (
                2
                if name in ("qwen06", "funasr_nano", "sensevoice", "sensevoice_nano")
                else None
            ),
        }
    )
    save(output_path, output)
    started = time.perf_counter()
    try:
        transcribe = load_engine(name)
        output["load_s"] = time.perf_counter() - started
        for lang in languages or SUPPORTED[name]:
            prior = output["datasets"].get(lang, {})
            if prior.get("status") == "complete":
                continue
            data_manifest = CACHE / "data" / lang / "manifest.json"
            while not data_manifest.exists():
                print("Waiting for public dataset", lang, flush=True)
                time.sleep(30)
            manifest = json.loads(data_manifest.read_text())
            rows = manifest["samples"]
            warmups = []
            for row in rows[:2]:
                start = time.perf_counter()
                transcribe(CACHE / "data" / lang / row["audio_path"])
                warmups.append(time.perf_counter() - start)
            samples = prior.get("samples", [])
            ds = {
                "status": "running",
                "language": lang,
                "warmup_s": warmups,
                "samples": samples,
            }
            output["datasets"][lang] = ds
            for row in rows[len(samples) :]:
                start = time.perf_counter()
                error = None
                try:
                    hypothesis = transcribe(CACHE / "data" / lang / row["audio_path"])
                except Exception as exc:
                    hypothesis, error = "", str(exc)
                elapsed = time.perf_counter() - start
                samples.append(
                    {
                        **row,
                        "hypothesis": hypothesis,
                        "inference_s": elapsed,
                        "error": error,
                    }
                )
                save(output_path, output)
                if len(samples) % 10 == 0:
                    print(name, lang, len(samples), round(elapsed, 3), flush=True)
            ds["accuracy"] = score(
                [r["reference"] for r in samples],
                [r["hypothesis"] for r in samples],
                lang,
            )
            ds["inference_s"] = sum(r["inference_s"] for r in samples)
            ds["audio_s"] = sum(r["duration_s"] for r in samples)
            ds["rtf"] = ds["inference_s"] / ds["audio_s"]
            ds["decode_p95_ms"] = float(
                np.percentile([r["inference_s"] * 1000 for r in samples], 95)
            )
            ds["failed_samples"] = sum(r["error"] is not None for r in samples)
            ds["status"] = "complete"
            save(output_path, output)
            print(name, lang, ds["accuracy"], "RTF", ds["rtf"], flush=True)
        output["status"] = (
            "complete"
            if all(
                output["datasets"].get(lang, {}).get("status") == "complete"
                for lang in SUPPORTED[name]
            )
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
        output["elapsed_s"] = time.time() - output["started_unix"]
        save(output_path, output)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("model", choices=SUPPORTED)
    parser.add_argument("--languages", nargs="+", choices=LANGUAGES)
    args = parser.parse_args()
    with model_run():
        run(args.model, args.languages)
