# Multilingual transcription and sentence delivery for brief voice

Evaluation date: 2026-10-23. Apple M4 Mac mini, 10 CPU cores, 16 GB unified
memory, macOS 26.2. This extends [the merged October survey](RESULTS-2026-10.md)
with common seven-language speech data, complete-sentence TTS delivery and
multilingual generated screenshots. All inputs are public/generated. The
Isoform app was not launched, and no private settings or recordings were used.

| Job | Recommended single artifact/profile | Measured reason | Setup change |
|---|---|---|---|
| Live transcription | **Qwen3-ASR 1.7B MLX 8-bit + speaker pack** | 4.40% seven-language macro error; worst-language RTF 0.132 | Replace the draft's 0.6B live choice with a live alias of the 1.7B artifact; integrate its MLX worker separately. |
| After-call transcription | **The same Qwen3-ASR 1.7B MLX 8-bit** | Best completed seven-language accuracy; beats Whisper in each subset | Share verified files with live. The production native 0.6B backend still needs replacement. |
| Brief voice | **Ming 3.0 TTS, six decoder steps, resident sentence delivery** | Only cleared tested profile passing both speed gates in all six requested languages; warm first sentence 0.654–1.043 s; English brief 57.4 s for 102.5 s audio | Keep the existing weights, set `ddpm_steps=6`, primary voices and explicit language, and synthesize sentence by sentence. |
| Screen OCR | **Apple Vision accurate + automatic language detection** | JA99.67%, KO100%, ES/FR99.84%, DE99.55% on generated screens | Change the language configuration; retain the system engine and add no alternative model. |

The ASR limits below are explicit survey throughput assumptions. Voice first-sentence readiness is a **warm complete greeting PCM buffer**, before the sound-device queue; Ming's first informative sentence takes 2.72–4.32 s. These measurements do not establish cold-start latency or uninterrupted playback. The per-language voice back-ASR tables below provide the intelligibility comparison. `gplComplianceAccepted` remains **false**; SenseVoice remains pending and Kokoro remains restricted.

## Decision method

One artifact must cover all required languages for its job. Unsupported
languages are shown as unavailable, never as zero errors or an average over a
more favourable subset. Live and after-call ASR are ranked independently among
cleared, completed, zero-failure configurations. Language-specific alternatives
are useful fallbacks for a weak language, not an automatic multi-model routing
recommendation.

The setup handoff specifies multilingual coverage but no numerical ASR SLA.
This survey therefore makes its screening assumptions explicit: **RTF <= 0.25
in every language for live ASR**, and **RTF <= 1.0 for after-call ASR**. Tables
retain RTF and p95 whole-utterance decode latency so a different product budget
can be applied without rerunning accuracy. These are ASR-only throughput limits;
they do not measure microphone-to-partial latency, VAD endpointing, speaker
separation, queues, background power or a full native app pipeline.

Voice must cover English, Mandarin, Japanese, Spanish, French and German with
one cleared model. Every observed resident-model first-sentence latency for the selected primary
voice must be <= 2 s, including its full-brief probe, and every primary full brief
must finish synthesis faster than playback (RTF < 1). Cold startup, first
informative sentence readiness and playback gaps are reported separately.
A short greeting can meet the first-sentence rule while the next sentence is
still unavailable; that limitation is not hidden by a good average RTF.
Back-ASR is an intelligibility proxy. It cannot establish warmth, naturalness,
accent preference or human-rated voice quality.

Voice quality ranking uses the equal-language macro back-ASR error of the five
primary-voice sentence samples, after coverage, licence and speed qualification.
Secondary voices and full-brief back-ASR remain separate evidence: repeating the
same text in a long brief must not overwhelm the common short-sentence comparison.
The six-step Ming follow-up is a separate configuration of the same pinned
weights, using the runtime's public `ddpm_steps=6` argument instead of default 10.

## Public corpus and reproducibility

FLEURS test subsets contain **100 utterances each** for en_us, cmn_hans_cn,
ja_jp, ko_kr, es_419, fr_fr and de_de, **700 distinct audio files / 2.2715 audio
hours** in total. Each language uses the first 100 published test rows, retaining
duplicate written prompts. The files are clean read speech; Spanish is the
Latin-American `es_419` subset. They are not meetings, code-heavy conversation,
code-switching or an independent set of 100 unique sentences. The common
CC-BY-4.0 dataset revision, references, waveform SHA-256 and durations are in
[the data receipts](survey-2026-10-multilingual/data/).
Attribution: [FLEURS, Conneau et al. (2022)](https://arxiv.org/abs/2205.12446),
distributed by [Google under CC-BY-4.0](https://huggingface.co/datasets/google/fleurs).
The retained references are the selected normalized transcription fields;
punctuation/quote parsing and scoring transformations are described below.

Every recognizer runs **without a ground-truth language hint**, batch size one,
and two excluded warmups per language. Generative language selection is automatic;
Granite CTC decodes its shared vocabulary without an explicit language-ID stage.
Its MLX adapter's constant `language="en"` result metadata is not used as a
language-ID measurement. CPU
recognizers use two threads; MLX uses a 128 MiB cache limit and an 8 GiB allocation
limit. All evaluation processes run with `nice -n 15` (effective nice 20 on this
worker) and an exclusive model lock. Downloads, load and warmup are excluded
from warm inference. Timings reflect a shared Mac, not an idle exclusive host.
RSS high-water and MLX allocation peak are different accounting views.

Unicode normalization preserves accents, Hangul syllables and kana, folds
case/width, ignores punctuation and does not hand-correct number spellings.
Chinese, Japanese and Korean use corpus CER; the four Latin-script languages
use corpus WER. Both metrics, edit counts and reference denominators are saved.
The seven-language macro score gives each language equal weight; its mixed
CER/WER units are a screening convention, not a universal error probability.
The June results and part-1 LibriSpeech/30-clip pilots are not merged into it.
The Qwen 0.6B adapter retains production's 18-second / 128-new-token bound;
other configurations process whole clips. Whisper's part-2 ASR timings disable
word timestamps and are not equivalent to its June alignment-enabled timings.

A reference audit found unbalanced literal quote fields in two German rows
and one Korean row. Default CSV quote handling joined their phonetic-transcription field
into the normalized reference, inflating the first German score. All seven
subsets were reparsed as seven literal tab-separated fields (`QUOTE_NONE`).
Audio IDs/order/hashes were unchanged; four other Korean rows changed only
quote characters. Saved Whisper hypotheses and timings were retained and rescored;
German WER changed from 17.97% to **6.47%**, and Korean CER from 3.76% to
**3.14%**, with the old and corrected edit
counts preserved in [the correction receipt](survey-2026-10-multilingual/whisper-reference-rescore.json).
The corrected references are used for every decision. This is a parsing fix,
not manual correction of numbers, proper names or recognizer output.

The [run guide](survey-2026-10-multilingual/README.md) describes environments,
commands, limits and provenance. Downloads use this worktree's isolated cache,
record `df` before each model file and reserve 16 GiB after expected bytes;
completed weights are removed. Audio is stored only in the ignored input cache
or the external listening directory and is never committed.

## Live transcription

**Recommend Qwen3-ASR 1.7B MLX 8-bit plus the mandatory speaker pack.** It has the lowest observed seven-language macro error, **4.40%**, versus 5.78% for Whisper and 8.38% for native Qwen 0.6B. It beats Whisper on each of the seven subsets. Its worst-language RTF is **0.132**, below the assumed 0.25 live screen; worst-language whole-utterance decode p95 is **2.03 s**. Speaker separation is a separate pipeline cost: part 2 does not repeat the part-1 speaker-pack smoke test.

All cells below are corpus edit rates over 100 utterances per supported language. Chinese/Japanese/Korean use CER; Latin-script languages use WER. The macro column is available only for complete seven-language runs. Unavailable cells mean the pinned artifact does not support that language. SenseVoice's measured scores do not clear its pending licence.

| Model | EN WER | ZH CER | JA CER | KO CER | ES WER | FR WER | DE WER | Seven-language macro |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Qwen3-ASR 1.7B MLX 8-bit | 4.04% | 8.23% | 5.33% | 2.63% | 2.53% | 4.95% | 3.05% | 4.40% |
| Whisper large-v3 turbo MLX | 5.60% | 9.62% | 5.76% | 3.14% | 3.18% | 6.69% | 6.47% | 5.78% |
| Qwen3-ASR 0.6B native int8 | 6.34% | 8.78% | 10.25% | 6.28% | 5.91% | 11.41% | 9.70% | 8.38% |
| FunASR-Nano CTC encoder | 15.48% | 11.33% | 11.03% | Unavailable | Unavailable | Unavailable | Unavailable | Not eligible |
| Full FunASR-Nano native int8 | 6.16% | 8.97% | 7.08% | Unavailable | Unavailable | Unavailable | Unavailable | Not eligible |
| Granite 5.0 TurboCTC 470M | 4.87% | Unavailable | 5.88% | Unavailable | 4.06% | 7.81% | 5.84% | Not eligible |
| SenseVoice Small (pending) | 9.28% | 9.89% | 7.41% | 6.60% | Unavailable | Unavailable | Unavailable | Not eligible |

All seven measured ASR configurations completed without inference exceptions. Speeds below exclude model loading and the two language warmups. Every language must pass its job's RTF screen; the rank is not based on a pooled speed average.

| Model | EN RTF | ZH | JA | KO | ES | FR | DE | Worst decode p95, ms |
|---|---:|---:|---:|---:|---:|---:|---:|---:|
| Qwen3-ASR 1.7B MLX 8-bit | 0.095 | 0.077 | 0.087 | 0.102 | 0.105 | 0.132 | 0.103 | 2031 |
| Whisper large-v3 turbo MLX | 0.189 | 0.168 | 0.143 | 0.150 | 0.162 | 0.195 | 0.142 | 2129 |
| Qwen3-ASR 0.6B native int8 | 0.162 | 0.177 | 0.154 | 0.175 | 0.190 | 0.186 | 0.177 | 4241 |
| FunASR-Nano CTC encoder | 0.029 | 0.034 | 0.032 | Unavailable | Unavailable | Unavailable | Unavailable | 745 |
| Full FunASR-Nano native int8 | 0.127 | 0.139 | 0.135 | Unavailable | Unavailable | Unavailable | Unavailable | 3676 |
| Granite 5.0 TurboCTC 470M | 0.007 | Unavailable | 0.007 | Unavailable | 0.007 | 0.007 | 0.007 | 133 |
| SenseVoice Small (pending) | 0.026 | 0.023 | 0.024 | 0.023 | Unavailable | Unavailable | Unavailable | 535 |

Live qualification is an **ASR-throughput recommendation**, not an end-to-end microphone/partial-result claim. The 1.7B MLX worker, streaming segmentation, queues, endpointing, power and concurrent app memory still require native integration measurements. Native Qwen 0.6B's 18-second / 128-token bound remains part of its tested configuration. Parakeet v2 is English-only; MiMo declares Chinese/English/Cantonese and has the part-1 bounded warmup failure. Neither has coverage of the five added languages, so their historical results are not mixed into this common-corpus table.

## After-call transcription

**Recommend the same Qwen3-ASR 1.7B MLX 8-bit artifact for after-call recognition.** It is the accuracy winner among all completed seven-language models under RTF <= 1.0. The setup catalogue should share its verified files across the two job rows. Rereading identical clean audio with the same recognizer is not an independently measured accuracy gain: after-call resegmentation/context and meeting diarization remain separate work. The existing disabled after-call choice can still retain the live transcript.

| Seven-language candidate | Mixed CER/WER macro | All-CER sensitivity macro | Worst RTF | Worst decode p95, ms | Download / installed MB | RSS high-water / Metal peak MB |
|---|---:|---:|---:|---:|---:|---:|
| Qwen3-ASR 1.7B MLX 8-bit | 4.40% | 3.34% | 0.132 | 2031 | 2467.9 / 2467.9 | 2553.4 / 3231.6 |
| Whisper large-v3 turbo MLX | 5.78% | 4.14% | 0.195 | 2129 | 1614.0 / 1614.0 | 1863.5 / 2144.8 |
| Qwen3-ASR 0.6B native int8 | 8.38% | 6.01% | 0.190 | 4241 | 1000.1 / 1000.1 | 3488.9 / 0.0 |

Using CER for all seven languages leaves the same Qwen / Whisper / native-Qwen ordering; the conclusion does not depend solely on mixing CJK CER and Latin WER. MB is decimal. RSS and Metal allocation overlap and must not be added. These are small, duplicated-prompt read-speech subsets; point-estimate improvements are not a meeting-quality or statistical-significance guarantee.

Mandarin (8.23% CER) and Japanese (5.33% CER) are the recommended model's highest-error languages here. The per-language alternatives below are diagnostics for weak-language choices, **not automatic model routing**. Each alternative is cleared, supports the named language and meets that job's speed budget on that subset; partial-language models can appear as alternatives while remaining ineligible for a global default.

| Language | Qwen 1.7B error | Best live alternative, error / RTF | Best after-call alternative, error / RTF |
|---|---:|---|---|
| EN | 4.04% | Granite 5.0 TurboCTC 470M, 4.87% / 0.007 | Granite 5.0 TurboCTC 470M, 4.87% / 0.007 |
| ZH | 8.23% | Qwen3-ASR 0.6B native int8, 8.78% / 0.177 | Qwen3-ASR 0.6B native int8, 8.78% / 0.177 |
| JA | 5.33% | Whisper large-v3 turbo MLX, 5.76% / 0.143 | Whisper large-v3 turbo MLX, 5.76% / 0.143 |
| KO | 2.63% | Whisper large-v3 turbo MLX, 3.14% / 0.150 | Whisper large-v3 turbo MLX, 3.14% / 0.150 |
| ES | 2.53% | Whisper large-v3 turbo MLX, 3.18% / 0.162 | Whisper large-v3 turbo MLX, 3.18% / 0.162 |
| FR | 4.95% | Whisper large-v3 turbo MLX, 6.69% / 0.195 | Whisper large-v3 turbo MLX, 6.69% / 0.195 |
| DE | 3.05% | Granite 5.0 TurboCTC 470M, 5.84% / 0.007 | Granite 5.0 TurboCTC 470M, 5.84% / 0.007 |

The production [Qwen backend](../wails-gui/asr_qwen3.go) still downloads the 0.6B native artifact. The measured 1.7B recommendation agrees with the setup handoff's name but requires a different runtime bridge. This evaluation updates the draft and evidence; it does not replace the app's engine or claim a running live implementation.

## Voice: first complete sentence and full brief

**Recommend Ming, 6 steps for the tested primary voice profile.** It is the lowest-back-ASR-error cleared configuration meeting all six-language speed gates: primary short-sentence macro error **0.30%**, maximum observed warm first-sentence starts **0.654–1.043 s**, and full-brief RTFs **0.467–0.689**. This is the best observed intelligibility proxy among the qualifying configurations, not a human-rated naturalness result.

The selected Ming profile reuses the existing pinned weights. Setup must record **ddpm_steps=6**, the measured per-language voices, explicit `lang_code`, a resident worker and sentence-by-sentence delivery. The default ten-step benchmark does not meet full-brief throughput on this shared host.

The common brief repeats five complete sentences to reach 300 normalized words (EN 308, ES 301, FR 300, DE 301) or at least 552 normalized characters (ZH 552, JA 559). These are five unique prompts per language, not hundreds of independent quality observations. The opening is a greeting, such as "Good morning." The first informative sentence is longer; its readiness is the cumulative time until both opening sentences are available. Full-brief WAVs concatenate sentence PCM without inference pauses; the traces separately predict playback gaps.

Max first includes the three repeated warm probes, saved greeting and full-brief start for the selected primary voice. Qualification uses unrounded values. All latency columns are seconds. Full synthesis must be shorter than that model's actual audio playback duration.

| Configuration | Language | Max first sentence | First informative ready | Full synthesis | Audio playback | RTF | Maximum playback gap |
|---|---|---:|---:|---:|---:|---:|---:|
| Ming, 6 steps | EN | 0.654 | 2.72 | 57.4 | 102.5 | 0.560 | 1.03 |
| Ming, 6 steps | ZH | 0.917 | 3.50 | 82.7 | 120.1 | 0.689 | 1.30 |
| Ming, 6 steps | JA | 1.043 | 3.76 | 57.1 | 89.3 | 0.639 | 1.29 |
| Ming, 6 steps | ES | 0.994 | 4.32 | 74.1 | 111.2 | 0.667 | 2.08 |
| Ming, 6 steps | FR (original UK_Charlotte) | 0.837 | 4.14 | 69.8 | 99.9 | 0.698 | 2.26 |
| Ming, 6 steps | DE | 0.992 | 4.05 | 85.7 | 127.0 | 0.675 | 1.72 |
| Ming, 6 steps | FR (selected US_James) | 0.933 | 2.86 | 48.8 | 104.6 | 0.467 | 1.01 |
| Chatterbox v3 FP16 parameters | EN | 1.641 | 5.28 | 89.2 | 102.2 | 0.873 | 2.26 |
| Chatterbox v3 FP16 parameters | ZH | 1.618 | 4.78 | 116.4 | 132.4 | 0.879 | 1.89 |
| Chatterbox v3 FP16 parameters | JA | 1.721 | 4.55 | 75.2 | 80.5 | 0.935 | 1.18 |
| Chatterbox v3 FP16 parameters | ES | 1.750 | 6.17 | 103.3 | 114.8 | 0.900 | 3.02 |
| Chatterbox v3 FP16 parameters | FR | 1.620 | 6.82 | 108.1 | 105.9 | 1.021 | 4.08 |
| Chatterbox v3 FP16 parameters | DE | 1.593 | 5.73 | 116.8 | 113.7 | 1.028 | 2.96 |
| Ming, 10 steps | EN | 1.463 | 6.65 | 112.9 | 110.6 | 1.021 | 3.98 |
| Ming, 10 steps | ZH | 1.256 | 4.72 | 120.6 | 111.2 | 1.085 | 2.27 |
| Ming, 10 steps | JA | 1.662 | 6.72 | 98.3 | 90.4 | 1.088 | 3.69 |
| Ming, 10 steps | ES | 1.617 | 6.94 | 119.8 | 116.6 | 1.028 | 3.85 |
| Ming, 10 steps | FR | 1.387 | 7.49 | 124.4 | 118.7 | 1.048 | 4.92 |
| Ming, 10 steps | DE | 1.614 | 7.64 | 142.4 | 133.7 | 1.065 | 4.63 |
| Melo zh_en | EN | 0.510 | 2.78 | 55.9 | 105.4 | 0.530 | 1.35 |
| Melo zh_en | ZH | 0.485 | 2.14 | 52.7 | 107.3 | 0.492 | 0.96 |
| Kokoro MLX (GPL) | EN | 0.140 | 0.52 | 9.6 | 117.9 | 0.081 | 0.00 |
| Kokoro MLX (GPL) | ZH | 0.188 | 0.62 | 15.1 | 195.3 | 0.077 | 0.00 |
| Kokoro MLX (GPL) | JA | 0.179 | 0.61 | 9.1 | 116.1 | 0.079 | 0.00 |
| Kokoro MLX (GPL) | ES | 0.122 | 0.59 | 9.8 | 116.3 | 0.085 | 0.00 |
| Kokoro MLX (GPL) | FR | 0.132 | 0.60 | 9.9 | 120.6 | 0.082 | 0.00 |
| Chatterbox v3 F32 | EN | 2.269 | 7.99 | 135.2 | 101.6 | 1.330 | 4.24 |
| Chatterbox v3 F32 | ZH | 2.284 | 7.97 | 175.8 | 134.2 | 1.310 | 4.28 |
| Chatterbox v3 F32 | JA | 2.617 | 7.06 | 105.6 | 80.5 | 1.312 | 2.76 |
| Chatterbox v3 F32 | ES | 2.252 | 8.13 | 137.7 | 115.8 | 1.189 | 4.60 |
| Chatterbox v3 F32 | FR | 1.765 | 7.95 | 135.7 | 106.6 | 1.273 | 5.17 |
| Chatterbox v3 F32 | DE | 1.960 | 7.75 | 142.4 | 112.3 | 1.268 | 4.55 |
| CosyVoice 3 8-bit | EN | 1.949 | 5.53 | 113.1 | 126.8 | 0.892 | 2.78 |
| CosyVoice 3 8-bit | ZH | 3.013 | 6.59 | 141.1 | 152.6 | 0.924 | 0.40 |
| CosyVoice 3 8-bit | JA | 2.952 | 7.29 | 87.4 | 111.2 | 0.786 | 1.80 |
| CosyVoice 3 8-bit | ES | 2.058 | 7.52 | 114.9 | 149.2 | 0.770 | 4.33 |
| CosyVoice 3 8-bit | FR | 2.000 | 6.42 | 109.5 | 134.1 | 0.816 | 3.42 |
| CosyVoice 3 8-bit | DE | 2.096 | 5.97 | 114.8 | 129.9 | 0.884 | 2.65 |

The FP16 Chatterbox profile misses the full-brief throughput gate in FR RTF 1.021, DE RTF 1.028. A marginal miss is still a miss under the stated unrounded rule; it is not relabelled as a passing global voice.

Six-step Ming passes all measured speed gates: maximum warm first starts 0.654–1.043 s and selected-voice brief RTFs 0.467–0.689. Its English brief falls from 112.9 s at ten steps to 57.4 s at six steps with identical input text. Audio duration also changes (110.6 s versus 102.5 s), so synthesis time and RTF are both retained. Its first informative sentence takes 2.72–4.32 s. An under-two-second greeting is not a guarantee for an arbitrary sentence or uninterrupted playback.

Chatterbox's distributed F32 run misses throughput. The follow-up retains those exact source files and applies public `Module.set_dtype(mx.float16)` to model/codec floating parameters, casts cached floating conditioning arrays, and preserves integer tokens/lengths. The effective emotion scalar is its F16 representation (requested 0.1); matching that scalar prevents the public generator from replacing it with F32. The [precision receipt](survey-2026-10-multilingual/chatterbox-fp16-precision.json) asserts parameter types. Intermediates may still promote to F32 for stability; this is not a claim that every operation runs in FP16. The same public cached-conditioning API is used in both precision runs. Optional pkuseg segmentation is absent; v3's NFKD/full-case route avoids the legacy Japanese GPL G2P.

CosyVoice's tested zero-shot profile supplies the matching English synthetic reference transcript for every target language. Its separate reference-only cross-lingual API mode is unmeasured. This profile finishes all briefs faster than playback but misses the primary first-sentence target in several languages with its published frontend, which recomputes reference features per call. An unmeasured cached CosyVoice adapter is not presented as a successful result. Melo zh_en is fast but covers only two requested languages in one artifact. Kokoro MLX is fast in five requested languages, lacks German and remains a GPL-component comparison while acceptance is false.

The two primary-voice quality measures below use Qwen3-ASR 1.7B 8-bit in a separate process, automatic language selection, low-energy roughly 30-second chunking and up to 1024 new tokens per chunk. Capped verifier output is rejected. Errors combine synthesis, verifier mistakes and orthographic variation (including number and script choices); they are intelligibility proxies, not direct pronunciation or MOS scores.

**Five common sentences per voice:** cells are primary / alternate corpus error; EN/ES/FR/DE WER, ZH/JA CER. A single available voice is marked explicitly.

| Configuration | EN WER | ZH CER | JA CER | ES WER | FR WER | DE WER |
|---|---:|---:|---:|---:|---:|---:|
| Ming, 6 steps | 0.00% / 4.55% | 0.00% / 0.00% | 1.82% / 3.64% | 0.00% / 17.02% | 0.00% / 10.64% | 0.00% / 2.33% |
| Chatterbox v3 FP16 parameters | 0.00% / 0.00% | 0.00% / 0.00% | 41.82% / 59.09% | 0.00% / 0.00% | 0.00% / 2.13% | 0.00% / 0.00% |
| Ming, 10 steps | 0.00% / 0.00% | 0.00% / 0.00% | 0.91% / 0.91% | 0.00% / 0.00% | 0.00% / 0.00% | 0.00% / 0.00% |
| Melo zh_en | 18.18% (one voice) | 14.75% (one voice) | Unavailable | Unavailable | Unavailable | Unavailable |
| Kokoro MLX (GPL) | 0.00% / 0.00% | 0.00% / 0.00% | 0.91% / 0.91% | 0.00% / 0.00% | 0.00% (one voice) | Unavailable |
| Chatterbox v3 F32 | 0.00% / 0.00% | 0.00% / 0.00% | 44.55% / 56.36% | 0.00% / 0.00% | 0.00% / 6.38% | 0.00% / 0.00% |
| CosyVoice 3 8-bit | 0.00% / 0.00% | 4.92% / 0.00% | 75.45% / 61.82% | 8.51% / 4.26% | 4.26% / 4.26% | 6.98% / 34.88% |

The selected six-step Ming voices have zero short-sample back-ASR errors in EN/ZH/ES/FR/DE and 1.82% Japanese CER on this small prompt set. The ten-step control has lower Japanese error (0.91%) but misses full-brief throughput in every language. Chatterbox F32/FP16 and the tested CosyVoice zero-shot route have particularly weak Japanese intelligibility with these English synthetic references (41.82–75.45% primary short-sample CER). Those are results for these pinned routes and reference voices, not proof that all native-reference or other API modes have the same quality.

Reference denominators per voice are EN 44 words, ZH 61 characters, JA 110 characters, ES 47 words, FR 47 words, DE 43 words. The same five prompts are used for every supporting model/voice. Alternate-voice quality does not inherit the primary full-brief speed qualification. Exact hypotheses, chunk boundaries and edit counts are retained in [voice-back-asr.json](survey-2026-10-multilingual/runs/voice-back-asr.json).

**Full-brief primary-voice back-ASR:** these repeated-text results are separate checks, not extra independent prompt coverage.

| Configuration | EN WER | ZH CER | JA CER | ES WER | FR WER | DE WER |
|---|---:|---:|---:|---:|---:|---:|
| Ming, 6 steps | 0.00% | 0.18% | 1.43% | 0.00% | 3.33% | 1.66% |
| Chatterbox v3 FP16 parameters | 1.95% | 0.00% | 41.50% | 1.66% | 1.67% | 0.00% |
| Ming, 10 steps | 1.30% | 0.00% | 0.89% | 0.00% | 0.00% | 0.00% |
| Melo zh_en | 7.14% | 0.54% | Unavailable | Unavailable | Unavailable | Unavailable |
| Kokoro MLX (GPL) | 0.00% | 0.00% | 0.89% | 0.00% | 1.67% | Unavailable |
| Chatterbox v3 F32 | 1.95% | 0.00% | 45.80% | 0.66% | 1.67% | 0.00% |
| CosyVoice 3 8-bit | 0.00% | 5.43% | 38.82% | 9.30% | 5.67% | 2.33% |

Quality ranking uses the equal-language macro of primary short-sentence errors after coverage/licence/speed gates. Exact ties prefer lower mean full-brief RTF. The completed eligible ranking is Ming, 6 steps 0.30%. Initial default voices were chosen before scoring. An explicit French follow-up selected US_James after UK_Charlotte scored 10.64% and US_James 0% WER on the five common sentences. New greeting probes and a complete French brief validate speed before that switch; the original primary data remain in [the immutable original-run snapshot](survey-2026-10-multilingual/ming_fast-primary-before-fr-followup.json). This is disclosed voice selection on the pilot prompts, not independent held-out quality evidence.

Primary / alternate voices are UK_Charlotte / US_James for Ming in EN/JA/ES/DE and 灵小柔 / 灵信远 in ZH. The selected French six-step voice is US_James, with UK_Charlotte retained as the measured alternate; the ten-step control keeps its original UK_Charlotte primary. Melo's pinned bilingual checkpoint exposes one speaker, `0`. Kokoro uses af_heart / am_adam (EN), zf_xiaobei / zm_yunxi (ZH), jf_alpha / jm_kumo (JA), ef_dora / em_alex (ES), and its only French voice ff_siwis. Chatterbox and CosyVoice use synthetic_charlotte / synthetic_james across all languages, from the same hashed first-survey Ming samples. These English synthetic references test cross-language transfer and can carry accent/prosody; they are not real-person clones or native-speaker preference tests.

Model loading and first excluded per-profile calls remain separate below. They are not end-to-end application cold-start guarantees. RSS and Metal allocation views overlap; do not add them. High-water includes loading/warmups and FP16 conversion, not just steady-state generation. MB is decimal.

| Configuration | Model load, s | First excluded call range, s | RSS high-water, MB | Metal peak, MB |
|---|---:|---:|---:|---:|
| Ming, 6 steps | 15.75 | 0.57–1.07 | 2372.9 | 1858.5 |
| Chatterbox v3 FP16 parameters | 5.62 | 1.20–3.58 | 3372.9 | 4842.0 |
| Ming, 10 steps | 20.96 | 0.85–2.33 | 2262.6 | 1880.8 |
| Melo zh_en | 1.94 | 0.49–0.54 | 667.1 | 0.0 |
| Kokoro MLX (GPL) | 0.74 | 0.13–21.02 | 1088.8 | 1679.8 |
| Chatterbox v3 F32 | 3.51 | 1.57–6.39 | 1164.9 | 4858.1 |
| CosyVoice 3 8-bit | 7.67 | 1.77–6.41 | 3071.0 | 3778.1 |

The later French US_James follow-up loaded the same model in 19.57 s and recorded its own process/resource receipt. Its full brief took 48.8 s for 104.6 s of audio (RTF 0.467), with 3.33% full-brief WER. The follow-up occurred later on the shared host; it validates the speed budget and does not establish a causal voice-speed advantage. The initial configuration resource rows above remain distinct from that follow-up.

No device audio was played. The readiness measurement ends when complete sentence PCM is materialized, before peak normalization, file writes and the sound-device queue. Combined ASR/TTS memory, sustained background power, real brief diversity and native-speaker listening remain outside this standalone measurement.

## Screen OCR

Thirty CC0 generated public JPEG screens cover Japanese, Korean, Spanish,
French and German across 14/16/20/24-pixel text, light/dark themes and 1x/2x
resolution. They are controlled PIL fixtures, not captures of the user's screen
or a substitute for native Retina/browser documents. Case and punctuation are
preserved in OCR scoring; whitespace is ignored and fullwidth ASCII is folded,
as in the first survey. [Fixtures and references](survey-2026-10-multilingual/ocr-fixtures/)
are committed together.

| Configuration | Japanese character accuracy | Korean | Spanish | French | German |
|---|---:|---:|---:|---:|---:|
| App-equivalent Chinese-first request | 39.05% | 14.63% | 95.24% | 94.11% | 95.70% |
| Vision accurate, automatic language detection + supported languages | 99.67% | 100.00% | 99.84% | 99.84% | 99.55% |
| Vision accurate, known-language oracle | 99.51% | 100.00% | 99.84% | 99.84% | 99.55% |

**Retain Apple Vision, but change the language configuration before claiming
multilingual screen reading.** The weak Japanese/Korean result comes from the
app's Chinese-first request. Automatic language detection on the same engine
removes the failure on this corpus. A known-language oracle confirms that
replacing the OCR weights is unnecessary here. No alternative model is proposed
as an upgrade on languages where correctly configured Vision is already strong.
This evaluation changes only the standalone benchmark worker, not the app.
The app-equivalent request resolves `zh-Hans`, `zh-Hant`, `en-US`, `ko-KR`,
`ja-JP` in that order on this host, following
[`ocrDefaultLanguagePrefs`](../wails-gui/ocr_breadcrumb.go) and
[`ResolveLanguages`](../wails-gui/internal/platform/ocr/ocr.go).

Initial automatic-detection calls include language-specific cold work: mean
latencies were 186.5 / 1058.8 / 640.5 / 99.8 / 90.8 ms for JA/KO/ES/FR/DE.
The known-language request measured 145.5 / 127.4 / 85.1 / 80.8 / 78.8 ms.
Those numbers are distinct configurations, not evidence that automatic language
detection always runs at the oracle's speed. A separate warm-language probe
retains the first call per language before timing the six scored variants.

With one excluded warmup per language, automatic detection measured mean
latencies of **147.6 / 136.7 / 85.9 / 89.8 / 89.4 ms** for JA/KO/ES/FR/DE,
with the same 99.55–100% accuracy range. The first-language calls and all
30 scored frames are retained in [the warm-language receipt](survey-2026-10-multilingual/runs/ocr-multilingual-warm.json).

The native API reports 30 supported locale identifiers on this macOS version.
They were queried separately for `VNRecognizeTextRequest` and
`RecognizeDocumentsRequest`; consumers must query their actual OS/request rather
than hardcode this host's list. Document-language capability is not a new
multilingual document-accuracy measurement. Apple remains a system API under
macOS/SDK terms, with zero redistributed model bytes. [Apple text recognition API](https://developer.apple.com/documentation/vision/vnrecognizetextrequest),
[document recognition API](https://developer.apple.com/documentation/vision/recognizedocumentsrequest).

## Artifact languages, licences and setup changes

Language lists use ISO/BCP-47 identifiers. The seven ASR test languages are
English (`en`), Mandarin (`zh`), Japanese (`ja`), Korean (`ko`), Spanish (`es`),
French (`fr`) and German (`de`). Declared coverage and measured coverage are
separate fields in the catalogue; an unmeasured language is not an accuracy
claim. The full source-backed lists are:

- **Qwen3-ASR 0.6B / 1.7B** — `zh`, `en`, `yue`, `ar`, `de`, `fr`, `es`, `pt`, `id`, `it`, `ko`, `ru`, `th`, `vi`, `ja`, `tr`, `hi`, `ms`, `nl`, `sv`, `da`, `fi`, `pl`, `cs`, `fil`, `fa`, `el`, `hu`, `mk`, `ro`. Apache-2.0; cleared. Seven FLEURS languages measured; 22 Chinese dialect/accent labels are additional publisher categories, not 22 extra languages. [Coverage source](https://huggingface.co/Qwen/Qwen3-ASR-1.7B/blob/7278e1e70fe206f11671096ffdd38061171dd6e5/README.md).
- **Whisper large-v3 turbo** — `en`, `zh`, `de`, `es`, `ru`, `ko`, `fr`, `ja`, `pt`, `tr`, `pl`, `ca`, `nl`, `ar`, `sv`, `it`, `id`, `hi`, `fi`, `vi`, `he`, `uk`, `el`, `ms`, `cs`, `ro`, `da`, `hu`, `ta`, `no`, `th`, `ur`, `hr`, `bg`, `lt`, `la`, `mi`, `ml`, `cy`, `sk`, `te`, `fa`, `lv`, `bn`, `sr`, `az`, `sl`, `kn`, `et`, `mk`, `br`, `eu`, `is`, `hy`, `ne`, `mn`, `bs`, `kk`, `sq`, `sw`, `gl`, `mr`, `pa`, `si`, `km`, `sn`, `yo`, `so`, `af`, `oc`, `ka`, `be`, `tg`, `sd`, `gu`, `am`, `yi`, `lo`, `uz`, `fo`, `ht`, `ps`, `tk`, `nn`, `mt`, `sa`, `lb`, `my`, `bo`, `tl`, `mg`, `as`, `tt`, `haw`, `ln`, `ha`, `ba`, `jw`, `su`, `yue`. MIT; cleared. All seven FLEURS languages measured; 100 language tokens include Cantonese. [Coverage source](https://github.com/openai/whisper/blob/main/whisper/tokenizer.py).
- **SenseVoice Small** — `zh`, `en`, `ja`, `ko`, `yue`. Pending FunASR model terms. Four test languages supported; Spanish/French/German unavailable. The model has five declared languages, not six. [Coverage source](https://huggingface.co/FunAudioLLM/SenseVoiceSmall).
- **Original FunASR-Nano encoder / full decoder** — `zh`, `en`, `ja`. Apache-2.0; cleared. Original three-language checkpoint; the separate 31-language MLT model is a different artifact. [Coverage source](https://huggingface.co/FunAudioLLM/Fun-ASR-Nano-2512/blob/272c57b82523ada6fd87095e955f8e29100979ab/README.md).
- **Granite Speech 5.0 TurboCTC 470M** — `en`, `fr`, `de`, `es`, `pt`, `it`, `ja`. Apache-2.0; cleared. October 21 multilingual revision; Mandarin/Korean input ASR are unavailable. [Coverage source](https://huggingface.co/ibm-granite/granite-speech-5.0-470m-turboctc/blob/1dd23f46f6d4e6eae2531982ba2e33220086dd97/README.md).
- **MiMo V2.5 ASR** — `zh`, `en`, `yue`. MIT original / Apache conversion; cleared. No five-language expansion is claimed; part 1 has a bounded warmup failure. Cantonese is in the publisher declaration but not the conversion tag. [Coverage source](https://huggingface.co/XiaomiMiMo/MiMo-V2.5-ASR/blob/98641d537df521ac6df05f74090475694d9510b7/README.md).
- **Parakeet TDT v2** — `en`. CC-BY-4.0; cleared. English-only historical option; do not attach v3 languages to v2. [Coverage source](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v2).
- **Ming 3.0 TTS** — `zh`, `en`, `ja`, `ko`, `es`, `fr`, `de`. MIT; cleared. Six voice languages measured here; Korean declared but not part of the requested voice test. [Coverage source](https://huggingface.co/inclusionAI/Ming-3.0-tts-0.5B).
- **Melo zh_en** — `zh`, `en`. MIT; cleared. Pinned single-speaker bilingual artifact. Other family languages need separate checkpoints; no German family checkpoint is claimed. [Coverage source](https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/vits-melo-tts-zh_en.tar.bz2).
- **Kokoro MLX v1.0** — `en`, `es`, `fr`, `hi`, `it`, `ja`, `pt`, `zh`. Apache weights + GPL eSpeak; restricted. Five requested languages supported, no German. Nine runtime locale codes represent eight languages (two English locales). [Coverage source](https://huggingface.co/hexgrad/Kokoro-82M).
- **Kokoro v0.19 English** — `en`. Apache weights + GPL eSpeak; restricted. Historical control; not rerun as a new multilingual checkpoint. [Coverage source](https://huggingface.co/hexgrad/Kokoro-82M).
- **Kokoro v1.1 zh/en** — `zh`, `en`. Apache weights + GPL eSpeak; restricted. Historical bilingual artifact; do not inherit the v1.0 multilingual voice list. [Coverage source](https://huggingface.co/hexgrad/Kokoro-82M-v1.1-zh).
- **Chatterbox multilingual v3** — `ar`, `da`, `de`, `el`, `en`, `es`, `fi`, `fr`, `he`, `hi`, `it`, `ja`, `ko`, `ms`, `nl`, `no`, `pl`, `pt`, `ru`, `sv`, `sw`, `tr`, `zh`. MIT + Apache speech codec; cleared evaluated route. Six requested languages tested using the v3 NFKD/full-case frontend. Optional legacy frontends are outside this route. [Coverage source](https://huggingface.co/mlx-community/chatterbox-multilingual-v3/blob/03565773edd72e949572557597af8063bb49a18a/README.md).
- **CosyVoice 3** — `zh`, `en`, `ja`, `ko`, `de`, `es`, `fr`, `it`, `ru`. Apache weights / MIT MLX port; cleared evaluated route. Six requested languages tested; a reference voice and the separate speech codec are required. [Coverage source](https://huggingface.co/FunAudioLLM/Fun-CosyVoice3-0.5B-2512/blob/29e01c4e8d000f4bcd70751be16fa94bf3d85a18/README.md).
- **Apple Vision text** — `en-US`, `fr-FR`, `it-IT`, `de-DE`, `es-ES`, `pt-BR`, `zh-Hans`, `zh-Hant`, `yue-Hans`, `yue-Hant`, `ko-KR`, `ja-JP`, `ru-RU`, `uk-UA`, `th-TH`, `vi-VT`, `ar-SA`, `ars-SA`, `tr-TR`, `id-ID`, `cs-CZ`, `da-DK`, `nl-NL`, `no-NO`, `nn-NO`, `nb-NO`, `ms-MY`, `pl-PL`, `ro-RO`, `sv-SE`. macOS/SDK system terms. Exact locale identifiers queried on this host; actual OCR quality measured for JA/KO/ES/FR/DE here, EN/ZH in part 1. [Coverage source](https://developer.apple.com/documentation/vision/vnrecognizetextrequest/supportedrecognitionlanguages()).
- **Apple Vision documents** — `en`, `fr`, `it`, `de`, `es`, `pt`, `zh`, `zh-TW`, `yue-CN`, `yue`, `ko`, `ja`, `ru`, `uk`, `th`, `vi-VT`, `ar-SA`, `ars`, `tr`, `id`, `cs`, `da`, `nl`, `no`, `nn`, `nb`, `ms`, `pl`, `ro`, `sv`. macOS/SDK system terms. Separately queried capability list; part-2 multilingual document accuracy is not claimed. [Coverage source](https://developer.apple.com/documentation/vision/recognizedocumentsrequest).
- **PP-OCRv6 tiny** — `zh-Hans`, `zh-Hant`, `en`, `fr`, `de`, `it`, `es`, `pt`, `nl`, `pl`, `ro`, `cs`, `sv`, `no`, `da`, `fi`, `hu`, `tr`, `vi`, `id`, `ms`, `az`, `af`, `bs`, `hr`, `cy`, `et`, `ga`, `is`, `ku`, `lt`, `lv`, `mt`, `mi`, `oc`, `sk`, `sl`, `sq`, `sw`, `tl`, `uz`, `la`, `sr-Latn`, `ca`, `eu`, `gl`, `lb`, `rm`, `qu`. Apache-2.0; cleared. 49 advertised language/script entries; this tiny model excludes Japanese and Korean. [Coverage source](https://github.com/PaddlePaddle/PaddleOCR/blob/main/docs/version3.x/algorithm/PP-OCRv6/PP-OCRv6.en.md).

The speaker pack, MiMo audio tokenizer and Chatterbox/CosyVoice speech codecs
are support components rather than independent language recognizers. Their
`languages` lists are empty with explicit support semantics. The English Kokoro
G2P support asset lists `en`. The disabled after-call choice inherits the chosen
live transcript instead of claiming its own languages. Language metadata is
required for every manifest entry, including these cases.

The separate [31-language Fun-ASR-MLT-Nano checkpoint](https://huggingface.co/FunAudioLLM/Fun-ASR-MLT-Nano-2512)
was reviewed but not substituted for original Nano: its declared list omits
Spanish, French and German. Granite's measured 470M TurboCTC checkpoint is also
distinct from Granite's 2B model. Melo's evaluated single `zh_en` artifact does
not acquire Japanese/Spanish/French coverage from separately downloaded family
checkpoints. No unsupported family-language paths are represented as local
measurements or as a single-model multilingual default.

SenseVoice remains **pending**, with `non_commercial: null`, and cannot become
a default through an inferred permission. Kokoro remains **restricted** by its
GPL component. `gplComplianceAccepted` stays **false**. Even explicit GPL
acceptance would not give Kokoro German coverage or change the multilingual
winner automatically. Ordinary attribution/licence notices remain necessary
for cleared artifacts. The shared evaluation environment is not a shipping
runtime bill of materials.

The draft changes are concrete and remain evaluation metadata, not an installer or production engine change:

1. **Use one verified Qwen 1.7B artifact for both ASR jobs.** Add `qwen17-live` with `artifact_id: qwen17` and the live-only `speaker-pack` dependency. Keep `qwen17` for after-call. Deduplicate downloads/storage by artifact identity and file SHA-256; do not charge or fetch the same 2.47 GB twice. Keep the disabled after-call option. The per-language alternatives are diagnostic choices, not automatic model routing.
2. **Keep Ming's weights and change the measured generation profile.** The `ming_tts` row records `evaluation_profile: ming_fast`, exact six-step settings, tested primary voices, explicit `lang_code`, `resident_model_required` and `sentence_streaming_required`. A matching bridge must perform those operations; downloading the unchanged weights with default ten-step whole-brief generation does not reproduce the speed result. Warm the worker asynchronously without blocking setup's initial screen. Arbitrary first-sentence length, device playback, diverse real briefs and concurrent ASR/TTS resource use still need app measurements.
3. **Add the reviewed alternatives and visible dependencies.** Add Granite 5.0 TurboCTC 470M, Chatterbox multilingual v3, CosyVoice 3, their separate S3 speech codecs, and Kokoro's Japanese UniDic-lite asset. Chatterbox's selected comparison profile uses public FP16 parameter conversion; F32 and FP16 results remain separate evidence. Its French and German full briefs miss RTF < 1. The Chatterbox/CosyVoice rows explicitly require reference audio (and the tested CosyVoice zero-shot profile a matching transcript). The evaluated synthetic references are external, hashed samples, not a hosted preset pack. An installer must announce and pin a reference pack or ask for an explicit user reference; no invented URL or hidden download is added. Separate Melo family checkpoints are not presented as one multilingual artifact.
4. **Make language capability and evidence separate.** Every entry has `languages`, `language_scope`, source evidence and `tested_languages`. ASR defaults declare 30 languages, while Ming declares seven and has six voice languages measured here. Supporting codecs/speaker assets and disabled choices do not claim independent language accuracy. Apple text/document lists are separate OS capability snapshots; query the actual OS/request at runtime.
5. **Keep the licence gates.** `gplComplianceAccepted` stays false. SenseVoice remains unselectable/pending with `non_commercial: null`; Kokoro stays restricted. Explicit GPL acceptance can permit the comparison option but does not add German support or make Kokoro the global default. Package cleared routes separately from the shared survey environment's unused GPL components, and retain ordinary notices. The Japanese dictionary chooses the published BSD-3-Clause data option with its MIT wrapper; it does not remove Kokoro's eSpeak restriction.
6. **Apply the Vision request change in future app work.** The draft specifies accurate recognition, automatic language detection and querying supported languages. The benchmark fixes the request configuration on public fixtures; the app's current Chinese-first configuration is unchanged by this PR.

The generated catalogue remains a draft. New reference-voice packaging and the native worker bridges are explicit shipping gaps, rather than implied permission or completed app integration.


## Verification and limits

All **46 tests pass** (`python -m unittest discover -s asr-eval -p 'test_*.py'`). Both the original survey validator and the multilingual validator pass. The latter recomputes all **3,600 ASR observations**, selected-model rankings, **393 voice back-ASR records**, all **120 OCR observations**, language declarations, sentence timing traces and the French follow-up's immutable/append-only provenance.

[`samples-2.json`](samples-2.json) indexes **393 generated voice files and two synthetic reference files** under `~/src/model-survey-samples-2/`. All **395 WAVs** pass byte-size, SHA-256 and PCM-header/duration verification; the directory contains no missing or unindexed WAVs. Audio is external and no audio is committed. The original 34-sample survey remains unchanged.

The default manifest regenerates deterministically with `gplComplianceAccepted: false`. An explicit-acceptance temporary variant also passes the original validator and retains the same multilingual defaults; the tests still reject pending or unaccepted restricted recommendations. Repository pre-commit checks are run after final cleanup and before the commit.

All inference processes enforce nice >=10 and ran at effective nice20 here. An exclusive model lock keeps them sequential. The 183 saved download/preflight checks record a minimum **17.91 GiB free**, with a **16 GiB post-download reserve** enforced above the requested 15 GB floor. The [disk receipts](survey-2026-10-multilingual/disk-checks.jsonl) retain the actual `df` output. This run's model/input cache, virtual environments, bytecode and temporary source snapshots are removed before handoff; the pinned inventories, requirements, measurements and external listening samples remain.

The results are small public read-speech and generated-text pilots on one shared M4 host. They do not cover meeting noise, spontaneous/code-switched speech, all declared languages, arbitrary first-sentence lengths, naturalness preferences, production cold start, concurrent memory/power or a native app pipeline. The French voice selection reuses pilot prompts and is disclosed as tuning. No Isoform app was launched, and neither private agent directory was read. Production Go/frontend checks are outside these evaluation-only changes.
