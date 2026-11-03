"""Main ASR evaluation runner.

Usage:
    python evaluate.py                           # Run all engines on all datasets
    python evaluate.py --engines sensevoice      # Run specific engine
    python evaluate.py --datasets fleurs_zh      # Run specific dataset
    python evaluate.py --max-samples 50          # Limit samples per dataset
"""

import argparse
import json
import sys
import time
from datetime import datetime
from pathlib import Path

import yaml
from rich.console import Console
from rich.progress import BarColumn, Progress, TextColumn, TimeRemainingColumn
from rich.table import Table

from data_prep.download import load_manifest
from engines import create_engine
from metrics.accuracy import compute_accuracy_metrics
from metrics.performance import PerformanceTracker

console = Console()


def run_evaluation(
    config: dict,
    engine_names: list[str],
    dataset_names: list[str],
    max_samples: int | None,
) -> dict:
    results = {}
    data_dir = Path(config["evaluation"]["data_dir"])
    warmup_runs = config["evaluation"].get("warmup_runs", 2)

    for engine_name in engine_names:
        engine_cfg = config["engines"][engine_name]
        console.print(f"\n[bold cyan]Engine: {engine_cfg['name']}[/bold cyan]")

        engine = create_engine(
            engine_cfg["type"],
            **{k: v for k, v in engine_cfg.items() if k not in ("name", "type")},
        )
        tracker = PerformanceTracker()

        # Load model and measure load time
        console.print("  Loading model...")
        t0 = time.perf_counter()
        try:
            engine.load()
        except Exception as e:
            console.print(f"  [red]Failed to load engine: {e}[/red]")
            results[engine_name] = {"error": str(e)}
            continue
        tracker.model_load_time_s = time.perf_counter() - t0
        console.print(f"  Model loaded in {tracker.model_load_time_s:.2f}s")

        results[engine_name] = {
            "engine_info": engine.get_model_info(),
            "datasets": {},
        }

        for ds_name in dataset_names:
            ds_cfg = config["datasets"][ds_name]
            manifest_path = data_dir / ds_name / "manifest.tsv"

            if not manifest_path.exists():
                console.print(
                    f"  [yellow]Dataset {ds_name} not downloaded, skipping[/yellow]"
                )
                continue

            samples = load_manifest(manifest_path)
            if max_samples and len(samples) > max_samples:
                samples = samples[:max_samples]

            language = ds_cfg["language"]
            console.print(
                f"\n  [bold]{ds_cfg['name']}[/bold] ({len(samples)} samples, lang={language})"
            )

            # Warmup runs (not counted in metrics)
            if warmup_runs > 0 and len(samples) > 0:
                console.print(f"  Warming up ({warmup_runs} runs)...")
                for i in range(min(warmup_runs, len(samples))):
                    try:
                        engine.transcribe(
                            samples[i]["audio_path"], language_hint=language
                        )
                    except Exception:
                        pass

            # Evaluation runs
            tracker_ds = PerformanceTracker()
            tracker_ds.model_load_time_s = tracker.model_load_time_s
            hypotheses = []
            references = []
            sample_results = []

            with Progress(
                TextColumn("  {task.description}"),
                BarColumn(),
                TextColumn("{task.completed}/{task.total}"),
                TimeRemainingColumn(),
                console=console,
            ) as progress:
                task = progress.add_task("Transcribing", total=len(samples))

                for sample in samples:
                    audio_path = sample["audio_path"]
                    reference = sample["reference"]
                    duration = sample["duration_s"]

                    error = None
                    try:
                        with tracker_ds.measure(duration):
                            result = engine.transcribe(
                                audio_path, language_hint=language
                            )
                        hypothesis = result.text
                    except Exception as e:
                        hypothesis = ""
                        error = str(e)
                        console.print(f"    [red]Error on {audio_path}: {e}[/red]")

                    hypotheses.append(hypothesis)
                    references.append(reference)

                    acc = compute_accuracy_metrics(hypothesis, reference, language)
                    sample_results.append(
                        {
                            "id": sample["id"],
                            "reference": reference,
                            "hypothesis": hypothesis,
                            "duration_s": duration,
                            "error": error,
                            **acc,
                        }
                    )

                    progress.update(task, advance=1)

            # Compute aggregate metrics
            n = len(hypotheses)
            if n > 0:
                avg_wer = sum(r["wer"] for r in sample_results) / n
                avg_cer = sum(r["cer"] for r in sample_results) / n
                avg_mer = sum(r["mer"] for r in sample_results) / n
            else:
                avg_wer = avg_cer = avg_mer = 0.0

            perf_summary = tracker_ds.summary()

            ds_result = {
                "dataset": ds_cfg["name"],
                "language": language,
                "num_samples": n,
                "failed_samples": sum(r["error"] is not None for r in sample_results),
                "accuracy": {
                    "wer": round(avg_wer, 4),
                    "cer": round(avg_cer, 4),
                    "mer": round(avg_mer, 4),
                },
                "performance": perf_summary,
                "samples": sample_results,
            }
            results[engine_name]["datasets"][ds_name] = ds_result

            # Print summary
            console.print(
                f"    WER: {avg_wer:.2%}  CER: {avg_cer:.2%}  MER: {avg_mer:.2%}"
            )
            if perf_summary:
                console.print(
                    f"    RTF: {perf_summary['rtf_mean']:.4f}  "
                    f"Latency(mean): {perf_summary['latency_mean_ms']:.0f}ms  "
                    f"Memory(peak): {perf_summary['peak_memory_mb']:.0f}MB"
                )

        engine.unload()

    return results


def save_results(results: dict, results_dir: Path):
    results_dir.mkdir(parents=True, exist_ok=True)
    timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
    out_path = results_dir / f"eval_{timestamp}.json"

    output = {
        "timestamp": timestamp,
        "results": results,
    }

    # Write full results (with per-sample data)
    with open(out_path, "w", encoding="utf-8") as f:
        json.dump(output, f, indent=2, ensure_ascii=False)
    console.print(f"\n[bold green]Results saved to {out_path}[/bold green]")

    # Write summary (without per-sample data)
    summary = {
        "timestamp": timestamp,
        "results": {},
    }
    for engine_name, engine_data in results.items():
        if "error" in engine_data:
            summary["results"][engine_name] = engine_data
            continue
        summary["results"][engine_name] = {
            "engine_info": engine_data["engine_info"],
            "datasets": {},
        }
        for ds_name, ds_data in engine_data.get("datasets", {}).items():
            summary["results"][engine_name]["datasets"][ds_name] = {
                k: v for k, v in ds_data.items() if k != "samples"
            }

    summary_path = results_dir / f"summary_{timestamp}.json"
    with open(summary_path, "w", encoding="utf-8") as f:
        json.dump(summary, f, indent=2, ensure_ascii=False)
    console.print(f"[bold green]Summary saved to {summary_path}[/bold green]")

    return out_path, summary_path


def print_comparison_table(results: dict):
    """Print a comparison table across all engines and datasets."""
    console.print("\n[bold]Comparison Summary[/bold]\n")

    # Collect all datasets
    all_datasets = set()
    for engine_data in results.values():
        if "datasets" in engine_data:
            all_datasets.update(engine_data["datasets"].keys())

    for ds_name in sorted(all_datasets):
        table = Table(title=ds_name)
        table.add_column("Engine", style="cyan")
        table.add_column("WER", justify="right")
        table.add_column("CER", justify="right")
        table.add_column("RTF", justify="right")
        table.add_column("Latency(ms)", justify="right")
        table.add_column("Memory(MB)", justify="right")
        table.add_column("Load(s)", justify="right")

        for engine_name, engine_data in results.items():
            if "error" in engine_data:
                table.add_row(engine_name, "ERROR", "-", "-", "-", "-", "-")
                continue
            ds_data = engine_data.get("datasets", {}).get(ds_name)
            if not ds_data:
                continue

            acc = ds_data["accuracy"]
            perf = ds_data.get("performance", {})
            table.add_row(
                engine_data["engine_info"]["name"],
                f"{acc['wer']:.2%}",
                f"{acc['cer']:.2%}",
                f"{perf.get('rtf_mean', 0):.4f}",
                f"{perf.get('latency_mean_ms', 0):.0f}",
                f"{perf.get('peak_memory_mb', 0):.0f}",
                f"{perf.get('model_load_s', 0):.1f}",
            )

        console.print(table)
        console.print()


def main():
    parser = argparse.ArgumentParser(description="ASR Evaluation Framework")
    parser.add_argument("--config", default="config.yaml", help="Config file path")
    parser.add_argument(
        "--engines", nargs="*", help="Engines to evaluate (default: all)"
    )
    parser.add_argument("--datasets", nargs="*", help="Datasets to use (default: all)")
    parser.add_argument(
        "--max-samples", type=int, default=None, help="Max samples per dataset"
    )
    args = parser.parse_args()

    config_path = Path(args.config)
    if not config_path.exists():
        console.print(f"[red]Config file not found: {config_path}[/red]")
        sys.exit(1)

    with open(config_path, encoding="utf-8") as f:
        config = yaml.safe_load(f)

    engine_names = args.engines or list(config["engines"].keys())
    dataset_names = args.datasets or list(config["datasets"].keys())

    if args.max_samples:
        config["evaluation"]["max_samples_per_dataset"] = args.max_samples

    # Validate engines exist
    for name in engine_names:
        if name not in config["engines"]:
            console.print(
                f"[red]Unknown engine: {name}. Available: {list(config['engines'].keys())}[/red]"
            )
            sys.exit(1)

    console.print("[bold]ASR Evaluation Framework[/bold]")
    console.print(f"Engines: {', '.join(engine_names)}")
    console.print(f"Datasets: {', '.join(dataset_names)}")
    if args.max_samples:
        console.print(f"Max samples per dataset: {args.max_samples}")

    results = run_evaluation(config, engine_names, dataset_names, args.max_samples)

    results_dir = Path(config["evaluation"]["results_dir"])
    save_results(results, results_dir)
    print_comparison_table(results)


if __name__ == "__main__":
    main()
