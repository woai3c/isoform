"""Qwen3-ASR-1.7B engine via mlx-qwen3-asr."""

from mlx_qwen3_asr import Session

from engines.base import ASREngine, TranscriptionResult
from engines.registry import register_engine


LANGUAGE_MAP = {
    "en": "English",
    "zh": "Chinese",
    "zh-en": None,  # auto-detect
    "ja": "Japanese",
    "ko": "Korean",
}


@register_engine("qwen3_asr")
class Qwen3ASREngine(ASREngine):
    name = "Qwen3-ASR-1.7B (MLX)"

    def __init__(self, model: str = "mlx-community/Qwen3-ASR-1.7B-8bit", **kwargs):
        self._model_id = model
        self._session = None

    def load(self) -> None:
        self._session = Session(model=self._model_id)

    def transcribe(
        self, audio_path: str, language_hint: str = ""
    ) -> TranscriptionResult:
        kwargs = {}
        lang = LANGUAGE_MAP.get(language_hint)
        if lang:
            kwargs["language"] = lang

        result = self._session.transcribe(audio_path, **kwargs)
        text = result.text if hasattr(result, "text") else str(result)
        detected_lang = getattr(result, "language", language_hint)

        return TranscriptionResult(
            text=text.strip(),
            language=detected_lang or "",
        )

    def unload(self) -> None:
        self._session = None

    def get_model_info(self) -> dict:
        return {
            "name": self.name,
            "model": self._model_id,
            "framework": "mlx-qwen3-asr",
        }
