import json, pathlib, urllib.request, hashlib, concurrent.futures, tarfile, io

ROOT = pathlib.Path(__file__).resolve().parents[2]
M = "https://model-assets.yansu.app/"
entries = []


def entry(
    id,
    job,
    name,
    line,
    langs,
    rec=False,
    license="Apache-2.0",
    license_url="https://www.apache.org/licenses/LICENSE-2.0",
):
    e = dict(
        id=id,
        job=job,
        displayName=name,
        line=line,
        evidence="",
        sizeBytes=0,
        files=[],
        sources=[],
        licence=dict(name=license, url=license_url, nonCommercial=False),
        recommended=rec,
        languages=langs,
    )
    entries.append(e)
    return e


def file(e, path, urls, sha=None, size=None, archive=None):
    f = dict(path=path, sha256=sha, bytes=size, urls=urls)
    if archive:
        f["archive"] = archive
    e["files"].append(f)


def sources(e, upstream, kind="huggingface"):
    e["sources"] = [dict(kind="mirror", url=M), dict(kind=kind, url=upstream)]


s = entry(
    "sensevoice",
    "live",
    "SenseVoice",
    "Fast multilingual live transcription.",
    ["zh", "en", "ja", "ko", "yue"],
    True,
)
base = "https://huggingface.co/csukuangfj/sherpa-onnx-sense-voice-zh-en-ja-ko-yue-2024-07-17/resolve/main/"
for n in ["model.int8.onnx", "tokens.txt"]:
    file(s, "sherpa/sense-voice/" + n, [M + "sherpa/" + n, base + n])
s["licence"] = dict(
    name="FunASR Model Open Source License Agreement v1.1",
    url="https://github.com/modelscope/FunASR/blob/main/MODEL_LICENSE",
    nonCommercial=False,
)
sources(s, base)
s = entry(
    "speaker-pack",
    "speakerPack",
    "Speaker pack",
    "Silero VAD, pyannote segmentation 3.0 int8 and 3D-Speaker CAM++.",
    ["mul"],
    True,
)
for dest, mirror, url in [
    (
        "silero_vad.onnx",
        "silero_vad.onnx",
        "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx",
    ),
    (
        "speaker/3dspeaker_campplus.onnx",
        "3dspeaker_campplus.onnx",
        "https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-recongition-models/3dspeaker_speech_campplus_sv_zh-cn_16k-common.onnx",
    ),
    (
        "speaker/pyannote-segmentation-3.0.int8.onnx",
        "speaker/pyannote-segmentation-3.0.int8.onnx",
        "https://huggingface.co/csukuangfj/sherpa-onnx-pyannote-segmentation-3-0/resolve/9403a6902bb58e3d5ae8c7e77c3422de279db2e0/model.int8.onnx",
    ),
]:
    file(s, "sherpa/" + dest, [M + "sherpa/" + mirror, url])
s["licence"] = dict(
    name="MIT (Silero, pyannote); Apache-2.0 (3D-Speaker)",
    url="https://github.com/k2-fsa/sherpa-onnx/blob/master/LICENSE",
    nonCommercial=False,
)
sources(s, "https://github.com/k2-fsa/sherpa-onnx/releases", "github")
for id, name, dirname in [
    ("kokoro-en", "Kokoro", "kokoro-en-v0_19"),
    ("melotts-zh-en", "MeloTTS", "vits-melo-tts-zh_en"),
    ("qwen3-asr-0.6b", "Qwen3-ASR 0.6B", "sherpa-onnx-qwen3-asr-0.6B-int8-2026-03-25"),
]:
    tts = id != "qwen3-asr-0.6b"
    rel = ("sherpa/tts/" if tts else "sherpa/qwen3-asr/") + dirname
    n = dirname + ".tar.bz2"
    url = (
        "https://github.com/k2-fsa/sherpa-onnx/releases/download/"
        + ("tts-models/" if tts else "asr-models/")
        + n
    )
    e = entry(
        id,
        "voice" if tts else "afterCall",
        name,
        (
            "Reads your brief in English, several voices."
            if id == "kokoro-en"
            else (
                "Chinese and English, one voice each."
                if tts
                else "Current in-process after-call engine."
            )
        ),
        ["en"] if id == "kokoro-en" else ["zh", "en"],
        id in ["kokoro-en", "qwen3-asr-0.6b"],
    )
    e["licence"] = (
        dict(
            name="MIT",
            url="https://github.com/myshell-ai/MeloTTS/blob/main/LICENSE",
            nonCommercial=False,
        )
        if id == "melotts-zh-en"
        else e["licence"]
    )
    file(
        e,
        ".archives/" + n,
        [M + ("sherpa/tts/" if tts else "sherpa/qwen3-asr/") + n, url],
        archive=dict(format="tar.bz2", destination=rel, root=dirname, files=[]),
    )
    if id == "qwen3-asr-0.6b":
        e["files"][0]["archive"]["legacy"] = dict(
            marker=".isoform-verified",
            required=[
                "conv_frontend.onnx",
                "encoder.int8.onnx",
                "decoder.int8.onnx",
                "tokenizer/vocab.json",
                "tokenizer/merges.txt",
            ],
        )
    sources(e, url, "github")
for id, job, name, repo, paths, rec in [
    (
        "gliner-pii",
        "privacy",
        "GLiNER privacy filter",
        "knowledgator/gliner-pii-base-v1.0",
        [("model.onnx", "onnx/model.onnx"), ("tokenizer.json", "tokenizer.json")],
        True,
    ),
    (
        "minilm",
        "memory",
        "MiniLM memory search",
        "sentence-transformers/all-MiniLM-L6-v2",
        [("model.onnx", "onnx/model.onnx"), ("vocab.txt", "vocab.txt")],
        True,
    ),
    (
        "openai-privacy-filter",
        "privacy",
        "OpenAI privacy filter",
        "openai/privacy-filter",
        [
            ("model_q4.onnx", "onnx/model_q4.onnx"),
            ("model_q4.onnx_data", "onnx/model_q4.onnx_data"),
            ("tokenizer.json", "tokenizer.json"),
        ],
        False,
    ),
]:
    e = entry(
        id,
        job,
        name,
        (
            "Removes personal details."
            if job == "privacy"
            else "Finds what Isoform remembers."
        ),
        ["mul"],
        rec,
    )
    dirname = {"gliner-pii": "gliner-pii-base", "minilm": "all-MiniLM-L6-v2"}.get(
        id, id
    )
    base = "https://huggingface.co/" + repo + "/resolve/main/"
    for dest, remote in paths:
        mirror = {
            "gliner-pii": "gliner-pii/base-v1.0/",
            "minilm": "all-MiniLM-L6-v2/",
            "openai-privacy-filter": "openai-privacy-filter/",
        }[id]
        file(e, dirname + "/" + dest, [M + mirror + remote, base + remote])
    e["languages"] = ["en"] if id in ["gliner-pii", "minilm"] else e["languages"]
    sources(e, base)
for id, job, name, repo, line, langs, rec, lic in [
    (
        "qwen3-asr-1.7b",
        "afterCall",
        "Qwen3-ASR 1.7B",
        "mlx-community/Qwen3-ASR-1.7B-8bit",
        "More accurate; re-reads each recording.",
        ["mul"],
        False,
        "Apache-2.0",
    ),
    (
        "parakeet-tdt-v2",
        "live",
        "Parakeet TDT v2",
        "mlx-community/parakeet-tdt-0.6b-v2",
        "English only, the fewest errors.",
        ["en"],
        False,
        "CC-BY-4.0",
    ),
    (
        "whisper-large-v3-turbo",
        "live",
        "Whisper large-v3 turbo",
        "mlx-community/whisper-large-v3-turbo",
        "99 languages.",
        ["mul"],
        False,
        "MIT",
    ),
    (
        "mimo-v2.5-asr",
        "afterCall",
        "MiMo-V2.5-ASR",
        "mlx-community/MiMo-V2.5-ASR-MLX-4bit",
        "The most accurate, and the slowest.",
        ["zh", "en"],
        False,
        "Apache-2.0",
    ),
]:
    e = entry(
        id,
        job,
        name,
        line,
        langs,
        rec,
        lic,
        "https://huggingface.co/" + repo + "/blob/main/README.md",
    )
    base = "https://huggingface.co/" + repo + "/resolve/main/"
    try:
        data = json.load(
            urllib.request.urlopen(
                "https://huggingface.co/api/models/" + repo + "?blobs=true", timeout=30
            )
        )
        for f in data["siblings"]:
            n = f["rfilename"]
            if n in [".gitattributes", "README.md", "LICENSE"]:
                continue
            file(
                e,
                id + "/" + n,
                [M + id + "/" + n, base + n],
                f.get("lfs", {}).get("sha256"),
                f.get("size"),
            )
    except Exception as ex:
        e["unknownReason"] = str(ex)
    sources(e, base)
entry("none", "afterCall", "None", "Keep the live transcript as it is.", [])
e = entry(
    "nomic-embed-text",
    "memory",
    "Nomic embed text",
    "Optional embedding model managed by Ollama.",
    ["en"],
)
e["sizeBytes"] = None
e["sources"] = [
    dict(kind="mirror", url=M),
    dict(
        kind="huggingface", url="https://huggingface.co/nomic-ai/nomic-embed-text-v1.5"
    ),
]
e["licence"] = dict(
    name="Apache-2.0",
    url="https://huggingface.co/nomic-ai/nomic-embed-text-v1.5/blob/main/LICENSE",
    nonCommercial=False,
)
e["unknownReason"] = (
    "The previous Ollama pull used a mutable tag without pinned blobs. Install this model in Ollama; Isoform does not pull it."
)

# Evidence is extracted mechanically from the source tables, never hand-entered.
results = (ROOT / "asr-eval/RESULTS.md").read_text()
(ROOT / "wails-gui/models/evaluation.md").write_text(results)
for e in entries:
    label = {
        "sensevoice": "SenseVoice",
        "qwen3-asr-1.7b": "Qwen3-ASR-1.7B",
        "mimo-v2.5-asr": "MiMo-V2.5-ASR",
        "whisper-large-v3-turbo": "Whisper v3-turbo",
        "parakeet-tdt-v2": "Parakeet TDT v2",
    }.get(e["id"])
    if label:
        sec = ""
        rows = []
        for line in results.splitlines():
            if line.startswith("## "):
                sec = line[3:]
            if (
                line.startswith("|")
                and line.split("|")[1].strip().replace("**", "") == label
                and sec
                in [
                    "Word Error Rate (lower = better)",
                    "Real-Time Factor (lower = faster)",
                ]
            ):
                rows.append(sec + ": " + line)
        e["evidence"] = "asr-eval/RESULTS.md\n" + "\n".join(rows)


# Fetch headers and small files; large payloads streamed through SHA-256 only.
def measure(f):
    for url in f["urls"][::-1]:
        try:
            with urllib.request.urlopen(url, timeout=45) as r:
                if f["sha256"] and f["bytes"] and not f.get("archive"):
                    return
                h = hashlib.sha256()
                n = 0

                class Reader:
                    def read(self, size=-1):
                        nonlocal n
                        b = r.read(size)
                        h.update(b)
                        n += len(b)
                        return b

                rr = Reader()
                if f.get("archive"):
                    with tarfile.open(fileobj=rr, mode="r|bz2") as tar:
                        for member in tar:
                            if not member.isfile():
                                continue
                            path = member.name
                            prefix = f["archive"]["root"] + "/"
                            if not path.startswith(prefix):
                                continue
                            stream = tar.extractfile(member)
                            hh = hashlib.sha256()
                            count = 0
                            while b := stream.read(1024 * 1024):
                                hh.update(b)
                                count += len(b)
                            f["archive"]["files"].append(
                                dict(
                                    path=path[len(prefix) :],
                                    sha256=hh.hexdigest(),
                                    bytes=count,
                                )
                            )
                    while rr.read(1024 * 1024):
                        pass
                else:
                    while rr.read(1024 * 1024):
                        pass
                f["sha256"] = h.hexdigest()
                f["bytes"] = n
                f["verifiedFrom"] = url
                print("verified", f["path"], n, flush=True)
                return
        except Exception as ex:
            print("failed", url, str(ex), flush=True)
            f["unknownReason"] = str(ex)
    f["sha256"] = f.get("sha256")
    f["bytes"] = f.get("bytes")


with concurrent.futures.ThreadPoolExecutor(max_workers=4) as pool:
    list(pool.map(measure, [f for e in entries for f in e["files"]]))
for e in entries:
    e["sizeBytes"] = (
        None
        if e["id"] == "nomic-embed-text"
        else (
            sum(f["bytes"] or 0 for f in e["files"])
            if all(f["bytes"] is not None for f in e["files"])
            else None
        )
    )
for e in entries:
    if e["id"] == "none":
        e["licence"] = dict(name="Not applicable", url="", nonCommercial=False)
(ROOT / "wails-gui/models/manifest.json").write_text(
    json.dumps(entries, indent=4) + "\n"
)
