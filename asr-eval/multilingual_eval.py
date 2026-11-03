"""Part-2 public evaluation utilities; isolated downloads, disk guard and scoring.

All model processes require nice >= 10 and share an exclusive advisory lock.
No application configuration, model cache or recordings are read.
"""

import argparse
import contextlib
import csv
import fcntl
import hashlib
import io
import json
import os
import shutil
import subprocess
import tarfile
import time
import unicodedata
import urllib.request
from pathlib import Path

ROOT = Path(__file__).resolve().parent
OUT = ROOT / "survey-2026-10-multilingual"
CACHE = ROOT / ".cache" / "multilingual"
LANGUAGES = {
    "en": "en_us",
    "zh": "cmn_hans_cn",
    "ja": "ja_jp",
    "ko": "ko_kr",
    "es": "es_419",
    "fr": "fr_fr",
    "de": "de_de",
}
RESERVE = 16 * 1024**3


def save(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    temp = path.with_suffix(path.suffix + ".partial")
    temp.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n")
    temp.replace(path)


def sha256(path):
    digest = hashlib.sha256()
    with Path(path).open("rb") as source:
        for chunk in iter(lambda: source.read(1024**2), b""):
            digest.update(chunk)
    return digest.hexdigest()


def disk_guard(expected=0):
    CACHE.mkdir(parents=True, exist_ok=True)
    df = subprocess.check_output(["df", "-k", str(CACHE)], text=True)
    free = shutil.disk_usage(CACHE).free
    with (CACHE / "disk-checks.jsonl").open("a") as log:
        log.write(
            json.dumps(
                {
                    "unix": time.time(),
                    "expected_bytes": expected,
                    "free_bytes": free,
                    "df": df,
                }
            )
            + "\n"
        )
    if free - expected < RESERVE:
        raise OSError(
            f"Download deferred: {free} free - {expected} bytes must exceed {RESERVE} reserve"
        )
    return free


def download(url, path, expected_size, expected_hash=None):
    path.parent.mkdir(parents=True, exist_ok=True)
    if path.exists():
        if path.stat().st_size != expected_size or (
            expected_hash and sha256(path) != expected_hash
        ):
            raise ValueError(f"Existing file mismatch: {path}")
        return
    disk_guard(expected_size)
    temp = path.with_suffix(path.suffix + ".partial")
    try:
        for attempt in range(3):
            try:
                with urllib.request.urlopen(url, timeout=90) as source, temp.open(
                    "wb"
                ) as target:
                    while block := source.read(1024**2):
                        if shutil.disk_usage(CACHE).free < RESERVE:
                            raise OSError("Disk guard stopped a download below reserve")
                        target.write(block)
                if temp.stat().st_size != expected_size:
                    raise ValueError(f"Download size mismatch: {path}")
                if expected_hash and sha256(temp) != expected_hash:
                    raise ValueError(f"Download SHA-256 mismatch: {path}")
                temp.replace(path)
                return
            except Exception:
                temp.unlink(missing_ok=True)
                if attempt == 2:
                    raise
    finally:
        temp.unlink(missing_ok=True)


@contextlib.contextmanager
def model_run():
    if os.getpriority(os.PRIO_PROCESS, 0) < 10:
        raise RuntimeError("Run evaluation with nice -n 15")
    CACHE.mkdir(parents=True, exist_ok=True)
    with (CACHE / "model.lock").open("w") as lock:
        fcntl.flock(lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
        yield


def normalize(text):
    # Unicode letters/numbers preserve accented words, Hangul and kana. No
    # romanization, number expansion or language-specific hand correction.
    text = unicodedata.normalize("NFKC", text).casefold().replace("’", "'")
    return "".join(c if c.isalnum() or c == "'" else " " for c in text).split()


def score(references, hypotheses, language):
    import jiwer

    if not references or len(references) != len(hypotheses):
        raise ValueError("Nonempty aligned reference/hypothesis lists required")
    refs = [" ".join(normalize(x)) for x in references]
    hyps = [" ".join(normalize(x)) for x in hypotheses]
    chars = jiwer.process_characters(
        ["".join(x.split()) for x in refs], ["".join(x.split()) for x in hyps]
    )
    words = jiwer.process_words(refs, hyps)

    def counts(result):
        return {
            "substitutions": result.substitutions,
            "deletions": result.deletions,
            "insertions": result.insertions,
            "reference_units": result.hits + result.substitutions + result.deletions,
        }

    use_chars = language in ("zh", "ja", "ko")
    return {
        "metric": "CER" if use_chars else "WER",
        "error_rate": chars.cer if use_chars else words.wer,
        "cer": chars.cer,
        "wer": words.wer,
        "character_edits": counts(chars),
        "word_edits": counts(words),
    }


def hub(model_id, repo, revision=None, include=None):
    endpoint = (
        f"https://huggingface.co/api/models/{repo}"
        + (f"/revision/{revision}" if revision else "")
        + "?blobs=true"
    )
    metadata = json.load(urllib.request.urlopen(endpoint, timeout=60))
    revision = metadata["sha"]
    records = []
    files = [
        f
        for f in metadata["siblings"]
        if (
            include(f["rfilename"])
            if include
            else not f["rfilename"].startswith((".", "samples/", "test_wavs/"))
            and f["rfilename"].endswith(
                (".json", ".safetensors", ".txt", ".model", ".onnx", "LICENSE")
            )
        )
    ]
    disk_guard(
        sum(
            f["size"] for f in files if not (CACHE / model_id / f["rfilename"]).exists()
        )
    )
    for file in files:
        name = file["rfilename"]
        path = CACHE / model_id / name
        if not path.resolve().is_relative_to((CACHE / model_id).resolve()):
            raise ValueError(name)
        url = f"https://huggingface.co/{repo}/resolve/{revision}/{name}"
        print("download", model_id, name, file["size"], flush=True)
        download(url, path, file["size"], file.get("lfs", {}).get("sha256"))
        records.append(
            {
                "path": name,
                "size_bytes": path.stat().st_size,
                "sha256": sha256(path),
                "url": url,
            }
        )
    inventory = {
        "id": model_id,
        "repo": repo,
        "revision": revision,
        "files": records,
        "licence_metadata": metadata.get("cardData", {}).get("license"),
    }
    save(OUT / "assets" / f"{model_id}.json", inventory)
    try:
        card = (
            urllib.request.urlopen(
                f"https://huggingface.co/{repo}/raw/{revision}/README.md", timeout=60
            )
            .read()
            .decode()
        )
        cards = CACHE / "source-cards"
        cards.mkdir(parents=True, exist_ok=True)
        (cards / (model_id + ".md")).write_text(card)
        inventory["model_card_url"] = (
            f"https://huggingface.co/{repo}/blob/{revision}/README.md"
        )
        inventory["model_card_sha256"] = hashlib.sha256(card.encode()).hexdigest()
        save(OUT / "assets" / f"{model_id}.json", inventory)
    except Exception as error:
        print("Card unavailable", error, flush=True)
    return inventory


def fleurs_rows(raw_text, count):
    # Quoting is not a CSV contract here: unbalanced literal quotes can consume
    # the phonetic-reference column and make correct speech look inaccurate.
    rows = list(
        csv.reader(io.StringIO(raw_text), delimiter="\t", quoting=csv.QUOTE_NONE)
    )[:count]
    if len(rows) != count or any(len(row) != 7 for row in rows):
        raise ValueError("Expected exactly seven literal FLEURS TSV fields per row")
    return rows


def prepare(count=100):
    import soundfile as sf

    repo = "google/fleurs"
    revision = json.load(
        urllib.request.urlopen(
            f"https://huggingface.co/api/datasets/{repo}", timeout=60
        )
    )["sha"]
    for lang, config in LANGUAGES.items():
        dest = CACHE / "data" / lang
        if (dest / "manifest.json").exists():
            continue
        disk_guard(256 * 1024**2)
        base = (
            f"https://huggingface.co/datasets/{repo}/resolve/{revision}/data/{config}"
        )
        raw_tsv = urllib.request.urlopen(base + "/test.tsv", timeout=60).read()
        table = fleurs_rows(raw_tsv.decode(), count)
        wanted = {r[1]: (i, r[0], r[3]) for i, r in enumerate(table)}
        rows = []
        dest.mkdir(parents=True, exist_ok=True)
        with urllib.request.urlopen(
            base + "/audio/test.tar.gz", timeout=90
        ) as source, tarfile.open(fileobj=source, mode="r|gz") as archive:
            for member in archive:
                name = Path(member.name).name
                if name not in wanted:
                    continue
                i, sid, reference = wanted.pop(name)
                audio, rate = sf.read(
                    io.BytesIO(archive.extractfile(member).read()), dtype="float32"
                )
                path = dest / name
                sf.write(path, audio, rate, subtype="PCM_16")
                rows.append(
                    {
                        "order": i,
                        "id": sid + "-" + name,
                        "audio_path": name,
                        "reference": reference,
                        "duration_s": len(audio) / rate,
                        "sha256": sha256(path),
                    }
                )
                if not wanted:
                    break
        if wanted:
            raise ValueError(f"Missing FLEURS audio: {wanted}")
        rows.sort(key=lambda r: r["order"])
        result = {
            "dataset": repo,
            "revision": revision,
            "config": config,
            "split": "test",
            "selection": f"First {count} utterances in published test.tsv order; duplicate prompts retained",
            "licence": "CC-BY-4.0",
            "source": base,
            "source_tsv_sha256": hashlib.sha256(raw_tsv).hexdigest(),
            "reference_parsing": "Literal tab separation, csv.QUOTE_NONE; seven fields required. Do not parse embedded quote characters as CSV field delimiters.",
            "samples": rows,
        }
        save(dest / "manifest.json", result)
        save(OUT / "data" / f"{lang}.json", result)
        print(
            "FLEURS",
            lang,
            len(rows),
            round(sum(r["duration_s"] for r in rows), 1),
            flush=True,
        )


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["data", "hub"])
    parser.add_argument("--model")
    parser.add_argument("--repo")
    parser.add_argument("--revision")
    args = parser.parse_args()
    if args.action == "data":
        prepare()
    else:
        hub(args.model, args.repo, args.revision)
