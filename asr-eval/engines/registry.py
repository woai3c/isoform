from engines.base import ASREngine
from importlib import import_module

_REGISTRY: dict[str, type[ASREngine]] = {}
_MODULES = {
    "sherpa_sensevoice": "sherpa_sensevoice",
    "sherpa_paraformer": "sherpa_paraformer",
    "whisper_mlx": "whisper_mlx",
    "qwen3_asr": "qwen3_asr",
    "parakeet_mlx": "parakeet_mlx",
    "mlx_audio": "mlx_audio_generic",
    "mimo_asr": "mimo_asr",
    "sherpa_qwen3": "sherpa_qwen3",
    "sherpa_funasr_nano": "sherpa_funasr_nano",
}


def register_engine(engine_type: str):
    def decorator(cls):
        _REGISTRY[engine_type] = cls
        return cls

    return decorator


def create_engine(engine_type: str, **kwargs) -> ASREngine:
    if engine_type in _MODULES and engine_type not in _REGISTRY:
        import_module(f"engines.{_MODULES[engine_type]}")
    if engine_type not in _REGISTRY:
        available = ", ".join(list_engines())
        raise ValueError(
            f"Unknown engine type: {engine_type!r}. Available: {available}"
        )
    return _REGISTRY[engine_type](**kwargs)


def list_engines() -> list[str]:
    return sorted(set(_REGISTRY) | set(_MODULES))
