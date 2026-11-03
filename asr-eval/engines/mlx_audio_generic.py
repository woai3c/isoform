"""Generic mlx-audio STT engine adapter.

Supports any model loadable via mlx_audio.stt.load, including:
- Mega-ASR (noise-robust Qwen3-ASR variant)
- Whisper variants via mlx-audio
- Other mlx-audio compatible models
"""

from mlx_audio.stt import load

from engines.base import ASREngine, TranscriptionResult
from engines.registry import register_engine


LANGUAGE_MAP = {
    "en": "English",
    "zh": "Chinese",
    "zh-en": None,
    "ja": "Japanese",
    "ko": "Korean",
}


@register_engine("mlx_audio")
class MLXAudioEngine(ASREngine):
    name = "MLX Audio STT"

    def __init__(self, model: str = "mlx-community/Mega-ASR-bf16", **kwargs):
        self._model_id = model
        self._model = None
        self.name = f"MLX-Audio ({model.split('/')[-1]})"

    def load(self) -> None:
        self._model = load(self._model_id)

    def transcribe(
        self, audio_path: str, language_hint: str = ""
    ) -> TranscriptionResult:
        kwargs = {}
        lang = LANGUAGE_MAP.get(language_hint)
        if lang:
            kwargs["language"] = lang

        result = self._model.generate(audio_path, **kwargs)

        text = ""
        if hasattr(result, "text"):
            text = result.text
        elif isinstance(result, dict):
            text = result.get("text", "")
        else:
            text = str(result)

        detected_lang = ""
        if hasattr(result, "language"):
            detected_lang = result.language

        return TranscriptionResult(
            text=text.strip(), language=detected_lang or language_hint
        )

    def unload(self) -> None:
        self._model = None

    def get_model_info(self) -> dict:
        return {
            "name": self.name,
            "model": self._model_id,
            "framework": "mlx-audio",
        }
