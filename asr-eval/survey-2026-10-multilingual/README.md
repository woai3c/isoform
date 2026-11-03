# Multilingual survey, part 2

This directory contains public-input evidence for
[the multilingual report](../RESULTS-2026-10-multilingual.md). The parent
October survey remains historical evidence; do not overwrite its results.

Run from `asr-eval/`. Never point these commands at application folders, launch
Isoform, or access private recordings/settings. All model evaluation commands
must run under `nice -n 15`. The runners require nice >= 10 and use an exclusive
lock so only one evaluation model runs at a time. Reference speech codecs and
speaker encoders are components of their TTS model, not separate competing jobs.
ASR and back-ASR never share a process with a synthesis model.

## Disk and environments

Use only this worktree's `.cache/multilingual/`, `.venv/`, and `.venv-plus/`.
`multilingual_eval.download` executes and records `df -k` before each download,
checks the exact expected size against **16 GiB** remaining, and rechecks free
space during transfer. A whole model/expanded archive also has a preflight.
This exceeds the requested 15 GB floor. Do not lower the reserve to get a model
to fit; delete completed evaluation weights first. Downloads check both size
and publisher LFS / pinned prior-survey SHA-256 when available. All generated
inventories include measured SHA-256 even when upstream supplies no hash.

The main environment uses mlx-audio 0.6.1, mlx-qwen3-asr 0.4.4,
mlx-whisper 0.4.3 and sherpa-onnx 1.13.8. Recreate it from the committed
`requirements-main.txt`; its English spaCy wheel URL matches the pinned artifact
in `manifest-draft.json` (id `kokoro-english-g2p`). Check `df`
before package installation as well. The isolated CosyVoice environment uses
`requirements-plus.txt`. Its mlx-audio-plus source is pinned to Git revision
`4c9ec6a8489e790b5ba8964ab1f1d63150476f9f` (package version 0.1.8).
The PyPI 0.1.4 package mentioned by the conversion card contains CosyVoice **2**,
not 3. Do not install upstream mlx-audio over the fork in that environment;
they provide the same Python namespace. The fork was installed with `--no-deps`
after the dependencies were prepared. Shared survey packages are not a shipping
runtime bill of materials or evidence that unused GPL packages are required
by Ming/CosyVoice/Chatterbox.

Kokoro's Japanese frontend uses Misaki's Cutlet path with `unidic-lite` 1.0.8
(`unidic.DICDIR` is explicitly set to that installed dictionary). English uses
the pinned `en_core_web_sm` 3.8.0 wheel. No frontend model is silently fetched
inside the timed calls. The Japanese dictionary is explicitly inventoried as
`assets/kokoro-japanese-g2p.json` (MIT wrapper, selected BSD-3-Clause dictionary
route). Its downloaded archive hash and installed data size are recorded.

## Public speech and ASR

```sh
nice -n 15 .venv/bin/python multilingual_eval.py data
nice -n 15 .venv/bin/python multilingual_run.py asr
```

`data` streams the seven FLEURS test archives without retaining whole archives.
It exports the first 100 rows of each published `test.tsv`, preserving row order
and duplicate prompts, at 16 kHz PCM16. Each reference, filename, duration and
waveform hash is committed under `data/`. Files stay in the ignored cache.
All seven subsets use the same pinned dataset revision and CC-BY-4.0 licence.
FLEURS is parsed as seven literal tab-separated fields (`csv.QUOTE_NONE`);
unbalanced quote characters in two German and one Korean row otherwise pull phonetic text
into the reference. `reference-parsing-correction.json` records the audit and
Whisper was rescored without changing its hypotheses, audio or timings.
The regional configs are en_us, cmn_hans_cn, ja_jp, ko_kr, es_419, fr_fr, de_de.
These are clean read-speech samples, not meetings or code-switching tests.

The one-model wrapper can also resume an individual run:

```sh
nice -n 15 .venv/bin/python multilingual_asr.py qwen17
```

Use `multilingual_run.py` first to download the pinned artifacts. Individual
ASR runs checkpoint every observation, discard two warmups per language and
wait for any not-yet-prepared public subset. All recognizers run without a
ground-truth language prompt. Generative language selection is automatic;
Granite uses shared-vocabulary greedy CTC. Its port's constant English result
metadata is not a language-ID measurement. CPU models use two
threads. MLX uses a 128 MiB cache limit and an 8 GiB allocation limit.
Qwen 0.6B preserves the app's 18-second decoder windows / 128 new-token bound.
Other candidates process each complete utterance. Whisper disables word
alignment for ASR-only speed; June's word-timestamp timings are not equivalent.

Scores sum corpus edit counts. Unicode NFKC/casefold normalization preserves
accents, Hangul syllables and kana, removes punctuation, and retains apostrophes
inside words. Chinese/Japanese/Korean use CER; English/Spanish/French/German use
WER. Spaces are excluded from CER. Numbers are not hand-expanded or corrected.
Both CER and WER with denominators and edit counts are retained. Each language
has equal weight in the mixed-metric macro screening score. That score is not
an interchangeable error unit across languages. Failure outputs count as
empty hypotheses; a run with failed observations cannot win selection.

The screening assumptions are RTF <= 0.25 in every language for live ASR and
RTF <= 1.0 for after call. `host.json` records these choices explicitly: the
handoff did not specify numerical ASR limits. These are ASR-only throughput
screens, not microphone-to-partial latency SLAs. Endpointing, speaker separation,
queues, cold loads and native app packaging require separate validation.

## Sentence delivery and voices

```sh
nice -n 15 .venv/bin/python multilingual_run.py tts
nice -n 15 .venv/bin/python multilingual_run.py tts ming_fast
nice -n 15 .venv/bin/python multilingual_run.py tts chatterbox_fp16
```

This runs Ming, the pinned Melo zh/en control, Kokoro MLX, Chatterbox multilingual
v3 and CosyVoice 3 sequentially, in the appropriate environment. All prompts are
original generated public text in `prompts.json`. The first two languages use
the same wording as part 1, split into individual sentences. The greeting and
first informative sentence are reported separately. Two preset/reference voices
are used where available; the pinned Melo model has one speaker and Kokoro has
one French voice. Unsupported artifact/language combinations are not scored.
The separate `ming_fast` follow-up uses the same pinned Ming weights and voices
with its public `ddpm_steps=6` option, compared with the baseline default of 10.
Its outputs and generation configuration are retained separately; a faster
configuration is not assumed to preserve quality before back-ASR.
The separate `chatterbox_fp16` follow-up retains the pinned F32 files and uses
MLX's public `Module.set_dtype(mx.float16)` on model/codec floating parameters,
then casts cached floating conditioning arrays while preserving integer tokens
and lengths. Its parameter precision is asserted and recorded separately.
Some operations may promote intermediates to F32 for numerical stability;
this is not a claim that every kernel computes exclusively in FP16.

Samples go to **`~/src/model-survey-samples-2/`**, outside the repository.
`voice-references.json` hashes two synthetic Ming samples from part 1. Chatterbox
and CosyVoice use those same synthetic voices in all languages. Chatterbox caches its public `Conditionals` result per reference voice after
the initial excluded warmup; its preparation time is retained. CosyVoice uses
the published zero-shot generation frontend, matching reference transcript and separately inventoried speech codec. The reference-only cross-lingual API mode is not measured.
This deliberately
tests cross-language voice transfer and can carry English accent/prosody; it is
not a native-speaker preference study or cloning of a real person's voice.

A first-call warmup (including language/voice setup) is retained separately.
Three subsequent first-sentence probes per voice, five sentence samples per
voice, and one full brief per language are timed. The full brief repeats complete
sentences until it reaches at least 300 normalized words (Latin languages) or
552 normalized characters (Chinese/Japanese); no final sentence is truncated.
Exactly the same language-specific input is used for every supporting model.
Full PCM waveforms are retained externally for back-ASR and listening.
The full-brief WAV concatenates those sentences without inference pauses;
predicted playback gaps are retained separately in the sentence delivery trace.

Readiness means the whole sentence has been generated and copied to host PCM,
with a resident model, before peak normalization, disk writes or sound-device
queues. No audio is automatically played. `load_s` and first-call times are
separate cold-path evidence. An under-two-second warm result is not a promise
of under-two-second cold startup. Sentence delivery traces retain all input
sentences, synthesis and playback duration, RTF, first informative sentence
readiness and predicted playback gaps. Average RTF < 1 alone does not imply
uninterrupted playback.

Qualifying for the requested voice recommendation requires coverage of all six
languages, every observed warm first-sentence probe for its selected primary voice <= 2 s, each primary brief
first sentence <= 2 s, and each full brief RTF < 1. Only cleared routes qualify;
Kokoro stays a GPL-component comparison while consent is false. Quality ranking
uses back-ASR as an intelligibility proxy, not an unsupported naturalness score.
If no cleared model passes, the report must say so and avoid silently relabelling
a slow fallback as compliant.

Back-ASR runs only after synthesis processes exit. Reuse/download Qwen 1.7B
through `multilingual_assets.py qwen17` before the following commands. The
verifier uses its automatic low-energy, roughly 30-second chunking, an explicit
1024-token cap per chunk, and rejects capped output instead of attributing
verifier truncation to TTS quality:

```sh
nice -n 15 .venv/bin/python multilingual_backtranscribe.py
nice -n 15 .venv/bin/python multilingual_samples.py --verify-dir ~/src/model-survey-samples-2
```

`samples-2.json` lives in the parent eval directory. It records exact PCM file
sizes, hashes, sample rates, duration, model/language/voice and input text. It
includes full briefs and separate reference metadata; no audio is committed.

## OCR and validation

```sh
nice -n 15 .venv/bin/python multilingual_ocr.py
nice -n 15 .venv/bin/python multilingual_ocr.py --warm-language
nice -n 15 .venv/bin/python -m unittest discover -p 'test_survey*.py'
nice -n 15 .venv/bin/python test_multilingual.py
nice -n 15 python3 survey_manifest.py
nice -n 15 python3 validate_survey.py
nice -n 15 .venv/bin/python validate_multilingual.py --samples-dir ~/src/model-survey-samples-2
```

Do not run OCR while another evaluation model holds the lock. The generated
screens are CC0 public test strings, not captures of the user's computer.
Thirty JPEGs cover Japanese, Korean, Spanish, French and German at six font/
theme/resolution combinations. The app-equivalent Chinese-first request,
automatic language detection with supported languages, and a known-language
oracle are all measured. The additional automatic run discards one warmup per
language and retains those timings explicitly. Supported locales are queried
independently for text and document recognition.

The French six-step Ming follow-up uses the alternate US_James voice after the
five common sentences scored 0% WER versus UK_Charlotte's 10.64%. It preserves
an immutable original-run snapshot, appends a new full brief and greeting probes,
and switches `primary_voices.fr` only if the new measured profile passes the
same speed gates. The reused short samples remain unchanged. Reproduce it after
the initial back-ASR pass, then verify only the newly added audio through the
resumable back-ASR runner:

```sh
nice -n 15 .venv/bin/python multilingual_assets.py ming_tts
nice -n 15 .venv/bin/python multilingual_tts.py ming_fast --brief-voice fr US_James
nice -n 15 .venv/bin/python multilingual_backtranscribe.py
nice -n 15 .venv/bin/python multilingual_samples.py --verify-dir ~/src/model-survey-samples-2
```

This is disclosed voice selection using the pilot prompts, not a held-out
quality estimate. The original primary remains a visible comparison.

After saving validated evidence, copy `disk-checks.jsonl` to the result directory,
remove this run's model/data cache, both virtual environments and generated
bytecode, and retain only the external listening samples. Run repository
pre-commit checks before committing. Never delete another worktree's cache.
