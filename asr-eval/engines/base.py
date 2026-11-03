from abc import ABC, abstractmethod
from dataclasses import dataclass


@dataclass
class TranscriptionResult:
    text: str
    language: str = ""
    segments: list = None

    def __post_init__(self):
        if self.segments is None:
            self.segments = []


class ASREngine(ABC):
    """Base class for ASR engines in the evaluation framework."""

    name: str = "base"

    @abstractmethod
    def load(self) -> None:
        """Load the model into memory. Called once before evaluation."""

    @abstractmethod
    def transcribe(
        self, audio_path: str, language_hint: str = ""
    ) -> TranscriptionResult:
        """Transcribe a single audio file and return the result."""

    @abstractmethod
    def unload(self) -> None:
        """Release model resources."""

    def get_model_info(self) -> dict:
        """Return metadata about the engine for reporting."""
        return {"name": self.name}
