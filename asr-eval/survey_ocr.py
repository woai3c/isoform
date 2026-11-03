"""OCR accuracy/latency/CPU evaluation on generated public screenshots.

One persistent engine per run, one discarded warmup, one pass of 30 images.
CPU is process CPU seconds / wall seconds, where 100% is one logical core.
"""

import argparse
import json
import os
import platform
import subprocess
import time

import psutil

from survey_assets import ROOT
from survey_metrics import ocr_score


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "engine",
        choices=["app", "accurate", "accurate-zh-first", "fast", "documents", "ppocr6"],
    )
    args = parser.parse_args()
    if os.getpriority(os.PRIO_PROCESS, 0) < 10:
        raise SystemExit("Run with nice -n 15")
    fixtures = ROOT / "survey-2026-10" / "ocr-fixtures"
    manifest = json.loads((fixtures / "manifest.json").read_text())["samples"]
    output = {
        "engine": args.engine,
        "host": platform.platform(),
        "samples": [],
        "nice": os.getpriority(os.PRIO_PROCESS, 0),
    }
    start = time.perf_counter()
    child = None
    if args.engine == "ppocr6":
        from rapidocr import RapidOCR, OCRVersion, ModelType

        engine = RapidOCR(
            params={
                "Det.ocr_version": OCRVersion.PPOCRV6,
                "Det.model_type": ModelType.TINY,
                "Rec.ocr_version": OCRVersion.PPOCRV6,
                "Rec.model_type": ModelType.TINY,
                "Global.use_cls": False,
                "EngineConfig.onnxruntime.intra_op_num_threads": 2,
                "EngineConfig.onnxruntime.inter_op_num_threads": 1,
                "Global.model_root_dir": str(ROOT / ".cache" / "ocr-models"),
            }
        )
        process = psutil.Process()

        def recognize(path):
            begin = time.perf_counter()
            result = engine(str(path))
            return {
                "text": "\n".join(result.txts or []),
                "latency_ms": (time.perf_counter() - begin) * 1000,
            }

    else:
        child = subprocess.Popen(
            [str(ROOT / ".cache" / "vision-eval")],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            text=True,
        )
        process = psutil.Process(child.pid)

        def recognize(path):
            child.stdin.write(
                json.dumps({"path": str(path), "mode": args.engine}) + "\n"
            )
            child.stdin.flush()
            line = child.stdout.readline()
            if not line:
                raise RuntimeError(f"Vision worker exited: {child.poll()}")
            return json.loads(line)

    output["load_s"] = time.perf_counter() - start
    try:
        start = time.perf_counter()
        output["warmup"] = recognize(fixtures / manifest[0]["path"])
        output["warmup_wall_s"] = time.perf_counter() - start
        for row in manifest:
            process.cpu_percent()
            start = time.perf_counter()
            result = recognize(fixtures / row["path"])
            result.update(
                {
                    "id": row["id"],
                    "category": row["category"],
                    "reference": row["reference"],
                    "wall_ms": (time.perf_counter() - start) * 1000,
                    "cpu_percent": process.cpu_percent(),
                    "rss_bytes": process.memory_info().rss,
                }
            )
            result.update(ocr_score(row["reference"], result["text"]))
            output["samples"].append(result)
            print(
                args.engine,
                row["id"],
                round(result["latency_ms"]),
                round(result["character_accuracy"], 4),
                flush=True,
            )
    finally:
        if child:
            child.stdin.close()
            child.wait(timeout=30)
    rows = output["samples"]
    latency = sorted(r["latency_ms"] for r in rows)
    output["summary"] = {
        "count": len(rows),
        "character_accuracy": 1
        - sum(r["edits"] for r in rows) / sum(r["reference_characters"] for r in rows),
        "latency_mean_ms": sum(latency) / len(latency),
        "latency_p95_ms": latency[round(0.95 * (len(latency) - 1))],
        "cpu_percent_mean": sum(r["cpu_percent"] for r in rows) / len(rows),
        "rss_max_bytes": max(r["rss_bytes"] for r in rows),
        "errors": sum(bool(r.get("error")) for r in rows),
    }
    dest = ROOT / "survey-2026-10" / "runs"
    dest.mkdir(parents=True, exist_ok=True)
    (dest / f"ocr-{args.engine}.json").write_text(
        json.dumps(output, ensure_ascii=False, indent=2) + "\n"
    )


if __name__ == "__main__":
    main()
