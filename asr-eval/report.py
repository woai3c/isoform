"""Generate detailed comparison reports from evaluation results.

Usage:
    python report.py results/summary_*.json
    python report.py results/eval_*.json --detailed
"""

import argparse
import json
import sys
from pathlib import Path

from rich.console import Console
from rich.table import Table

console = Console()


def load_results(path: Path) -> dict:
    with open(path, encoding="utf-8") as f:
        return json.load(f)


def print_accuracy_report(data: dict):
    results = data["results"]

    console.print("\n[bold underline]Accuracy Report[/bold underline]\n")

    all_datasets = set()
    for engine_data in results.values():
        if "datasets" in engine_data:
            all_datasets.update(engine_data["datasets"].keys())

    for ds_name in sorted(all_datasets):
        table = Table(title=f"Accuracy: {ds_name}", show_lines=True)
        table.add_column("Engine", style="cyan", min_width=30)
        table.add_column("WER", justify="right", min_width=10)
        table.add_column("CER", justify="right", min_width=10)
        table.add_column("MER", justify="right", min_width=10)
        table.add_column("Samples", justify="right", min_width=8)

        rows = []
        for engine_name, engine_data in results.items():
            if "error" in engine_data:
                continue
            ds_data = engine_data.get("datasets", {}).get(ds_name)
            if not ds_data:
                continue
            acc = ds_data["accuracy"]
            rows.append(
                (
                    engine_data["engine_info"]["name"],
                    acc["wer"],
                    acc["cer"],
                    acc["mer"],
                    ds_data["num_samples"],
                )
            )

        # Sort by WER (best first)
        rows.sort(key=lambda r: r[1])
        best_wer = rows[0][1] if rows else 0

        for name, w, c, m, n in rows:
            wer_str = f"{w:.2%}"
            if w == best_wer:
                wer_str = f"[bold green]{wer_str}[/bold green]"
            table.add_row(name, wer_str, f"{c:.2%}", f"{m:.2%}", str(n))

        console.print(table)
        console.print()


def print_performance_report(data: dict):
    results = data["results"]

    console.print("\n[bold underline]Performance Report[/bold underline]\n")

    all_datasets = set()
    for engine_data in results.values():
        if "datasets" in engine_data:
            all_datasets.update(engine_data["datasets"].keys())

    for ds_name in sorted(all_datasets):
        table = Table(title=f"Performance: {ds_name}", show_lines=True)
        table.add_column("Engine", style="cyan", min_width=30)
        table.add_column("RTF (mean)", justify="right")
        table.add_column("RTF (p95)", justify="right")
        table.add_column("Latency(ms)", justify="right")
        table.add_column("Lat p95(ms)", justify="right")
        table.add_column("Peak Mem(MB)", justify="right")
        table.add_column("Load(s)", justify="right")

        rows = []
        for engine_name, engine_data in results.items():
            if "error" in engine_data:
                continue
            ds_data = engine_data.get("datasets", {}).get(ds_name)
            if not ds_data:
                continue
            perf = ds_data.get("performance", {})
            if not perf:
                continue
            rows.append(
                (
                    engine_data["engine_info"]["name"],
                    perf.get("rtf_mean", 0),
                    perf.get("rtf_p95", 0),
                    perf.get("latency_mean_ms", 0),
                    perf.get("latency_p95_ms", 0),
                    perf.get("peak_memory_mb", 0),
                    perf.get("model_load_s", 0),
                )
            )

        # Sort by RTF (fastest first)
        rows.sort(key=lambda r: r[1])
        best_rtf = rows[0][1] if rows else 0

        for name, rtf, rtf95, lat, lat95, mem, load_t in rows:
            rtf_str = f"{rtf:.4f}"
            if rtf == best_rtf:
                rtf_str = f"[bold green]{rtf_str}[/bold green]"
            table.add_row(
                name,
                rtf_str,
                f"{rtf95:.4f}",
                f"{lat:.0f}",
                f"{lat95:.0f}",
                f"{mem:.0f}",
                f"{load_t:.1f}",
            )

        console.print(table)
        console.print()


def print_detailed_errors(data: dict, top_n: int = 10):
    results = data["results"]

    console.print("\n[bold underline]Worst Predictions (by WER)[/bold underline]\n")

    for engine_name, engine_data in results.items():
        if "error" in engine_data:
            continue
        console.print(f"[bold cyan]{engine_data['engine_info']['name']}[/bold cyan]")

        for ds_name, ds_data in engine_data.get("datasets", {}).items():
            samples = ds_data.get("samples", [])
            if not samples:
                continue

            worst = sorted(samples, key=lambda s: s.get("wer", 0), reverse=True)[:top_n]
            table = Table(title=f"{ds_name} - Top {top_n} worst")
            table.add_column("ID", width=6)
            table.add_column("WER", justify="right", width=8)
            table.add_column("Reference", max_width=40, overflow="ellipsis")
            table.add_column("Hypothesis", max_width=40, overflow="ellipsis")

            for s in worst:
                table.add_row(
                    str(s["id"]),
                    f"{s['wer']:.2%}",
                    s["reference"][:80],
                    s["hypothesis"][:80],
                )

            console.print(table)
            console.print()


def main():
    parser = argparse.ArgumentParser(description="Generate ASR evaluation reports")
    parser.add_argument("results_file", help="Path to results JSON file")
    parser.add_argument(
        "--detailed", action="store_true", help="Include per-sample error analysis"
    )
    parser.add_argument(
        "--top-errors", type=int, default=10, help="Number of worst predictions to show"
    )
    args = parser.parse_args()

    path = Path(args.results_file)
    if not path.exists():
        console.print(f"[red]File not found: {path}[/red]")
        sys.exit(1)

    data = load_results(path)

    print_accuracy_report(data)
    print_performance_report(data)

    if args.detailed:
        print_detailed_errors(data, args.top_errors)


if __name__ == "__main__":
    main()
