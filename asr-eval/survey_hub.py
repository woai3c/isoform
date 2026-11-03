"""Pin a public Hugging Face model and download only its runtime artifacts."""

import argparse
import json
import urllib.request
import shutil
from pathlib import Path

from survey_assets import ROOT, CACHE, RAW, download

MODELS = {
    "kokoro_mlx": "mlx-community/Kokoro-82M-bf16",
    "funasr_nano": "csukuangfj/sherpa-onnx-funasr-nano-int8-2025-12-30",
    "qwen17": "mlx-community/Qwen3-ASR-1.7B-8bit",
    "ming_tts": "mlx-community/Ming-3.0-tts-0.5B-8bit",
    "mimo": "mlx-community/MiMo-V2.5-ASR-MLX-4bit",
    "mimo_tokenizer": "mlx-community/MiMo-Audio-Tokenizer",
}


def hub(model_id):
    repo = MODELS[model_id]
    inventory_path = ROOT / "survey-2026-10" / "assets" / f"{model_id}.json"
    revision_path = ""
    if inventory_path.exists():
        prior = json.loads(inventory_path.read_text())
        if prior.get("repo") == repo and prior.get("revision"):
            revision_path = "/revision/" + prior["revision"]
    data = json.load(
        urllib.request.urlopen(
            f"https://huggingface.co/api/models/{repo}{revision_path}?blobs=true",
            timeout=60,
        )
    )
    RAW.mkdir(parents=True, exist_ok=True)
    (RAW / (repo.replace("/", "--") + ".json")).write_text(
        json.dumps(data, indent=2) + "\n"
    )
    revision = data["sha"]
    records = []
    for file in data["siblings"]:
        name = file["rfilename"]
        if name.startswith((".", "samples/", "test_wavs/")) or name.endswith(
            (".md", ".pdf", ".png", ".jpg", ".pt")
        ):
            continue
        path = CACHE / model_id / name
        if not path.resolve().is_relative_to((CACHE / model_id).resolve()):
            raise ValueError(name)
        expected = (file.get("lfs") or {}).get("sha256")
        if (
            not path.exists()
            and shutil.disk_usage(CACHE).free < file["size"] + 512 * 1024 * 1024
        ):
            raise OSError(
                f"Insufficient eval disk headroom for {name}; require model bytes plus 512 MiB"
            )
        print(model_id, name, file.get("size"), flush=True)
        info = download(
            f"https://huggingface.co/{repo}/resolve/{revision}/{name}", path, expected
        )
        info["path"] = name
        records.append(info)
    dest = ROOT / "survey-2026-10" / "assets"
    dest.mkdir(parents=True, exist_ok=True)
    (dest / f"{model_id}.json").write_text(
        json.dumps(
            {"id": model_id, "repo": repo, "revision": revision, "files": records},
            indent=2,
        )
        + "\n"
    )
    if model_id == "mimo_tokenizer":
        sibling = CACHE / "MiMo-Audio-Tokenizer"
        if not sibling.exists():
            sibling.symlink_to("mimo_tokenizer", target_is_directory=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("model", choices=MODELS)
    hub(parser.parse_args().model)
