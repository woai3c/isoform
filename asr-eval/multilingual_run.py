"""Resume sequential part-2 evaluation, with explicit downloads and cleanup.

Run under nice -n 15. This never starts an app, plays audio, or reads app data.
The two Python environments must be prepared using the run guide first.
"""

import argparse
import fcntl
import json
import os
import shutil
import subprocess
import time

from multilingual_eval import ROOT, OUT, CACHE, hub, disk_guard
from multilingual_assets import asset

ASR = [
    "whisper",
    "sensevoice",
    "sensevoice_nano",
    "qwen06",
    "funasr_nano",
    "qwen17",
    "granite_ctc",
]
VOICE = ["ming_tts", "melo", "kokoro_mlx", "chatterbox", "cosyvoice3"]


def wait_for_idle_model():
    CACHE.mkdir(parents=True, exist_ok=True)
    with (CACHE / "model.lock").open("a") as lock:
        while True:
            try:
                fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                fcntl.flock(lock, fcntl.LOCK_UN)
                return
            except BlockingIOError:
                time.sleep(10)


def completed(stage, name):
    path = OUT / "runs" / f"{stage}-{name}.json"
    return path.exists() and json.loads(path.read_text()).get("status") == "complete"


def model_assets(stage, name):
    # Reuse the exact part-1 inventories, not floating Hub revisions.
    if name == "ming_fast":
        return model_assets(stage, "ming_tts")
    if name == "chatterbox_fp16":
        return model_assets(stage, "chatterbox")
    if name in [
        "sensevoice",
        "sensevoice_nano",
        "qwen06",
        "funasr_nano",
        "qwen17",
        "ming_tts",
        "melo",
        "kokoro_mlx",
    ]:
        dest = CACHE / name
        if dest.exists() and (OUT / "assets" / f"{name}.json").exists():
            return
        asset(name)
    else:
        repos = {
            "whisper": (
                "mlx-community/whisper-large-v3-turbo",
                "a4aaeec0636e6fef84abdcbe3544cb2bf7e9f6fb",
            ),
            "granite_ctc": (
                "ibm-granite/granite-speech-5.0-470m-turboctc",
                "1dd23f46f6d4e6eae2531982ba2e33220086dd97",
            ),
            "chatterbox": (
                "mlx-community/chatterbox-multilingual-v3",
                "03565773edd72e949572557597af8063bb49a18a",
            ),
            "s3tokenizer": (
                "mlx-community/S3TokenizerV2",
                "e0c9886f0e1c35ae85b1f27277416fb19fc72bec",
            ),
            "cosyvoice3": (
                "mlx-community/Fun-CosyVoice3-0.5B-2512-8bit",
                "177baf277ac75e04e810f7db7f0d9c0ce5a17854",
            ),
        }
        if name == "cosyvoice3":
            inventory = json.loads((OUT / "assets" / "s3tokenizer3.json").read_text())
            hub("s3tokenizer3", inventory["repo"], inventory["revision"])
        repo, revision = repos[name]
        hub(name, repo, revision)
        if name == "chatterbox":
            model_assets(stage, "s3tokenizer")


def run(stage, name):
    wait_for_idle_model()
    if completed(stage, name):
        if name != "qwen17":
            shutil.rmtree(CACHE / name, ignore_errors=True)
        print("Already complete", stage, name, flush=True)
        return
    model_assets(stage, name)
    env = {
        **os.environ,
        "OMP_NUM_THREADS": "2",
        "OPENBLAS_NUM_THREADS": "2",
        "VECLIB_MAXIMUM_THREADS": "2",
        "HF_HOME": str(CACHE / "hf"),
        "HF_HUB_OFFLINE": "1",
        "TRANSFORMERS_OFFLINE": "1",
        "PYTHONDONTWRITEBYTECODE": "1",
    }
    python = ROOT / (".venv-plus" if name == "cosyvoice3" else ".venv") / "bin/python"
    with (OUT / f"{stage}-{name}.log").open("a") as log:
        try:
            result = subprocess.run(
                [str(python), str(ROOT / f"multilingual_{stage}.py"), name],
                env=env,
                stdout=log,
                stderr=subprocess.STDOUT,
                timeout=14400 if stage == "asr" else 3600,
            )
        except subprocess.TimeoutExpired:
            path = OUT / "runs" / f"{stage}-{name}.json"
            data = json.loads(path.read_text()) if path.exists() else {"model": name}
            data.update(status="deadline", deadline_s=14400 if stage == "asr" else 3600)
            from multilingual_eval import save

            save(path, data)
            raise
    print("Finished", stage, name, result.returncode, flush=True)
    if result.returncode:
        raise RuntimeError(
            f"{stage} {name} failed; inspect the saved run and log before retrying"
        )
    if name != "qwen17":
        artifact = {"ming_fast": "ming_tts", "chatterbox_fp16": "chatterbox"}.get(
            name, name
        )
        shutil.rmtree(CACHE / artifact)
    for support in {
        "chatterbox": ["s3tokenizer"],
        "chatterbox_fp16": ["s3tokenizer"],
        "cosyvoice3": ["s3tokenizer3"],
    }.get(name, []):
        shutil.rmtree(CACHE / support, ignore_errors=True)
    disk_guard()


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("stage", choices=["asr", "tts"])
    p.add_argument("models", nargs="*")
    args = p.parse_args()
    if os.getpriority(os.PRIO_PROCESS, 0) < 10:
        raise SystemExit("Run with nice -n 15")
    for name in args.models or (ASR if args.stage == "asr" else VOICE):
        run(args.stage, name)
