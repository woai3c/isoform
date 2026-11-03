"""SenseVoice engine via sherpa-onnx Python bindings.

Evaluation datasets provide pre-segmented utterances, so VAD is not used here.
"""

import os
import re
from pathlib import Path

import soundfile as sf
import sherpa_onnx

from engines.base import ASREngine, TranscriptionResult
from engines.registry import register_engine


@register_engine("sherpa_sensevoice")
class SherpaSenseVoiceEngine(ASREngine):
    name = "SenseVoice (sherpa-onnx)"

    def __init__(
        self, model_dir: str = "./.cache/sherpa", num_threads: int = 2, **kwargs
    ):
        self._model_dir = Path(os.path.expanduser(model_dir))
        self._num_threads = num_threads
        self._recognizer = None

    def load(self) -> None:
        sense_voice_model = str(self._model_dir / "sense-voice" / "model.int8.onnx")
        tokens = str(self._model_dir / "sense-voice" / "tokens.txt")

        for p in [sense_voice_model, tokens]:
            if not os.path.exists(p):
                raise FileNotFoundError(f"Missing model file: {p}")

        self._recognizer = sherpa_onnx.OfflineRecognizer.from_sense_voice(
            model=sense_voice_model,
            tokens=tokens,
            language="auto",
            use_itn=True,
            num_threads=self._num_threads,
            decoding_method="greedy_search",
        )

    def transcribe(
        self, audio_path: str, language_hint: str = ""
    ) -> TranscriptionResult:
        samples, sample_rate = sf.read(audio_path, dtype="float32", always_2d=True)
        samples = samples.mean(axis=1)

        stream = self._recognizer.create_stream()
        stream.accept_waveform(sample_rate, samples)
        self._recognizer.decode_stream(stream)
        text = _strip_tags(stream.result.text)
        lang = getattr(stream.result, "lang", "")
        return TranscriptionResult(text=text, language=lang)

    def unload(self) -> None:
        self._recognizer = None

    def get_model_info(self) -> dict:
        return {
            "name": self.name,
            "model": "SenseVoice int8 ONNX",
            "num_threads": self._num_threads,
        }


def _strip_tags(text: str) -> str:
    return re.sub(r"<\|[^|]*\|>", "", text).strip()
