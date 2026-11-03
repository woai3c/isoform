"""Parakeet TDT engine via parakeet-mlx (Apple Silicon optimized).

NVIDIA Parakeet TDT v2 is the best English-only ASR model.
English-only -- does not support Chinese.
"""

import parakeet_mlx

from engines.base import ASREngine, TranscriptionResult
from engines.registry import register_engine


@register_engine("parakeet_mlx")
class ParakeetMLXEngine(ASREngine):
    name = "Parakeet TDT v2 (MLX)"

    def __init__(self, model: str = "mlx-community/parakeet-tdt-0.6b-v2", **kwargs):
        self._model_id = model
        self._model = None

    def load(self) -> None:
        self._model = parakeet_mlx.from_pretrained(self._model_id)

    def transcribe(
        self, audio_path: str, language_hint: str = ""
    ) -> TranscriptionResult:
        result = self._model.transcribe(audio_path)
        text = ""
        if isinstance(result, str):
            text = result
        elif isinstance(result, list):
            text = " ".join(r.text if hasattr(r, "text") else str(r) for r in result)
        elif hasattr(result, "text"):
            text = result.text
        else:
            text = str(result)

        return TranscriptionResult(text=text.strip(), language="en")

    def unload(self) -> None:
        self._model = None

    def get_model_info(self) -> dict:
        return {
            "name": self.name,
            "model": self._model_id,
            "framework": "parakeet-mlx",
            "languages": "English only",
        }
