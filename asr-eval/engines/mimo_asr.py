"""MiMo-V2.5-ASR engine via upstream mlx-audio >= 0.6.0.

Xiaomi's MiMo-V2.5-ASR supports Chinese, English, dialects, and code-switching.
The separate pinned audio tokenizer must be installed alongside local weights.
"""

from pathlib import Path
import mlx.core as mx
from mlx_audio.stt import load

from engines.base import ASREngine, TranscriptionResult
from engines.registry import register_engine


LANGUAGE_MAP = {
    "en": "en",
    "zh": "zh",
    "zh-en": None,
}


@register_engine("mimo_asr")
class MiMoASREngine(ASREngine):
    name = "MiMo-V2.5-ASR (MLX)"

    def __init__(
        self,
        model: str = "mlx-community/MiMo-V2.5-ASR-MLX-4bit",
        memory_limit_bytes: int | None = None,
        **kwargs,
    ):
        self._model_id = model
        self._memory_limit_bytes = memory_limit_bytes
        self._model = None

    def load(self) -> None:
        if Path(self._model_id).is_dir():
            sibling = Path(self._model_id).resolve().parent / "MiMo-Audio-Tokenizer"
            if not (sibling / "model.safetensors").is_file():
                raise FileNotFoundError(
                    "Download the pinned MiMo audio tokenizer first"
                )
        mx.set_cache_limit(128 * 1024 * 1024)
        if self._memory_limit_bytes is not None:
            mx.set_memory_limit(self._memory_limit_bytes)
        self._model = load(self._model_id)

    def transcribe(
        self, audio_path: str, language_hint: str = ""
    ) -> TranscriptionResult:
        kwargs = {}
        lang = LANGUAGE_MAP.get(language_hint)
        if lang:
            kwargs["language"] = lang

        result = self._model.generate(audio_path, **kwargs)
        text = result.text if hasattr(result, "text") else str(result)
        detected_lang = getattr(result, "language", language_hint) or ""

        return TranscriptionResult(text=text.strip(), language=detected_lang)

    def unload(self) -> None:
        self._model = None

    def get_model_info(self) -> dict:
        return {
            "name": self.name,
            "model": self._model_id,
            "framework": "mlx-audio >= 0.6.0",
            "mlx_cache_limit_bytes": 128 * 1024 * 1024,
            "mlx_memory_limit_bytes": self._memory_limit_bytes,
        }
