"""App-compatible Qwen3-ASR 0.6B int8; bounded 18 s decoding windows."""

from pathlib import Path

import soundfile as sf
import sherpa_onnx

from engines.base import ASREngine, TranscriptionResult
from engines.registry import register_engine


@register_engine("sherpa_qwen3")
class SherpaQwen3Engine(ASREngine):
    name = "Qwen3-ASR 0.6B int8 (sherpa-onnx)"

    def __init__(self, model_dir: str, num_threads: int = 2, **kwargs):
        self.model_dir = Path(model_dir).expanduser().resolve()
        self.num_threads = num_threads
        self.recognizer = None

    def load(self):
        d = self.model_dir
        self.recognizer = sherpa_onnx.OfflineRecognizer.from_qwen3_asr(
            conv_frontend=str(d / "conv_frontend.onnx"),
            encoder=str(d / "encoder.int8.onnx"),
            decoder=str(d / "decoder.int8.onnx"),
            tokenizer=str(d / "tokenizer"),
            num_threads=self.num_threads,
            max_new_tokens=128,
            seed=42,
        )

    def transcribe(self, audio_path: str, language_hint: str = ""):
        audio, rate = sf.read(audio_path, dtype="float32", always_2d=True)
        audio = audio[:, 0]
        texts = []
        # Same native decoder bound as the app, but no app VAD or padding.
        for start in range(0, len(audio), rate * 18):
            stream = self.recognizer.create_stream()
            stream.accept_waveform(rate, audio[start : start + rate * 18])
            self.recognizer.decode_stream(stream)
            texts.append(stream.result.text)
        return TranscriptionResult(text=" ".join(texts).strip())

    def unload(self):
        self.recognizer = None

    def get_model_info(self):
        return {
            "name": self.name,
            "model_dir": str(self.model_dir),
            "num_threads": self.num_threads,
            "max_window_s": 18,
            "language": "auto",
        }
