"""One-model-per-process wrapper around the existing ASR evaluation harness."""

import argparse
import importlib.metadata
import json
import os
import platform
import resource
import time
from pathlib import Path

import yaml

from evaluate import run_evaluation
from survey_assets import ROOT
from survey_metrics import corpus_scores


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("engine")
    parser.add_argument("--config", default="survey-2026-10/config.yaml")
    args = parser.parse_args()
    os.chdir(ROOT)
    if os.getpriority(os.PRIO_PROCESS, 0) < 10:
        raise SystemExit("Run with nice -n 15 to protect other workers")
    config = yaml.safe_load(Path(args.config).read_text())
    started = time.time()
    result = run_evaluation(
        config,
        [args.engine],
        list(config["datasets"]),
        config["evaluation"]["max_samples_per_dataset"],
    )
    for data in result.get(args.engine, {}).get("datasets", {}).values():
        data["corpus_accuracy"] = corpus_scores(
            [r["reference"] for r in data["samples"]],
            [r["hypothesis"] for r in data["samples"]],
        )
    output = {
        "started_unix": started,
        "elapsed_s": time.time() - started,
        "host": platform.platform(),
        "nice": os.getpriority(os.PRIO_PROCESS, 0),
        "process_peak_rss_bytes": resource.getrusage(resource.RUSAGE_SELF).ru_maxrss,
        "packages": {
            name: importlib.metadata.version(name)
            for name in ["sherpa-onnx", "numpy", "jiwer", "psutil"]
        },
        "results": result,
    }
    try:
        import mlx.core as mx

        output["metal_peak_bytes"] = mx.get_peak_memory()
    except ImportError:
        pass
    dest = ROOT / "survey-2026-10" / "runs"
    dest.mkdir(parents=True, exist_ok=True)
    (dest / f"asr-{args.engine}.json").write_text(
        json.dumps(output, ensure_ascii=False, indent=2) + "\n"
    )
    if "error" in result.get(args.engine, {}) or not result.get(args.engine, {}).get(
        "datasets"
    ):
        raise SystemExit(1)


if __name__ == "__main__":
    main()
