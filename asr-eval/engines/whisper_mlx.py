"""Whisper engine via mlx-whisper (Apple Silicon optimized)."""

import mlx_whisper

from engines.base import ASREngine, TranscriptionResult
from engines.registry import register_engine


LANGUAGE_MAP = {
    "en": "english",
    "zh": "chinese",
    "zh-en": None,  # auto-detect for code-switching
    "ja": "japanese",
    "ko": "korean",
}


@register_engine("whisper_mlx")
class WhisperMLXEngine(ASREngine):
    name = "Whisper (MLX)"

    def __init__(self, model: str = "mlx-community/whisper-large-v3-mlx", **kwargs):
        self._model_repo = model
        self._loaded = False
        self.name = f"Whisper ({model.split('/')[-1]})"

    def load(self) -> None:
        # mlx-whisper loads the model on first transcribe call and caches it.
        # Do a warmup transcribe to trigger the download/load now.
        # We generate a tiny silent WAV in memory and transcribe it.
        import os
        import tempfile
        import numpy as np
        import soundfile as sf

        f = tempfile.NamedTemporaryFile(suffix=".wav", delete=False)
        f.close()
        try:
            silence = np.zeros(16000, dtype=np.float32)  # 1s silence
            sf.write(f.name, silence, 16000)
            mlx_whisper.transcribe(f.name, path_or_hf_repo=self._model_repo)
        finally:
            try:
                os.unlink(f.name)
            except OSError:
                pass
        self._loaded = True

    def transcribe(
        self, audio_path: str, language_hint: str = ""
    ) -> TranscriptionResult:
        kwargs = {"path_or_hf_repo": self._model_repo, "word_timestamps": True}
        lang = LANGUAGE_MAP.get(language_hint)
        if lang:
            kwargs["language"] = lang

        result = mlx_whisper.transcribe(audio_path, **kwargs)

        segments = []
        if "segments" in result:
            for seg in result["segments"]:
                segments.append(
                    {
                        "start": seg.get("start", 0),
                        "end": seg.get("end", 0),
                        "text": seg.get("text", ""),
                    }
                )

        detected_lang = result.get("language", language_hint)
        return TranscriptionResult(
            text=result.get("text", "").strip(),
            language=detected_lang,
            segments=segments,
        )

    def unload(self) -> None:
        self._loaded = False
        # mlx-whisper doesn't expose explicit unload; rely on GC

    def get_model_info(self) -> dict:
        return {
            "name": self.name,
            "model": self._model_repo,
            "framework": "mlx-whisper",
        }
