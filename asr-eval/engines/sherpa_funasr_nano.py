"""Full Fun-ASR-Nano int8 decoder, distinct from its standalone encoder."""

import sherpa_onnx
import soundfile as sf

from engines.sherpa_qwen3 import SherpaQwen3Engine
from engines.base import TranscriptionResult
from engines.registry import register_engine


@register_engine("sherpa_funasr_nano")
class SherpaFunASRNanoEngine(SherpaQwen3Engine):
    name = "Fun-ASR-Nano int8 (sherpa-onnx)"

    def load(self):
        d = self.model_dir
        self.recognizer = sherpa_onnx.OfflineRecognizer.from_funasr_nano(
            encoder_adaptor=str(d / "encoder_adaptor.int8.onnx"),
            llm=str(d / "llm.int8.onnx"),
            embedding=str(d / "embedding.int8.onnx"),
            tokenizer=str(d / "Qwen3-0.6B"),
            num_threads=self.num_threads,
            max_new_tokens=256,
            seed=42,
        )

    def transcribe(self, audio_path: str, language_hint: str = ""):
        audio, rate = sf.read(audio_path, dtype="float32", always_2d=True)
        stream = self.recognizer.create_stream()
        stream.accept_waveform(rate, audio[:, 0])
        self.recognizer.decode_stream(stream)
        return TranscriptionResult(text=stream.result.text.strip())
