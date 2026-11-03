"""Download and prepare evaluation datasets from HuggingFace."""

import argparse
import os
import sys
from pathlib import Path

import soundfile as sf
import yaml
from rich.console import Console
from rich.progress import Progress, SpinnerColumn, TextColumn

console = Console()

DATASET_HANDLERS = {}


def handler(name):
    def decorator(fn):
        DATASET_HANDLERS[name] = fn
        return fn

    return decorator


def _sanitize_ref(text) -> str:
    return (text or "").replace("\t", " ").replace("\n", " ").strip()


@handler("librispeech_test_clean")
def download_librispeech_clean(cfg: dict, data_dir: Path):
    return _download_librispeech(cfg, data_dir, cfg["split"], "librispeech_test_clean")


@handler("librispeech_test_other")
def download_librispeech_other(cfg: dict, data_dir: Path):
    return _download_librispeech(cfg, data_dir, cfg["split"], "librispeech_test_other")


def _download_librispeech(cfg: dict, data_dir: Path, split: str, out_name: str):
    from datasets import load_dataset

    out_dir = data_dir / out_name
    manifest = out_dir / "manifest.tsv"
    if manifest.exists():
        console.print(f"  [dim]Already cached: {out_dir}[/dim]")
        return _count_lines(manifest)

    out_dir.mkdir(parents=True, exist_ok=True)
    ds = load_dataset(
        cfg["source"], split=split, streaming=True, cache_dir=str(data_dir / "cache")
    )

    count = 0
    with open(manifest, "w", encoding="utf-8") as f:
        f.write("id\taudio_path\treference\tduration_s\n")
        for i, sample in enumerate(ds):
            audio = sample["audio"]
            wav_path = out_dir / f"{i:06d}.wav"
            sf.write(str(wav_path), audio["array"], audio["sampling_rate"])
            ref = _sanitize_ref(sample.get("text"))
            duration = len(audio["array"]) / audio["sampling_rate"]
            f.write(f"{i}\t{wav_path.name}\t{ref}\t{duration:.3f}\n")
            count += 1

    return count


@handler("fleurs_zh")
def download_fleurs_zh(cfg: dict, data_dir: Path):
    return _download_fleurs(
        cfg, data_dir, cfg.get("subset", "cmn_hans_cn"), "fleurs_zh"
    )


def _download_fleurs(cfg: dict, data_dir: Path, subset: str, out_name: str):
    from datasets import load_dataset

    out_dir = data_dir / out_name
    manifest = out_dir / "manifest.tsv"
    if manifest.exists():
        console.print(f"  [dim]Already cached: {out_dir}[/dim]")
        return _count_lines(manifest)

    out_dir.mkdir(parents=True, exist_ok=True)
    ds = load_dataset(
        cfg["source"], subset, split=cfg["split"], cache_dir=str(data_dir / "cache")
    )

    with open(manifest, "w", encoding="utf-8") as f:
        f.write("id\taudio_path\treference\tduration_s\n")
        for i, sample in enumerate(ds):
            audio = sample["audio"]
            wav_path = out_dir / f"{i:06d}.wav"
            sf.write(str(wav_path), audio["array"], audio["sampling_rate"])
            ref = _sanitize_ref(sample.get("transcription"))
            duration = len(audio["array"]) / audio["sampling_rate"]
            f.write(f"{i}\t{wav_path.name}\t{ref}\t{duration:.3f}\n")

    return len(ds)


@handler("ascend_test")
def download_ascend(cfg: dict, data_dir: Path):
    from datasets import load_dataset

    out_dir = data_dir / "ascend_test"
    manifest = out_dir / "manifest.tsv"
    if manifest.exists():
        console.print(f"  [dim]Already cached: {out_dir}[/dim]")
        return _count_lines(manifest)

    out_dir.mkdir(parents=True, exist_ok=True)
    ds = load_dataset(
        cfg["source"], split=cfg["split"], cache_dir=str(data_dir / "cache")
    )

    with open(manifest, "w", encoding="utf-8") as f:
        f.write("id\taudio_path\treference\tduration_s\n")
        for i, sample in enumerate(ds):
            audio = sample["audio"]
            wav_path = out_dir / f"{i:06d}.wav"
            sf.write(str(wav_path), audio["array"], audio["sampling_rate"])
            ref = _sanitize_ref(sample.get("transcription") or sample.get("text"))
            duration = len(audio["array"]) / audio["sampling_rate"]
            f.write(f"{i}\t{wav_path.name}\t{ref}\t{duration:.3f}\n")

    return len(ds)


def _count_lines(manifest: Path) -> int:
    with open(manifest, encoding="utf-8") as f:
        return sum(1 for _ in f) - 1  # exclude header


def load_manifest(manifest_path: Path) -> list[dict]:
    """Load a manifest.tsv into a list of dicts."""
    samples = []
    with open(manifest_path, encoding="utf-8") as f:
        header = f.readline().strip().split("\t")
        for line in f:
            parts = line.strip().split("\t")
            row = dict(zip(header, parts))
            row["audio_path"] = str(manifest_path.parent / row["audio_path"])
            row["duration_s"] = float(row["duration_s"])
            samples.append(row)
    return samples


def main():
    parser = argparse.ArgumentParser(description="Download ASR evaluation datasets")
    parser.add_argument("--config", default="config.yaml", help="Config file path")
    parser.add_argument(
        "--datasets", nargs="*", help="Specific datasets to download (default: all)"
    )
    args = parser.parse_args()

    with open(args.config, encoding="utf-8") as f:
        config = yaml.safe_load(f)

    data_dir = Path(config["evaluation"]["data_dir"])
    data_dir.mkdir(parents=True, exist_ok=True)

    targets = args.datasets or list(config["datasets"].keys())

    console.print("[bold]Downloading ASR evaluation datasets...[/bold]\n")

    with Progress(
        SpinnerColumn(), TextColumn("{task.description}"), console=console
    ) as progress:
        for ds_name in targets:
            if ds_name not in config["datasets"]:
                console.print(f"  [red]Unknown dataset: {ds_name}[/red]")
                continue

            ds_cfg = config["datasets"][ds_name]
            task = progress.add_task(f"Downloading {ds_cfg['name']}...")

            if ds_name not in DATASET_HANDLERS:
                console.print(f"  [yellow]No handler for {ds_name}, skipping[/yellow]")
                progress.update(task, completed=True)
                continue

            try:
                count = DATASET_HANDLERS[ds_name](ds_cfg, data_dir)
                progress.update(task, completed=True)
                console.print(f"  [green]{ds_cfg['name']}: {count} samples[/green]")
            except Exception as e:
                progress.update(task, completed=True)
                console.print(f"  [red]{ds_cfg['name']}: FAILED - {e}[/red]")
                if "--debug" in sys.argv:
                    raise

    console.print("\n[bold green]Dataset preparation complete.[/bold green]")


if __name__ == "__main__":
    main()
