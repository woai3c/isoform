"""Download a bounded, reproducible public speech pilot (30 utterances/split).

Uses the first 30 entries in published order. This is a pilot, not a new SOTA
benchmark. No application recordings or private directories are inspected.
"""

import argparse
import csv
import io
import json
import re
import hashlib
import tarfile
import urllib.request
from pathlib import Path

import pyarrow.parquet as pq
import soundfile as sf

from survey_assets import CACHE, ROOT, download, sha256


def write_dataset(name, rows, source, licence, selection="first N in published order"):
    dest = CACHE / "data" / name
    dest.mkdir(parents=True, exist_ok=True)
    manifest = []
    for sample_id, reference, audio in rows:
        samples, rate = sf.read(io.BytesIO(audio), dtype="float32")
        path = dest / f"{sample_id}.wav"
        sf.write(path, samples, rate, subtype="PCM_16")
        manifest.append(
            {
                "id": str(sample_id),
                "audio_path": path.name,
                "reference": reference.replace("\t", " ").replace("\n", " "),
                "duration_s": len(samples) / rate,
                "sha256": sha256(path),
            }
        )
    with (dest / "manifest.tsv").open("w") as f:
        writer = csv.DictWriter(f, fieldnames=list(manifest[0]), delimiter="\t")
        writer.writeheader()
        writer.writerows(manifest)
    out = ROOT / "survey-2026-10" / "data"
    out.mkdir(parents=True, exist_ok=True)
    (out / f"{name}.json").write_text(
        json.dumps(
            {
                "source": source,
                "licence": licence,
                "selection": selection,
                "samples": manifest,
            },
            ensure_ascii=False,
            indent=2,
        )
        + "\n"
    )
    print(name, len(manifest), flush=True)


def prepare(count, mixed_only=False):
    repo = "CAiRE/ASCEND"
    revision = json.load(
        urllib.request.urlopen(f"https://huggingface.co/api/datasets/{repo}")
    )["sha"]
    url = f"https://huggingface.co/datasets/{repo}/resolve/{revision}/main/test-00000-of-00001.parquet"
    path = CACHE / "ascend-test.parquet"
    download(url, path)
    all_rows = pq.read_table(path).to_pylist()
    mixed = [
        r
        for r in all_rows
        if re.search(r"[\u3400-\u9fff]", r["transcription"])
        and re.search(r"[A-Za-z]", r["transcription"])
    ]
    mixed.sort(key=lambda r: hashlib.sha256(str(r["id"]).encode()).hexdigest())
    write_dataset(
        "ascend_mixed",
        [
            (str(r["id"]), r["transcription"], r["audio"]["bytes"])
            for r in mixed[:count]
        ],
        url,
        "CC-BY-SA-4.0",
        "lowest SHA-256(id) among test utterances containing both Han and Latin script",
    )
    if mixed_only:
        path.unlink()
        return
    table = all_rows[:count]
    print("ASCEND columns", list(table[0]), flush=True)
    rows = [
        (str(i), r.get("transcription") or r.get("text"), r["audio"]["bytes"])
        for i, r in enumerate(table)
    ]
    write_dataset("ascend_test", rows, url, "CC-BY-SA-4.0")
    path.unlink()

    repo = "google/fleurs"
    revision = json.load(
        urllib.request.urlopen(f"https://huggingface.co/api/datasets/{repo}")
    )["sha"]
    base = f"https://huggingface.co/datasets/{repo}/resolve/{revision}/data/cmn_hans_cn"
    text = urllib.request.urlopen(base + "/test.tsv").read().decode()
    table = list(csv.reader(io.StringIO(text), delimiter="\t"))[:count]
    wanted = {r[1]: (r[0], r[3]) for r in table}
    rows = []
    # Stream the archive without retaining 525 MB after the selection is read.
    with urllib.request.urlopen(
        base + "/audio/test.tar.gz", timeout=60
    ) as source, tarfile.open(fileobj=source, mode="r|gz") as tar:
        for member in tar:
            name = Path(member.name).name
            if name in wanted:
                sid, ref = wanted.pop(name)
                rows.append(
                    (
                        sid + "-" + name.removesuffix(".wav"),
                        ref,
                        tar.extractfile(member).read(),
                    )
                )
                if not wanted:
                    break
    if wanted:
        raise ValueError(f"Missing FLEURS audio: {list(wanted)}")
    write_dataset("fleurs_zh", rows, base, "CC-BY-4.0")

    for config, name in [
        ("clean", "librispeech_test_clean"),
        ("other", "librispeech_test_other"),
    ]:
        endpoint = f"https://datasets-server.huggingface.co/rows?dataset=openslr/librispeech_asr&config={config}&split=test&offset=0&length={count}"
        table = json.load(urllib.request.urlopen(endpoint, timeout=60))["rows"]
        rows = [
            (
                r["row"]["id"],
                r["row"]["text"],
                urllib.request.urlopen(r["row"]["audio"][0]["src"], timeout=60).read(),
            )
            for r in table
        ]
        write_dataset(name, rows, endpoint, "CC-BY-4.0")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--count", type=int, default=30)
    parser.add_argument("--mixed-only", action="store_true")
    args = parser.parse_args()
    prepare(args.count, args.mixed_only)
