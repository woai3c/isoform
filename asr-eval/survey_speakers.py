"""Smoke test the unchanged speaker pack on two public LibriSpeech speakers.

This is not an overlap/meeting DER benchmark. ASR and speaker models remain
separate: output labels and boundaries are observations about audio, not text.
"""

import json
import os
import time

import numpy as np
import soundfile as sf
import sherpa_onnx as s

from survey_assets import CACHE, ROOT, download

FILES = [
    (
        "silero_vad.onnx",
        "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/silero_vad.onnx",
        "9e2449e1087496d8d4caba907f23e0bd3f78d91fa552479bb9c23ac09cbb1fd6",
    ),
    (
        "campplus.onnx",
        "https://github.com/k2-fsa/sherpa-onnx/releases/download/speaker-recongition-models/3dspeaker_speech_campplus_sv_zh-cn_16k-common.onnx",
        "f682b514c05d947ee3fa91cd6ec6c5c7543479a128373fa29b1faedccd21fd11",
    ),
    (
        "segmentation.onnx",
        "https://huggingface.co/csukuangfj/sherpa-onnx-pyannote-segmentation-3-0/resolve/9403a6902bb58e3d5ae8c7e77c3422de279db2e0/model.int8.onnx",
        "d582f4b4c6b48205de7e0643c57df0df5615a3c176189be3fc461e9d18827b5d",
    ),
]


def main():
    if os.getpriority(os.PRIO_PROCESS, 0) < 10:
        raise SystemExit("Run with nice -n 15")
    files = [
        download(url, CACHE / "speakers" / name, digest) for name, url, digest in FILES
    ]
    (ROOT / "survey-2026-10/assets/speakers.json").write_text(
        json.dumps({"id": "speaker-pack", "files": files}, indent=2) + "\n"
    )
    rate = 16000
    parts, reference = [], []
    offset = 0
    sources = []
    datasets = ["librispeech_test_clean", "librispeech_test_other"]
    manifests = [
        json.loads((ROOT / "survey-2026-10/data" / (name + ".json")).read_text())[
            "samples"
        ]
        for name in datasets
    ]
    for index in range(3):
        for speaker in [0, 1]:
            row = manifests[speaker][index]
            audio, sr = sf.read(
                CACHE / "data" / datasets[speaker] / row["audio_path"], dtype="float32"
            )
            assert sr == rate
            parts.append(np.zeros(rate, dtype="float32"))
            offset += rate
            start = offset / rate
            parts.append(audio)
            offset += len(audio)
            reference.append(
                {
                    "speaker": speaker,
                    "start": start,
                    "end": offset / rate,
                    "source_id": row["id"],
                }
            )
            sources.append(row["id"])
    audio = np.concatenate(parts)
    config = s.OfflineSpeakerDiarizationConfig(
        segmentation=s.OfflineSpeakerSegmentationModelConfig(
            pyannote=s.OfflineSpeakerSegmentationPyannoteModelConfig(
                model=str(CACHE / "speakers/segmentation.onnx")
            ),
            num_threads=1,
        ),
        embedding=s.SpeakerEmbeddingExtractorConfig(
            model=str(CACHE / "speakers/campplus.onnx"), num_threads=1
        ),
        clustering=s.FastClusteringConfig(num_clusters=-1, threshold=0.5),
        min_duration_on=0.2,
        min_duration_off=0.3,
    )
    start = time.perf_counter()
    diarizer = s.OfflineSpeakerDiarization(config)
    load_s = time.perf_counter() - start
    start = time.perf_counter()
    result = diarizer.process(audio)
    elapsed = time.perf_counter() - start
    segments = [
        {"speaker": seg.speaker, "start": seg.start, "end": seg.end}
        for seg in result.sort_by_start_time()
    ]
    turn_labels = []
    for turn in reference:
        overlaps = {}
        for seg in segments:
            overlap = max(
                0, min(turn["end"], seg["end"]) - max(turn["start"], seg["start"])
            )
            overlaps[seg["speaker"]] = overlaps.get(seg["speaker"], 0) + overlap
        turn_labels.append(max(overlaps, key=overlaps.get) if overlaps else None)
    separated = len(set(turn_labels)) == 2 and all(
        turn_labels[i] == turn_labels[i % 2] for i in range(6)
    )
    output = {
        "scope": "Two English audiobook speakers, six concatenated turns, no overlap; clustering smoke test only, not meeting DER",
        "audio_duration_s": len(audio) / rate,
        "load_s": load_s,
        "inference_s": elapsed,
        "rtf": elapsed / (len(audio) / rate),
        "reference": reference,
        "segments": segments,
        "dominant_turn_labels": turn_labels,
        "six_turn_separation_passed": separated,
    }
    (ROOT / "survey-2026-10/runs/speaker-pack.json").write_text(
        json.dumps(output, indent=2) + "\n"
    )
    print(json.dumps(output, indent=2))
    if not separated:
        raise SystemExit(1)


if __name__ == "__main__":
    main()
