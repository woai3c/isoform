"""Performance metrics for ASR evaluation: RTF, latency, memory, CPU."""

import os
import time
import threading
from dataclasses import dataclass, field

import psutil


@dataclass
class PerformanceSample:
    audio_duration_s: float
    processing_time_s: float
    peak_memory_mb: float
    cpu_percent: float

    @property
    def rtf(self) -> float:
        if self.audio_duration_s <= 0:
            return 0.0
        return self.processing_time_s / self.audio_duration_s

    @property
    def latency_ms(self) -> float:
        return self.processing_time_s * 1000


@dataclass
class PerformanceTracker:
    """Tracks performance metrics across multiple transcription runs."""

    samples: list[PerformanceSample] = field(default_factory=list)
    model_load_time_s: float = 0.0
    _process: psutil.Process = field(default=None, repr=False)

    def __post_init__(self):
        self._process = psutil.Process(os.getpid())

    def measure(self, audio_duration_s: float):
        """Context manager for measuring a single transcription."""
        return _MeasureContext(self, audio_duration_s)

    def summary(self) -> dict:
        """Compute aggregate performance statistics."""
        if not self.samples:
            return {}

        rtfs = [s.rtf for s in self.samples]
        latencies = [s.latency_ms for s in self.samples]
        memories = [s.peak_memory_mb for s in self.samples]

        return {
            "model_load_s": round(self.model_load_time_s, 3),
            "num_samples": len(self.samples),
            "total_audio_s": round(sum(s.audio_duration_s for s in self.samples), 2),
            "total_processing_s": round(
                sum(s.processing_time_s for s in self.samples), 2
            ),
            "rtf_mean": round(_mean(rtfs), 4),
            "rtf_corpus": round(
                sum(s.processing_time_s for s in self.samples)
                / sum(s.audio_duration_s for s in self.samples),
                4,
            ),
            "rtf_median": round(_median(rtfs), 4),
            "rtf_p95": round(_percentile(rtfs, 95), 4),
            "latency_mean_ms": round(_mean(latencies), 1),
            "latency_median_ms": round(_median(latencies), 1),
            "latency_p95_ms": round(_percentile(latencies, 95), 1),
            "peak_memory_mb": round(max(memories), 1),
            "avg_memory_mb": round(_mean(memories), 1),
            "cpu_percent_mean": round(_mean([s.cpu_percent for s in self.samples]), 1),
            "memory_method": "process RSS sampled every 20 ms during inference; excludes Metal allocations not mapped in RSS",
        }

    def reset(self):
        self.samples.clear()
        self.model_load_time_s = 0.0


class _MeasureContext:
    def __init__(self, tracker: PerformanceTracker, audio_duration_s: float):
        self._tracker = tracker
        self._audio_duration_s = audio_duration_s
        self._start_time = 0.0
        self._start_memory = 0.0
        self._stop = threading.Event()
        self._peak_memory = 0.0

    def _sample(self):
        while not self._stop.wait(0.02):
            self._peak_memory = max(
                self._peak_memory,
                self._tracker._process.memory_info().rss / (1024 * 1024),
            )

    def __enter__(self):
        self._tracker._process.cpu_percent()
        mem_info = self._tracker._process.memory_info()
        self._start_memory = mem_info.rss / (1024 * 1024)
        self._peak_memory = self._start_memory
        self._sampler = threading.Thread(target=self._sample, daemon=True)
        self._sampler.start()
        self._start_time = time.perf_counter()
        return self

    def __exit__(self, exc_type, exc_val, exc_tb):
        elapsed = time.perf_counter() - self._start_time
        self._stop.set()
        self._sampler.join()
        cpu = self._tracker._process.cpu_percent()
        mem_info = self._tracker._process.memory_info()
        peak_mb = max(self._peak_memory, mem_info.rss / (1024 * 1024))

        self._tracker.samples.append(
            PerformanceSample(
                audio_duration_s=self._audio_duration_s,
                processing_time_s=elapsed,
                peak_memory_mb=peak_mb,
                cpu_percent=cpu,
            )
        )
        return False


def _mean(vals: list[float]) -> float:
    return sum(vals) / len(vals) if vals else 0.0


def _median(vals: list[float]) -> float:
    if not vals:
        return 0.0
    s = sorted(vals)
    n = len(s)
    if n % 2 == 1:
        return s[n // 2]
    return (s[n // 2 - 1] + s[n // 2]) / 2


def _percentile(vals: list[float], p: float) -> float:
    if not vals:
        return 0.0
    s = sorted(vals)
    k = (len(s) - 1) * p / 100
    f = int(k)
    c = f + 1
    if c >= len(s):
        return s[-1]
    return s[f] + (k - f) * (s[c] - s[f])
