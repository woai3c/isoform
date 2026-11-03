"""Back-ASR in a separate process after all voice synthesis models exit."""

import json
import os
import time
from multilingual_eval import ROOT, OUT, CACHE, save, score, model_run
from multilingual_samples import build_index
from multilingual_tts import SAMPLES


def run():
    import mlx.core as mx

    mx.set_cache_limit(128 * 1024**2)
    mx.set_memory_limit(8 * 1024**3)
    mx.random.seed(42)
    from mlx_qwen3_asr import Session

    session = Session(model=str(CACHE / "qwen17"))
    path = OUT / "runs" / "voice-back-asr.json"
    output = (
        json.loads(path.read_text())
        if path.exists()
        else {
            "verifier": "Qwen3-ASR 1.7B 8bit / mlx-qwen3-asr 0.4.4, auto language",
            "samples": [],
        }
    )
    output["status"] = "running"
    output["nice"] = os.getpriority(os.PRIO_PROCESS, 0)
    output["decode_scope"] = (
        "Automatic low-energy ~30-second chunks, no language hint, maximum 1024 new tokens per chunk; capped results are rejected rather than attributed to TTS."
    )
    seen = {r["file_name"] for r in output["samples"]}
    for row in build_index()["samples"]:
        if row["file_name"] in seen:
            continue
        start = time.perf_counter()
        result = session.transcribe(
            str(SAMPLES / row["file_name"]), return_chunks=True, max_new_tokens=1024
        )
        if getattr(result, "truncated", False):
            raise RuntimeError(f"Verifier hit its decode cap: {row['file_name']}")
        hypothesis = result.text
        output["samples"].append(
            {
                **row,
                "hypothesis": hypothesis,
                "verifier_truncated": False,
                "verifier_chunks": result.chunks,
                "verification_s": time.perf_counter() - start,
                "accuracy": score([row["reference"]], [hypothesis], row["language"]),
            }
        )
        save(path, output)
        print(
            row["file_name"],
            output["samples"][-1]["accuracy"]["error_rate"],
            flush=True,
        )
    output["by_model_language_voice"] = {}
    keys = sorted(
        {(r["model"], r["language"], r["voice"], r["kind"]) for r in output["samples"]}
    )
    for model, lang, voice, kind in keys:
        rows = [
            r
            for r in output["samples"]
            if (r["model"], r["language"], r["voice"], r["kind"])
            == (model, lang, voice, kind)
        ]
        output["by_model_language_voice"]["/".join([model, lang, voice, kind])] = score(
            [r["reference"] for r in rows], [r["hypothesis"] for r in rows], lang
        )
    output["status"] = "complete"
    save(path, output)


if __name__ == "__main__":
    with model_run():
        run()
