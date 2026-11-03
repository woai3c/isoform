"""Reuse pinned part-1 artifacts under the stricter part-2 disk guard."""

import argparse
import fcntl
import json
import shutil
import tarfile
from pathlib import Path
from multilingual_eval import ROOT, OUT, CACHE, download, disk_guard, hub, save, sha256


def asset(name):
    CACHE.mkdir(parents=True, exist_ok=True)
    with (CACHE / (name + ".download.lock")).open("a") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        return _asset(name)


def _asset(name):
    from pathlib import Path

    old = json.loads((ROOT / "survey-2026-10" / "assets" / f"{name}.json").read_text())
    dest = CACHE / name
    if dest.exists() and all(
        (dest / file["path"]).is_file()
        and (dest / file["path"]).stat().st_size == file["size_bytes"]
        and sha256(dest / file["path"]) == file["sha256"]
        for file in old["files"]
    ):
        save(OUT / "assets" / f"{name}.json", old)
        return
    if "transport" not in old:
        return hub(
            name,
            old["repo"],
            old["revision"],
            include=lambda n: n in {f["path"] for f in old["files"]},
        )
    transport = old["transport"]
    disk_guard(transport["size_bytes"] + sum(f["size_bytes"] for f in old["files"]))
    archive = CACHE / transport["path"]
    download(transport["url"], archive, transport["size_bytes"], transport["sha256"])
    dest = CACHE / name
    dest.mkdir(exist_ok=True)
    with tarfile.open(archive) as tar:
        for member in tar:
            relative = Path(*Path(member.name).parts[1:])
            target = (dest / relative).resolve()
            if not target.is_relative_to(dest.resolve()) or not (
                member.isdir() or member.isfile()
            ):
                raise ValueError(f"Unsafe archive entry: {member.name}")
            if member.isdir():
                target.mkdir(parents=True, exist_ok=True)
            else:
                target.parent.mkdir(parents=True, exist_ok=True)
                with tar.extractfile(member) as source, target.open("wb") as output:
                    shutil.copyfileobj(source, output)
    for file in old["files"]:
        path = dest / file["path"]
        if path.stat().st_size != file["size_bytes"] or sha256(path) != file["sha256"]:
            raise ValueError(f"Extracted artifact mismatch: {path}")
    archive.unlink()
    save(OUT / "assets" / f"{name}.json", old)
    print("Ready", name, flush=True)


if __name__ == "__main__":
    p = argparse.ArgumentParser(description=__doc__)
    p.add_argument("model")
    asset(p.parse_args().model)
