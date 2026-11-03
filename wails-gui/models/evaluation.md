# ASR Evaluation Results

Last updated: 2026-06-09

Evaluated on Apple Silicon Mac. 500 samples per dataset per model.

## Models

| Model | Framework | Model Size | Languages |
|--------|-----------|-----------|-----------|
| SenseVoice (current) | sherpa-onnx (ONNX, CPU) | 228M int8 | zh/en/ja/ko/yue |
| Paraformer-zh | sherpa-onnx (ONNX, CPU) | 217M int8 | zh/en |
| Whisper large-v3-turbo | mlx-whisper (MLX, GPU) | 809M | 99 languages |
| Qwen3-ASR-1.7B | mlx-qwen3-asr (MLX, GPU) | 2.46G 8-bit | 52 languages |
| MiMo-V2.5-ASR | mlx-audio fork (MLX, GPU) | 4.51G 4-bit | zh/en/dialects |
| Parakeet TDT v2 | parakeet-mlx (MLX, GPU) | 600M | English only |
| Mega-ASR | mlx-audio (MLX, GPU) | 4.17G bf16 | zh/en |

## Datasets

| Dataset | Language | Samples | Hours | Source |
|---------|----------|---------|-------|--------|
| FLEURS zh_cn | Chinese | 945 | ~3.1h | google/fleurs (CC BY 4.0) |
| ASCEND test | zh-en mixed | 1,315 | ~0.9h | CAiRE/ASCEND (CC BY-SA 4.0) |
| LibriSpeech test-clean | English | 2,620 | ~5.4h | openslr/librispeech_asr (CC BY 4.0) |
| LibriSpeech test-other | English | 2,939 | ~5.1h | openslr/librispeech_asr (CC BY 4.0) |

## Word Error Rate (lower = better)

| Model | FLEURS zh | ASCEND zh-en | LS clean | LS other |
|--------|-----------|-------------|----------|----------|
| **MiMo-V2.5-ASR** | **3.06%** | 17.55% | **2.63%** | **5.05%** |
| Qwen3-ASR-1.7B | 6.27% | **15.08%** | 3.28% | 4.48% |
| SenseVoice | 7.11% | 20.85% | 4.77% | 10.38% |
| Paraformer-zh | 8.78% | 19.48% | 19.19% | 28.86% |
| Whisper v3-turbo | 9.06% | 104.32%* | 4.84% | 6.07% |
| Parakeet TDT v2 | N/A | N/A | 2.49% | 3.92% |

*Whisper ASCEND WER >100% due to text normalization mismatch on code-switched content.

## Character Error Rate (lower = better)

| Model | FLEURS zh | ASCEND zh-en | LS clean | LS other |
|--------|-----------|-------------|----------|----------|
| **MiMo-V2.5-ASR** | **3.43%** | 15.44% | 1.05% | 2.07% |
| Qwen3-ASR-1.7B | 5.11% | **14.19%** | 0.88% | 1.17% |
| SenseVoice | 5.50% | 19.58% | 1.96% | 4.34% |
| Paraformer-zh | 6.39% | 16.64% | 6.54% | 10.25% |
| Whisper v3-turbo | 6.81% | 134.02% | 2.42% | 2.22% |
| Parakeet TDT v2 | N/A | N/A | **0.63%** | **1.06%** |

## Real-Time Factor (lower = faster)

RTF < 1.0 means faster than real-time. RTF 0.05 = 20x real-time.

| Model | FLEURS zh | ASCEND | LS clean | LS other |
|--------|-----------|--------|----------|----------|
| **Paraformer-zh** | **0.016** | **0.021** | **0.017** | **0.016** |
| SenseVoice | 0.043 | 0.043 | 0.056 | 0.050 |
| Parakeet TDT v2 | N/A | N/A | 0.543 | 0.105 |
| Qwen3-ASR-1.7B | 0.267 | 0.937 | 0.407 | 0.530 |
| Whisper v3-turbo | 0.333 | 4.005 | 0.590 | 0.598 |
| MiMo-V2.5-ASR | 0.598 | 1.674 | 0.590 | 0.720 |

## Latency and Resources

| Model | Avg Latency | Peak Memory | Model Load |
|--------|-------------|-------------|------------|
| Paraformer-zh | 41-183ms | 328-866MB | 0.9s |
| SenseVoice | 89-496ms | 380-618MB | 1-3s |
| Parakeet TDT v2 | 636-3737ms | 86-1233MB | 2s |
| Qwen3-ASR-1.7B | 1516-2980ms | 80-88MB | 1.5-3s |
| Whisper v3-turbo | 3000-4572ms | 90-109MB | 3s |
| MiMo-V2.5-ASR | 2658-6396ms | 84-172MB | 58s |

## Tier List

| Tier | Model | Best For |
|------|--------|----------|
| S+ | MiMo-V2.5-ASR | Accuracy king: best zh + en WER. Too slow for real-time. |
| S | Qwen3-ASR-1.7B | Best all-round accuracy. Best code-switching. Near-real-time capable. |
| A | SenseVoice | Production choice: best speed/accuracy tradeoff for bilingual real-time voice input. |
| A | Parakeet TDT v2 | English SOTA (2.49% WER). No Chinese support. |
| B | Whisper v3-turbo | Widest language support. Slow. Fails on code-switching. |
| B | Paraformer-zh | Fastest model (RTF 0.016). Weak on English. |

## Recommendation

**Keep SenseVoice as the Yansu Desktop production ASR model.**

- Speed is critical for real-time voice input UX (push-to-talk, background listening)
- SenseVoice RTF 0.05 vs MiMo 0.6 vs Qwen3 0.4 -- SenseVoice is 8-12x faster
- SenseVoice runs in-process via sherpa-onnx Go bindings; MLX models need Python subprocess
- 7.11% WER on Chinese is acceptable for voice input (not transcription)

For future consideration:
- Watch for Qwen3-ASR ONNX/sherpa-onnx support (`from_qwen3_asr` API exists)
- MiMo-V2.5-ASR for offline transcription features
- Paraformer-zh if app becomes Chinese-primary (2-3x faster than SenseVoice)
