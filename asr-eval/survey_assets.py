"""Pinned public downloads for the October survey; never uses application data.

Run from asr-eval: nice -n 15 .venv/bin/python survey_assets.py <asset-id>
The generated inventory records exact transport bytes as well as extracted files.
"""

import argparse
import hashlib
import json
import shutil
import tarfile
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent
CACHE = ROOT / ".cache" / "october"
RAW = ROOT / "survey-2026-10" / "raw"
ASSETS = {
    "sensevoice": (
        "asr-models",
        "sherpa-onnx-sense-voice-zh-en-ja-ko-yue-int8-2024-07-17.tar.bz2",
    ),
    "sensevoice_nano": (
        "asr-models",
        "sherpa-onnx-sense-voice-funasr-nano-int8-2025-12-17.tar.bz2",
    ),
    "qwen06": ("asr-models", "sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25.tar.bz2"),
    "funasr_nano": ("asr-models", "sherpa-onnx-funasr-nano-int8-2025-12-30.tar.bz2"),
    "kokoro019": ("tts-models", "kokoro-en-v0_19.tar.bz2"),
    "melo": ("tts-models", "vits-melo-tts-zh_en.tar.bz2"),
    "kokoro11": ("tts-models", "kokoro-int8-multi-lang-v1_1.tar.bz2"),
}


def sha256(path):
    h = hashlib.sha256()
    with Path(path).open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def download(url, path, expected_sha=None):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    if not path.exists():
        if shutil.disk_usage(path.parent).free < 512 * 1024 * 1024:
            raise OSError("Less than 512 MiB free; defer this eval download")
        temp = path.with_suffix(path.suffix + ".partial")
        for attempt in range(3):
            try:
                with urllib.request.urlopen(url, timeout=60) as source, temp.open(
                    "wb"
                ) as dest:
                    shutil.copyfileobj(source, dest, length=1024 * 1024)
                temp.replace(path)
                break
            except Exception:
                temp.unlink(missing_ok=True)
                if attempt == 2:
                    raise
    digest = sha256(path)
    if expected_sha and digest != expected_sha:
        raise ValueError(f"SHA-256 mismatch: {path}")
    return {
        "path": path.name,
        "size_bytes": path.stat().st_size,
        "sha256": digest,
        "url": url,
    }


def asset(asset_id):
    tag, name = ASSETS[asset_id]
    metadata_path = RAW / f"{tag}.json"
    if not metadata_path.exists():
        RAW.mkdir(parents=True, exist_ok=True)
        metadata = json.load(
            urllib.request.urlopen(
                f"https://api.github.com/repos/k2-fsa/sherpa-onnx/releases/tags/{tag}"
            )
        )
        metadata_path.write_text(json.dumps(metadata, indent=2) + "\n")
    metadata = json.loads(metadata_path.read_text())
    upstream = next(x for x in metadata["assets"] if x["name"] == name)
    digest = (upstream.get("digest") or "").removeprefix("sha256:") or None
    if asset_id == "melo":
        digest = "e58351ed7149f290a54534538badd4077cdbe6fddc964b24d0bee870415d1514"
    archive = CACHE / name
    print(f"Downloading {asset_id}: {upstream['size']} bytes", flush=True)
    transport = download(upstream["browser_download_url"], archive, digest)
    if transport["size_bytes"] != upstream["size"]:
        raise ValueError("Download size differs from release metadata")
    dest = CACHE / asset_id
    dest.mkdir(parents=True, exist_ok=True)
    with tarfile.open(archive) as tar:
        # Only regular files/directories inside one root. Reject links/devices.
        for member in tar:
            if not (member.isfile() or member.isdir()):
                raise ValueError(f"Unsupported archive entry: {member.name}")
            parts = Path(member.name).parts
            relative = Path(*parts[1:])
            target = (dest / relative).resolve()
            if not target.is_relative_to(dest.resolve()):
                raise ValueError(f"Unsafe archive path: {member.name}")
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with tar.extractfile(member) as source, target.open("wb") as out:
                    shutil.copyfileobj(source, out)
    inventory = {
        "id": asset_id,
        "release_asset_created_at": upstream["created_at"],
        "transport": transport,
        "files": [
            {
                "path": str(p.relative_to(dest)),
                "size_bytes": p.stat().st_size,
                "sha256": sha256(p),
            }
            for p in sorted(dest.rglob("*"))
            if p.is_file()
        ],
    }
    out = ROOT / "survey-2026-10" / "assets"
    out.mkdir(parents=True, exist_ok=True)
    (out / f"{asset_id}.json").write_text(json.dumps(inventory, indent=2) + "\n")
    archive.unlink()
    print(f"Ready: {dest}", flush=True)


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("asset", choices=ASSETS)
    args = parser.parse_args()
    asset(args.asset)
