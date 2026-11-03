# Local models for transcription, voice and OCR — October 2026

Survey date: 2026-10-23. Host: Apple M4 Mac mini, 10 CPU cores, 16 GB unified
memory, macOS 26.2 (25C56). This is an evaluation-only change: no Isoform app
code, app settings or private recordings were used, and the app was not launched.

| Job | Recommendation | Why | Setup handoff impact |
|---|---|---|---|
| Live transcription | **Qwen3-ASR 0.6B int8 + speaker pack** | Strongest bilingual accuracy among the licence-cleared native candidates; accept its higher decode cost. | Change the handoff live pick; SenseVoice is a pending option, not a cleared download. |
| After-call transcription | **Qwen3-ASR 1.7B MLX 8-bit** | Lowest measured mixed-language error among the completed bilingual runs, with ample throughput. | Keep the handoff name; future app work replaces its distinct 0.6B native engine. |
| Brief voice | **Ming 3.0 TTS 0.5B MLX 8-bit** | Cleared bilingual voices with no GPL phonemizer in the evaluated path; 103.9 / 68.8 seconds for the full English / Chinese brief. | Change the default from Kokoro to Ming. Kokoro is an alternative labelled GPL component and needs explicit acceptance. |
| Screen OCR | **Apple Vision accurate, app language configuration** | 98.22% character accuracy at 95 ms mean latency with lower CPU than PP-OCRv6. | Keep the built-in choice; no model download. |

At survey base `508fe59f` (fetched before work began), the setup handoff and implementation name different after-call
artifacts. [`asr_qwen3.go`](../wails-gui/asr_qwen3.go) downloads **Qwen3-ASR
0.6B int8 ONNX**, not the **1.7B MLX 8-bit** model named in the
[setup handoff](../docs/setup/handoff.md). They have separate rows and manifest
IDs below. A recommendation for 1.7B therefore agrees with the handoff's name
but requires a future runtime/download change in the app.

## Scope and evidence

The shortlist has at most three new-to-this-comparison candidates per job:

- Live: the standalone FunASR-Nano CTC encoder and Qwen3-ASR 0.6B native CPU.
  SenseVoice is the production control. These are chunked offline recognizers,
  not claims of native token streaming.
- After call: full Fun-ASR-Nano, Qwen3-ASR 1.7B rerun on the newer MLX runtime,
  and MiMo V2.5 on upstream mlx-audio. The app's 0.6B native model is the control.
  MiMo's bounded October attempt is a failure record, not a completed accuracy run.
- Voice: Kokoro v1.1 int8 ONNX, Kokoro v1.0 MLX, and Ming 3.0 TTS 0.5B MLX.
  The current Kokoro English v0.19 and MeloTTS zh_en are controls.
- OCR: PP-OCRv6-tiny ONNX and macOS 26 `RecognizeDocumentsRequest`, against
  the production-equivalent Vision text request.

New-to-this-comparison does **not** mean newly trained since June. The dated
survey below distinguishes weight releases, runtime updates and older controls.
The reproducible commands are in [the run guide](survey-2026-10/README.md),
with [pinned sources](survey-2026-10/sources.json),
[asset inventories](survey-2026-10/assets/),
[per-sample runs](survey-2026-10/runs/), and
[the manifest draft](manifest-draft.json).

All heavy runs invoke `nice -n 15` (effective nice 20 on this inherited low-priority worker), one model process at a time. Native ASR/TTS
use two threads; the speaker pack uses production's one thread per component.
This Mac is shared with other workers, so timings include scheduling and
memory pressure. Downloads and model loads are outside warm inference timing.
Models were downloaded into the eval cache and removed after measurement when
space was needed; no other worker's model folder was used. The optional
PyTorch package installed with Misaki was removed from the eval environment
and its newly created package cache was cleaned during disk pressure.

### Speech pilot

The existing `evaluate.run_evaluation` harness runs 30 public utterances per
split: FLEURS Mandarin, LibriSpeech clean/other, the first 30 ASCEND test rows,
and a second ASCEND subset restricted to utterances containing both Han and
Latin text. The latter uses the lowest SHA-256 IDs among eligible test rows;
it is the mixed-language column used for decisions. Sources and waveform
hashes are in [data/](survey-2026-10/data/). FLEURS and LibriSpeech are
CC-BY-4.0; ASCEND is CC-BY-SA-4.0. Original speech files are not committed.

October accuracy sums edit counts across the corpus: English WER, Mandarin
CER, and mixed MER with Han characters plus Latin words as tokens. It preserves
numbers as written, rather than pretending every written-number variation is
normalized. True CER counts characters, unlike June's CER path which inserted
spaces around mixed tokens. The old per-utterance means remain in the raw JSON
for compatibility but are not the decision columns. RTF is total inference
seconds / total audio seconds. Two utterances per split are discarded warmups.

This is a pilot, not a statistically conclusive new leaderboard. The first
30 FLEURS clips repeat a small set of prompts and include Latin parenthetical
names often absent from model outputs; the recordings were not manually audited. Those unedited references can inflate
Mandarin CER; all models receive exactly the same inputs and references. The
first ASCEND rows are unusually short. The explicitly mixed subset and the
June 500-clip results are useful context, but neither removes the need for a
larger meeting/domain test before replacing production ASR.

RSS is sampled every 20 ms during ASR/TTS inference; process high-water RSS is
also retained. MLX Metal peak allocation is reported separately. Metal and RSS
are different accounting views and must not be blindly added. OCR memory is
maximum process RSS observed after calls, not a sampled allocation peak.
CPU 100% is one logical core, not the whole ten-core machine.

## 1. Live transcription

| Model | EN clean / other WER | Mandarin CER | Mixed MER | Corpus RTF / min per audio hour | Decode p95 ms (worst split) | Download / installed MB | RSS high-water / Metal peak MB | Licence; runtime |
|---|---|---|---|---|---|---|---|---|
| SenseVoice Small int8 (current live) | 3.64% / 4.12% | 11.84% | 10.28% | 0.021 / 1.3 | 330 | 239.5 / 239.5 | 1122 / 0 | Pending; FunASR v1.1; sherpa CPU 2t |
| FunASR-Nano CTC encoder int8 | 7.78% / 6.36% | 13.41% | 12.38% | 0.019 / 1.1 | 296 | 187.7 / 265.4 | 1305 / 0 | Apache-2.0; sherpa CPU 2t |
| Qwen3-ASR 0.6B int8 (current after call) | 2.65% / 2.23% | 10.10% | 10.75% | 0.190 / 11.4 | 3350 | 878.7 / 1000.1 | 2331 / 0 | Apache-2.0; sherpa CPU 2t |

Sizes use decimal MB, exclude optional runtimes, and add the shared 30.5 MB speaker pack for live use. Process memory includes the Python evaluator, not a Go app memory forecast. Licence status is separate from the non-commercial flag: SenseVoice is pending, and the cleared alternatives retain their licence/attribution obligations.

Measured CPU during decode (unweighted mean of per-utterance percentages, 100% = one core): SenseVoice 196%, Nano CTC encoder 196%, Qwen 0.6B 190%, Qwen 1.7B 46%, full Nano 156%. These percentages exclude idle time between audio chunks; the small SenseVoice RTF is what keeps continuous duty low. MLX also uses the GPU, whose work is outside process CPU percentage.

**Recommendation: Qwen3-ASR 0.6B int8 with the mandatory speaker pack**, because
it has the strongest measured bilingual accuracy among the cleared native
candidates: English clean/other WER 2.65% / 2.23%, Mandarin CER 10.10%, and
mixed MER 10.75%. Its publisher's [pinned model card declares Apache-2.0](https://huggingface.co/Qwen/Qwen3-ASR-0.6B/blob/5eb144179a02acc5e5ba31e748d22b0cf3e303b0/README.md).
This changes the handoff's live pick. The cost is material: corpus RTF 0.190
versus SenseVoice's 0.021, and worst-split p95 decode 3,350 ms versus 330 ms.
The cleared Nano CTC encoder is the low-CPU alternative (RTF 0.019), with worse
English clean WER 7.78%, Mandarin CER 13.41% and mixed MER 12.38%. The live
recommendation prioritizes bilingual accuracy within the measured near-real-time
native choices; it does not claim SenseVoice's low-CPU performance.

**SenseVoice remains the pending option**, with its observed English clean/other
WER 3.64% / 4.12%, Mandarin CER 11.84%, mixed MER 10.28% and RTF 0.021 preserved.
The earlier July reply in [SenseVoice #334](https://github.com/QwenAudio/SenseVoice/issues/334)
was reopened for licence-owner/core-maintainer confirmation. It is not final
commercial permission. [FunASR #3715](https://github.com/modelscope/FunASR/issues/3715)
leaves the licence version, conversion and commercial redistribution of these
pinned ONNX/tokens files unresolved. The linked
[FunASR model licence v1.1](https://github.com/modelscope/FunASR/blob/main/MODEL_LICENSE)
requires attribution/model names and contains the phrase “reference and learning
purposes only”; its scope remains disputed. The draft therefore records
`licence_status: pending`, `non_commercial: null`, and disables selection until
permission is established. Source code's MIT licence does not clear the weights.

These are pre-segmented utterance timings, not microphone-to-first-partial
latencies. Production also pays VAD/endpointing latency (including its silence
threshold), queueing and audio padding. Qwen's native adapter preserves the
app's 18-second decoder window bound, but this pilot does not duplicate the
app's full VAD/padding path. It must not be sold as a streaming latency test.

The separate diarization smoke test **passed all six alternating turns** on 38.38 seconds of public audio. It took 2.01 seconds (RTF 0.052), excluding load. That cost is separate from the ASR-only rows above; the exact speaker-pack download is 30,465,498 bytes.

The [speaker pack](survey-2026-10/assets/speakers.json) pins the same Silero
VAD, pyannote segmentation 3.0 int8 and CAM++ weights as production. Every live
choice in the draft requires it, including English-only alternatives. The
smoke test checks anonymous speaker clusters on six concatenated public
LibriSpeech turns from two different speakers. It is not overlapping-meeting
DER, bilingual speaker identification or a replacement for the app's channel
and attribution regression tests. No voice identity is inferred from text.

The handoff also lists two historical live alternatives. They are inventoried
in the manifest rather than represented as newly measured October candidates:

| Historical alternative | June EN clean / other WER | June RTF clean / other | October pinned download | Licence; runtime |
|---|---|---|---|---|
| Parakeet TDT v2 | 2.49% / 3.92% | 0.543 / 0.105 | 2,471,862,935 bytes | CC-BY-4.0; MLX |
| Whisper large-v3-turbo | 4.84% / 6.07% | 0.590 / 0.598 | 1,613,977,880 bytes | MIT; MLX |

These figures come from [June's report](RESULTS.md), with its different sample
count and averaging method. June RSS did not capture Metal allocations, so no
comparable October peak-memory number is claimed. Parakeet is English only;
Whisper's June mixed-language normalization failure prevents using that score
to rank code-switching. Both live choices still require the shared speaker pack.

## 2. After-call transcription

| Model | EN clean / other WER | Mandarin CER | Mixed MER | Corpus RTF / min per audio hour | Decode p95 ms (worst split) | Download / installed MB | RSS high-water / Metal peak MB | Licence; runtime |
|---|---|---|---|---|---|---|---|---|
| Qwen3-ASR 0.6B int8 (current after call) | 2.65% / 2.23% | 10.10% | 10.75% | 0.190 / 11.4 | 3350 | 878.7 / 1000.1 | 2331 / 0 | Apache-2.0; sherpa CPU 2t |
| SenseVoice Small int8 (current live) | 3.64% / 4.12% | 11.84% | 10.28% | 0.021 / 1.3 | 330 | 239.5 / 239.5 | 1122 / 0 | Pending; FunASR v1.1; sherpa CPU 2t |
| Qwen3-ASR 1.7B MLX 8-bit | 2.15% / 1.89% | 8.77% | 7.24% | 0.106 / 6.4 | 1774 | 2467.9 / 2467.9 | 2100 / 3232 | Apache-2.0; MLX Metal |
| Full Fun-ASR-Nano int8 | 1.66% / 2.06% | 10.76% | 11.92% | 0.213 / 12.8 | 4280 | 1009.6 / 1009.6 | 3768 / 0 | Apache-2.0; sherpa CPU 2t |

Sizes use decimal MB, exclude optional runtimes, and add the shared 30.5 MB speaker pack for live use. Process memory includes the Python evaluator, not a Go app memory forecast. Licence status is separate from the non-commercial flag: SenseVoice is pending, and the cleared alternatives retain their licence/attribution obligations.

**Recommendation: Qwen3-ASR 1.7B MLX 8-bit for the bilingual after-call default**,
because it improves the mixed-language pilot over the current 0.6B CPU model
and SenseVoice while remaining comfortably faster than real time. Full Nano
has a strong English-clean result, but it does not beat Qwen on this mixed
subset. This is an accuracy-first choice among the measured bilingual tradeoffs,
not a claim that one model wins every language/domain.

The setup handoff's chosen name stays Qwen3-ASR 1.7B; the shipping implementation
would change from 0.6B ONNX to the measured MLX artifact. The 1.7B run uses
`mlx-qwen3-asr` 0.4.4, whose September runtime release is materially newer than
the June evaluation. Minutes-per-hour figures extrapolate short utterances;
this run does not measure a one-hour call end to end, word alignment, speaker
reconciliation or thermal behavior on battery.

The third after-call candidate, **MiMo V2.5 ASR 4-bit**, was downloaded and
actually attempted with upstream `mlx-audio` 0.6.1. It loaded in 7.55 seconds
but remained in the two-utterance warmup phase at a 600-second process deadline,
with zero scored utterances. The attempt used an 8 GiB MLX memory limit and
128 MiB cache on this shared 16 GB host. A macOS process sample observed a
7.8 GB peak physical footprint and a wait in MLX evaluation / Metal command
submission. This is a bounded runtime/resource failure for this configuration,
not a measured accuracy/RTF result, proof of a general upstream defect, or
evidence that the memory limit alone caused it. It should be retried on a
quiet larger-memory Mac before being offered as a tested alternative.
[Exact failure record](survey-2026-10/runs/asr-mimo.json).

| Attempted candidate | October accuracy / RTF | Download, including required tokenizer | Memory observation | Licence; runtime |
|---|---|---|---|---|
| MiMo V2.5 ASR MLX 4-bit | Unavailable: warmup deadline | 7,103,256,224 bytes (4,527,573,441 ASR + 2,575,682,783 tokenizer) | 7.8 GB physical footprint peak reported by macOS sample; 8 GiB MLX limit | MIT original / Apache-2.0 conversion; mlx-audio 0.6.1 |

MiMo's June figures remain historical context with a different fork, corpus
and metrics; no October ranking is inferred from them. Its **separate audio
tokenizer is required** and must be included in any download/disk total, not
silently fetched by a runtime. A bare 4.5 GB ASR weight file is not a complete
MiMo installation. Likewise, this survey's
pinned Parakeet MLX artifact is about 2.47 GB and Whisper turbo MLX about
1.61 GB; the handoff's 600/809 MB labels do not describe those files. A different
quantization needs a separate ID, hash list and evaluation.

## 3. Voice for the brief

| Model | Primary short-sample RTF EN / ZH | Full brief synthesis seconds EN / ZH | Back-ASR EN WER / ZH CER | Download / installed MB | Max observed RSS / Metal peak MB | Licence; runtime |
|---|---|---|---|---|---|---|
| Kokoro English v0.19 | 0.443 / — | 74.9 / — | 0.00% / — | 319.6 / 369.3 | 867 / 0 | Restricted: Apache-2.0 weights + GPL eSpeak; sherpa CPU 2t |
| MeloTTS zh_en | 0.665 / 0.505 | 44.7 / 52.1 | 2.27% / 1.64% | 167.0 / 191.2 | 709 / 0 | MIT; sherpa CPU 2t |
| Kokoro bilingual v1.1 int8 | 0.630 / 1.022 | 90.9 / 165.1 | 0.00% / 4.92% | 147.0 / 215.3 | 679 / 0 | Restricted: Apache-2.0 weights + GPL eSpeak; sherpa CPU 2t |
| Kokoro bilingual v1.0 MLX | 0.066 / 0.076 | 8.3 / 9.5 | 0.00% / 0.00% | 355.3 / 355.3 | 1064 / 2440 | Restricted: Apache-2.0 weights + GPL eSpeak; MLX Metal |
| Ming 3.0 TTS 0.5B MLX 8-bit | 0.816 / 0.783 | 103.9 / 68.8 | 0.00% / 0.00% | 1617.2 / 1617.2 | 1371 / 2051 | MIT; MLX Metal |

The Kokoro MLX download additionally requires the 12.8 MB English spaCy G2P wheel (15.3 MB installed), plus runtime packages. Kokoro eSpeak components carry GPL-3.0-or-later terms separately from Apache-2.0 weights. Memory is the maximum observed across sample and full-brief stages; a brief-only rerun may have a lower process high-water than earlier short samples.

**Default recommendation: Ming 3.0 TTS 0.5B MLX 8-bit**
(`licence_status: cleared`), with the manifest's top-level
`gplComplianceAccepted: false`. Its evaluated `UK_Charlotte` / `灵小柔` presets
and additional `US_James` / `灵信远` samples provide several English and Chinese
voices. It has MIT publisher/distribution declarations and zero back-ASR errors
on the three primary sentences per language. Its text-tokenizer/AudioVAE path
does not use eSpeak. The cost is 103.9 seconds for the English brief and 68.8
seconds for Chinese, so the cleared default misses the few-seconds goal.
Melo is smaller/faster but offers only one speaker and had nonzero primary
back-ASR errors. Ming is the strongest measured cleared fit for the several-voices
requirement, not a claim of human-rated superiority. Package its selected
runtime separately from the shared survey environment's unused Kokoro/eSpeak
dependencies. [Publisher MIT declaration](https://huggingface.co/inclusionAI/Ming-3.0-tts-0.5B/blob/8d66b70a00f05ad183fa8a863b4ce609fc83cbc0/README.md),
[pinned distribution MIT licence](https://huggingface.co/mlx-community/Ming-3.0-tts-0.5B-8bit/blob/d0d1da6f627e4b3f20cc2db6ba4aee86d45343ad/LICENSE).

**Alternative: Kokoro v1.0 MLX — GPL component.** Its evaluated `af_heart` /
`zf_xiaobei` voices and additional `am_adam` / `zm_yunxi` presets give the best
measured full-brief throughput, 8.3 / 9.5 seconds. It becomes the voice default
only in a generated variant with `gplComplianceAccepted: true`; the committed
default keeps that flag false and recommends Ming. All three Kokoro entries
remain `restricted` and identify `restriction_acceptance_flag: gplComplianceAccepted`.
For **every job**, the validator rejects a recommended entry unless it is
cleared or its specific restriction is accepted by the matching supported flag.
A GPL flag cannot approve a pending licence or an unrelated restriction.

Kokoro's Apache-2.0 weights use
[eSpeak-ng, GPL-3.0-or-later](https://github.com/espeak-ng/espeak-ng/blob/master/COPYING),
for phonemization. **GPL component: needs source offer and notices if bundled.**
The explicit flag records acceptance; it does not itself fulfil those
obligations or clear the restricted entry. The English spaCy support wheel's
MIT evidence is its [3.8.0 release declaration](https://github.com/explosion/spacy-models/releases/tag/en_core_web_sm-3.8.0).
Both MLX choices require future app/runtime integration; this PR does not change
the app or establish human-rated voice warmth.

The same three original sentences per language are in
[tts_prompts.json](tts_prompts.json). Every bilingual candidate has all six;
additional first-sentence clips exercise a second English and Chinese voice.
[The sample inventory](tts-samples/samples.json) records every filename, sentence,
language, model, exact `size_bytes` and SHA-256. The 34 WAVs live only in
`~/src/model-survey-samples/` on this Mac; the hub sends them to Bo. They are
PCM16, peak-normalized after timing, and were not played automatically. No
WAV is retained in this branch's commit history.

The full throughput probe is exactly 300 English words, or 552 Chinese
characters. It repeats the short prompts to make throughput deterministic; it
is not a prose-quality test. The final probe packs bounded sentence groups
(maximum 200 Latin or 80 Han characters) and retains every input character.
This avoids Kokoro MLX's 510-phoneme truncation path. The rejected unchunked
Chinese run and earlier chunking attempts are preserved under
[diagnostics/](survey-2026-10/diagnostics/) and excluded from recommendations.
A short, truncated WAV is never credited as fast full-brief synthesis.

Qwen3-ASR 1.7B, the strongest completed bilingual/code-switching recognizer in this pilot, back-transcribed all 34 WAVs after synthesis processes exited. The table aggregates only the same three sentences in each primary voice (44 English words / 61 Chinese characters); secondary voices remain in the per-clip JSON. Kokoro MLX and Ming both had zero errors on those small primary subsets. This is a tiny intelligibility check, not a meaningful zero-error generalization estimate. The exact verifier and hypotheses are in [tts-backtranscription.json](survey-2026-10/runs/tts-backtranscription.json).

Back-transcription is an intelligibility proxy. WER cannot measure warmth,
prosody, naturalness, speaker preference, or whether an acronym was spoken well
but formatted differently by ASR. Bo's listening judgment remains necessary.
The run does not clone a user's voice. English spaCy G2P is an explicit model
support asset in the manifest; runtime packaging must include Misaki, eSpeak
and their licences without silent model downloads.

The MLX hub repository called `Kokoro-82M-bf16` actually stores **F32** tensors
at the pinned revision. The table and manifest use its measured bytes rather
than the repository name as a precision/size guarantee. The v1.1 Chinese model
is not a strict upgrade to v1.0: its publisher says it drops many prior voices.
Its 103 speakers include only three English female voices; v1.0 offers a
broader English voice selection. [Publisher's v1.1 notes](https://huggingface.co/hexgrad/Kokoro-82M-v1.1-zh),
[sherpa speaker mapping](https://github.com/k2-fsa/sherpa-onnx/pull/1942).

## 4. Continuous screen OCR

| Engine | Character accuracy | Latency mean / p95 ms | CPU during calls | Observed RSS MB | Download MB | Licence; runtime |
|---|---|---|---|---|---|---|
| Apple Vision text (current configuration) | 98.22% | 95.1 / 140.8 | 101.1% | 153.5 | 0.00 | Apple OS terms; native Vision |
| Apple Vision macOS 26 documents | 98.05% | 171.5 / 206.2 | 100.5% | 167.3 | 0.00 | Apple OS terms; native Vision |
| PP-OCRv6-tiny detector + recognizer | 99.23% | 183.2 / 277.8 | 190.2% | 975.3 | 6.32 | Apache-2.0; RapidOCR / ONNX CPU 2t |

At one screenshot per second, the measured per-call time/CPU implies about 0.10 of one CPU core for Vision text and 0.35 for PP-OCRv6. This is a duty-cycle projection, not a long-running capture/thermal measurement. Both native requests may use system accelerators internally; the CPU column does not measure GPU/ANE energy.

**Recommendation: retain Apple Vision accurate text recognition with the app's
existing Chinese-first language resolution and fullwidth-ASCII normalization**,
because its small CPU duty and latency are a better continuous-screen tradeoff
than the approximately one-point accuracy gain from PP-OCRv6 on this fixture set.
The handoff's built-in Apple Vision choice stays; it has no app-managed weight
download. Keep PP-OCRv6-tiny as an optional downloadable alternative, and use
the document API when structured documents are the actual job.

Thirty generated public JPEG screens cover English UI labels, Chinese/mixed
text, Go and TypeScript, light/dark themes and 1x/2x sizes. They are synthetic
PIL renderings, not captures of this computer or a substitute for a native
WebKit/Retina screenshot corpus. Character accuracy is `1 - corpus CER`, ignoring
whitespace and folding fullwidth ASCII like the app, while preserving case and
code punctuation. Images and references are [committed together](survey-2026-10/ocr-fixtures/).
There is one discarded warmup per persistent engine. Native latency excludes
the Python pipe; raw wall timing is also retained. The CPU column is usage
*during calls*, so continuous duty also depends on screenshot cadence.

The English-first diagnostic badly loses Chinese; that is **not production's
configuration**. [`ocr_breadcrumb.go`](../wails-gui/ocr_breadcrumb.go) already
puts `zh-Hans`, `zh-Hant`, then English first and appends supported CJK languages.
The production-equivalent result is the `app` row, not that diagnostic.

macOS 26 `RecognizeDocumentsRequest` was introduced at **WWDC25**, not after
June 2026. It supplies paragraphs, tables, lists and detected data, rather than
just OCR lines. This test actually compiles and runs it on macOS 26.2; it does
not infer speed from Apple's documentation. [Apple API](https://developer.apple.com/documentation/vision/recognizedocumentsrequest),
[Apple WWDC25 session](https://developer.apple.com/videos/play/wwdc2025/272/).

## What changed since June, and what was screened out

Dates below concern the identified artifact or runtime. A model being popular
in October does not make it a post-June weight release. The
[ASR leaderboard's version history](https://huggingface.co/spaces/hf-audio/open_asr_leaderboard/blob/main/init.py)
was checked through its October 21 update, and the
[Open TTS leaderboard](https://huggingface.co/spaces/hf-audio/open_tts_leaderboard)
was consulted for survey context. Their server/GPU scores are not substituted
for Apple Silicon timings or bilingual/code-switching results.

### ASR survey

| Family / evidence | Change or status | Local path and licence | Shortlist decision |
|---|---|---|---|
| [Qwen3-ASR](https://huggingface.co/collections/Qwen/qwen3-asr) | January weights; native HF variants listed in July; [MLX runtime 0.4.4](https://github.com/moona3k/mlx-qwen3-asr/releases/tag/v0.4.4) in September | MLX or sherpa int8; Apache-2.0 | Rerun 1.7B and the app's distinct 0.6B artifact. |
| [SenseVoice Small](https://github.com/QwenAudio/SenseVoice) | Older weights; July reply later reopened for licence-owner confirmation | sherpa-onnx; licence status **pending**: model v1.1 attribution and reference/learning wording, with pinned redistribution unresolved | Keep its measured numbers as the pending option; [#334](https://github.com/QwenAudio/SenseVoice/issues/334) and [FunASR #3715](https://github.com/modelscope/FunASR/issues/3715) do not establish final permission. |
| [Fun-ASR-Nano](https://huggingface.co/FunAudioLLM/Fun-ASR-Nano-2512) | December 2025 weights; newer native/runtime packaging in 2026 | Full int8 decoder and standalone CTC encoder in sherpa; source card Apache-2.0 | Evaluate the encoder and full decoder separately; they are not the same model. |
| [sherpa releases](https://github.com/k2-fsa/sherpa-onnx/releases) | 1.13.8 September fixes include Nano/Qwen races and silence handling; 2.0.0 appeared October 23 | Native macOS CPU runtime, Apache-2.0 code | Pin 1.13.8 for this run; same-day 2.0.0, including lexicon/OOV fixes, is not included in scores. |
| Paraformer | Already in June; poor English in that test | Existing sherpa ONNX path; model-specific FunASR terms | Not a new balanced-bilingual upgrade. |
| [Parakeet v3](https://huggingface.co/nvidia/parakeet-tdt-0.6b-v3), [Canary v2](https://huggingface.co/nvidia/canary-1b-v2) | August 2025 weights | MLX/ONNX; CC-BY-4.0 | No Mandarin coverage in these artifacts. Preserve v2 English-only handoff alternative as historical, with correct byte size. |
| [Moonshine](https://github.com/moonshine-ai/moonshine) | Streaming SDK releases continued through August 2026; streaming/Chinese weights predate June | Native/ONNX; English MIT; non-English community terms must be checked per checkpoint | Separate language checkpoints do not establish reliable bilingual code-switching. No single bilingual upgrade demonstrated here. |
| [Voxtral Realtime 4B](https://huggingface.co/mistralai/Voxtral-Mini-4B-Realtime-2602) | February 2026; newer [MLX conversions](https://huggingface.co/iris-sfg/Voxtral-Mini-4B-Realtime-2602-4bit-fp16) and incremental runtimes | MLX; Apache-2.0; includes Chinese/English | Genuine streaming candidate, but larger always-on GPU/memory footprint than the native CPU shortlist; not timed here. |
| [Whisper turbo](https://huggingface.co/openai/whisper-large-v3-turbo) | Older weights; no newer official variant verified in this survey | MLX; MIT | Keep broad-language historical alternative; June mixed score had a normalization failure and is not valid ranking evidence. |
| [MiMo V2.5](https://huggingface.co/mlx-community/MiMo-V2.5-ASR-MLX-4bit) | April weights / May MLX conversion; upstream MLX integration changed in October | MLX; original MIT, conversion distribution Apache-2.0; separate audio tokenizer | Attempted upstream 0.6.1 in October; warmup deadline at 600s, no accuracy score. Keep its complete package and failure status explicit. |
| [Granite Speech 5.0 2B](https://huggingface.co/ibm-granite/granite-speech-5.0-2b) | New October 7 release; appears in October 21 leaderboard update | MLX support in current runtime; Apache-2.0 | Its listed languages omit Mandarin, so not the main bilingual pick. |
| [Kyutai STT](https://huggingface.co/kyutai/stt-1b-en_fr) | June 17 release | Local Kyutai/MLX path; CC-BY-4.0 | English/French, not Mandarin. |
| [Qwen-Audio-3.0-ASR paper](https://arxiv.org/abs/2609.07549) | September report | No verified downloadable Apple-runtime artifact identified by this survey | Do not invent a model ID, licence, checksum or timing from a paper title. |

### TTS survey

| Family / evidence | Status and local runtime | Licence / non-commercial restriction | Decision |
|---|---|---|---|
| [Kokoro v1.0/v1.1](https://huggingface.co/hexgrad/Kokoro-82M-v1.1-zh) | Older 82M weights; current MLX and native ONNX options | Apache-2.0 weights; eSpeak/data/runtime terms remain separate | Benchmark both the multilingual MLX v1.0 and int8 native v1.1 artifacts. |
| [Ming 3.0 TTS 0.5B](https://huggingface.co/inclusionAI/Ming-3.0-tts-0.5B) | September 22 weights; [MLX conversion](https://huggingface.co/mlx-community/Ming-3.0-tts-0.5B-8bit) in October | MIT; no non-commercial-only restriction | Third candidate. `mlx-audio` 0.6.1 includes its prompt-contract fix; older runtimes can say the wrong words. |
| [Qwen3-TTS](https://huggingface.co/Qwen/Qwen3-TTS-12Hz-0.6B-CustomVoice) | January 2026, 0.6B/1.7B local MLX CustomVoice models | Apache-2.0 | Valid bilingual option; not one of the three timed candidates. Prefer testing smaller Kokoro plus the genuinely newer Ming weights within the shortlist budget. |
| [CosyVoice 3](https://huggingface.co/FunAudioLLM/Fun-CosyVoice3-0.5B-2512) | December 2025 weights, bilingual/multilingual | Apache-2.0 | No verified fit in the pinned sherpa/MLX evaluators here; no Mac timing claimed. |
| [F5](https://huggingface.co/SWivid/F5-TTS), [E2](https://huggingface.co/SWivid/E2-TTS) | Older checkpoints; F5 has a local MLX port | CC-BY-NC-4.0 **weights**; non-commercial restriction despite permissive code | Exclude from default downloads. A code licence is not a weight licence. |
| [IndexTTS-2](https://huggingface.co/IndexTeam/IndexTTS-2/blob/main/LICENSE.txt) | Local MLX implementation exists | Custom Bilibili Model Use Licence; downstream obligations and training/use restrictions, not blanket Apache/MIT | Exclude from the default shortlist; do not label it merely “open source” or assume a simple NC flag captures its terms. |
| [OpenAudio S1 mini](https://huggingface.co/fishaudio/openaudio-s1-mini) | Older Fish/OpenAudio checkpoint | CC-BY-NC-SA-4.0; non-commercial | Exclude from default downloads. |
| [Fish Drama 3](https://huggingface.co/fishaudio/drama-3/blob/main/LICENSE) | New October release; MLX support also changed in October | Current file is Fish Audio Community Licence 1.0: conditional commercial rights, revenue threshold, hosted-service and attribution conditions; hub tags may lag | Large model/package and conditional rights are a poor fit for this brief default. No small-Mac speed claim. |
| [Dia](https://huggingface.co/nari-labs/Dia-1.6B), [Orpheus](https://huggingface.co/canopylabs/orpheus-3b-0.1-ft), [Sesame CSM](https://huggingface.co/sesame/csm-1b) | Local MLX implementations; cited checkpoints are English | Apache-2.0 | These artifacts do not meet the Chinese requirement. |
| [Kyutai TTS](https://huggingface.co/kyutai/tts-1.6b-en_fr) | Local model, English/French | CC-BY-4.0 | No Mandarin in this artifact. |
| [Chatterbox](https://huggingface.co/ResembleAI/chatterbox) | Local MLX multilingual path includes Chinese/English | MIT | Eligible but not timed within the three-candidate limit; no unmeasured superiority claim. |
| [MeloTTS](https://github.com/myshell-ai/MeloTTS) | Existing zh_en VITS ONNX bundle | MIT | Keep as the current Chinese/bilingual control, not as an unverified “successor.” |

### OCR survey

| Family / evidence | Change and Apple Silicon path | Licence | Decision |
|---|---|---|---|
| [PP-OCRv6](https://github.com/PaddlePaddle/PaddleOCR/releases/tag/v3.7.0) | June 11, two days after the old report; tiny/small/medium, official ONNX exports | Apache-2.0 | Test tiny through RapidOCR/ONNX CPU; count both detector and recognizer files. |
| PP-OCRv5 / [PaddleOCR-VL 1.6](https://github.com/PaddlePaddle/PaddleOCR/releases/tag/v3.6.0) | Predate June; newer PP-OCRv6 is the lightweight candidate. MLX supports the VL family | Apache-2.0 | Document-parser quality is not evidence of continuous-screen latency. |
| [Surya](https://github.com/datalab-to/surya/blob/master/MODEL_LICENSE) | 650M document OCR; current source and weight licences differ | Apache-2.0 code; modified AI Pubs Open RAIL-M weights with revenue/funding/competition conditions | Conditional commercial rights; not a clean default redistribution choice. |
| [dots.ocr / dots.mocr](https://github.com/Blaizzy/mlx-vlm/blob/main/mlx_vlm/models/dots_ocr/README.md) | Generative multilingual document/layout OCR, local MLX | Original dots.ocr MIT; check the selected successor separately | Not shown to meet this screen-latency/CPU budget; not timed. |
| [olmOCR 2](https://huggingface.co/allenai/olmOCR-2-7B-1025) | 7B document model; local conversions exist | Apache-2.0 | Larger document-generation workload; not the lightweight continuous-screen shortlist. |
| [GOT-OCR 2.0](https://huggingface.co/ucaslcl/GOT-OCR2_0) | Older generative OCR | Apache-2.0 | No validated adapter in this local run; not a claimed native/Mac benchmark. |
| [GLM-OCR](https://github.com/Blaizzy/mlx-vlm/blob/main/mlx_vlm/models/glm_ocr/README.md) | Small generative OCR with verified MLX support | MIT source weights | Plausible document alternative; PP-OCRv6 and Vision fit the low-overhead screen job better. This is a workload-fit inference, not a measured GLM failure. |

## Manifest and handoff implications

`manifest-draft.json` supplies IDs, jobs, display/one-phrase/evidence lines,
exact transport bytes, file SHA-256s, ordered sources and licence URLs. Every
entry has `licence_status` (`cleared`, `pending` or `restricted`) with evidence
links and an explanation. `non_commercial: null` means unresolved, not cleared;
GPL obligations are a restriction distinct from non-commercial use. Cleared
means the documented delivery/use route with its ordinary conditions, not an
exemption from attribution. The top-level `gplComplianceAccepted` flag defaults
to false: every recommended entry must be cleared unless its named restriction
is explicitly accepted. The default voice is Ming; Kokoro is a GPL component
alternative. Apple is cleared only for system API use. The draft
separates download bytes from installed bytes, includes the mandatory speaker
pack, and represents system OCR / “None” with empty download lists. No Apple
private weight checksum or nonexistent Isoform mirror is invented. Existing
SenseVoice mirror order comes from production; new artifacts use verified
upstream sources until a mirror is actually populated.

This is a **draft model catalogue**, not a shipping downloader change. The
current privacy/memory assets remain outside this four-job survey. Before the
new setup ships, its common MLX runtime/G2P packaging and notices must also be
listed or bundled, and the backend must support any newly chosen runtime.
Weights measured here do not authorize silent runtime/model downloads. Licence
names describe the selected artifacts; publisher/card disagreement is called
out rather than silently replaced with the inference code's licence.

## Verification and remaining limits

Round 1 recorded **13 passing tests**: the original seven measurement tests,
three licence-policy guards and three real AudioFolder cache/export checks.
Round 2 adds **12 passing standard-library tests** for all-job recommendation
gating, both GPL flag values, pending/unknown restrictions and sample byte sizes.
The real generator/validator CLI accepts the default Ming manifest and an
explicitly accepted Kokoro variant, and rejects the same Kokoro selection with
the flag false. The earlier dependency-based tests were not rerun after their
evaluation environment was removed. All 34
external WAV hashes and PCM headers match the committed sample inventory.
The metadata-only validator also runs with the standard library after removing
the temporary evaluation environment; no models or private app data are read.

- Seven measurement-invariant unit tests pass, including proper corpus weighting,
  true CER/mixed tokens, silence insertions, lazy engine loading and lossless
  bounded TTS input. Python modules compile; the Swift worker compiled and
  exercised both native APIs against all fixtures.
- Five complete ASR configurations × 150 scored public utterances = **750 scored
  observations**, with no failed scored samples. MiMo is an explicit sixth
  attempt with a warmup deadline and no scored output. Two warmups per split
  are excluded from the 750.
- Three decision OCR configurations × 30 screens = **90 scored images**, plus
  two diagnostic language-order passes. No OCR call errors in the decision runs.
- Five TTS configurations, **34 indexed external PCM16 WAVs**, full-length English/Chinese
  throughput probes where supported, and Qwen 1.7B back-transcription of all
  clips. WAV hashes, sample counts, durations, input retention, finite metrics,
  model byte arithmetic and manifest dependency IDs pass `validate_survey.py`.
- Repository pre-commit checks pass for each commit. No desktop build, app
  launch or private recording/config access was needed; changes are limited to
  evaluation files and the requested hub self-review.

The intended next decision is listening to the externally retained samples and choosing
voice presets. Before shipping replacements, validate longer real meetings,
code-heavy bilingual speech, overlapping speakers, native Retina screenshots,
long-running CPU/energy and the actual packaged MLX/G2P runtime. Those gaps
are not filled by this public-data pilot or by server leaderboards.
