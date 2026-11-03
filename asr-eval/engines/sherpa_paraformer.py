"""Paraformer (FunASR) engine via sherpa-onnx.

Alibaba's Paraformer is the gold standard for Chinese ASR.
Uses sherpa-onnx's from_paraformer backend with ONNX int8 models.
"""

import os
from pathlib import Path

import librosa
import sherpa_onnx

from engines.base import ASREngine, TranscriptionResult
from engines.registry import register_engine

MODEL_URLS = {
    "model": "https://github.com/k2-fsa/sherpa-onnx/releases/download/asr-models/sherpa-onnx-paraformer-zh-2024-03-09.tar.bz2",
}


@register_engine("sherpa_paraformer")
class SherpaParaformerEngine(ASREngine):
    name = "Paraformer-zh (sherpa-onnx)"

    def __init__(
        self,
        model_dir: str = "~/.cache/asr-eval/paraformer-zh",
        num_threads: int = 2,
        **kwargs,
    ):
        self._model_dir = Path(os.path.expanduser(model_dir))
        self._num_threads = num_threads
        self._recognizer = None

    def load(self) -> None:
        model_path = self._model_dir / "model.int8.onnx"
        tokens_path = self._model_dir / "tokens.txt"

        if not model_path.exists():
            self._download_model()

        for p in [model_path, tokens_path]:
            if not p.exists():
                raise FileNotFoundError(f"Missing model file: {p}")

        self._recognizer = sherpa_onnx.OfflineRecognizer.from_paraformer(
            paraformer=str(model_path),
            tokens=str(tokens_path),
            num_threads=self._num_threads,
            decoding_method="greedy_search",
        )

    def _download_model(self):
        import tarfile
        import urllib.request

        self._model_dir.mkdir(parents=True, exist_ok=True)
        url = MODEL_URLS["model"]
        tar_path = self._model_dir / "model.tar.bz2"
        print(f"  Downloading Paraformer model from {url}...")
        urllib.request.urlretrieve(url, str(tar_path))
        with tarfile.open(str(tar_path), "r:bz2") as tar:
            for member in tar.getmembers():
                basename = os.path.basename(member.name)
                if basename in ("model.int8.onnx", "tokens.txt"):
                    member.name = basename
                    tar.extract(member, str(self._model_dir))
        tar_path.unlink()

    def transcribe(
        self, audio_path: str, language_hint: str = ""
    ) -> TranscriptionResult:
        samples, _ = librosa.load(audio_path, sr=16000)

        stream = self._recognizer.create_stream()
        stream.accept_waveform(16000, samples.tolist())
        self._recognizer.decode_stream(stream)
        text = stream.result.text.strip()
        return TranscriptionResult(text=text, language="zh")

    def unload(self) -> None:
        self._recognizer = None

    def get_model_info(self) -> dict:
        return {
            "name": self.name,
            "model": "Paraformer-zh int8 ONNX (217M)",
            "num_threads": self._num_threads,
        }
