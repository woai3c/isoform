# October 2026 local model survey

Read [../RESULTS-2026-10.md](../RESULTS-2026-10.md) for decisions and limits.
All evaluation inputs are public or generated; no app data is used. Do not
point these commands at application model folders or launch the desktop app.

Run commands from `asr-eval/`. This run uses an isolated `.cache/october/`
model and speech cache. Generated screenshots, raw per-sample results and
the listening sample index are committed; WAVs, model weights and source speech
are not. Listening WAVs are stored in `~/src/model-survey-samples/` on this Mac;
the hub sends them to Bo. `tts-samples/samples.json` pins their metadata/hashes.
The transport/file inventories under `assets/` pin exact bytes and hashes.

```sh
uv venv .venv --python 3.11
uv pip install --python .venv/bin/python -r requirements-october.txt
nice -n 15 .venv/bin/python survey_data.py --count 30
nice -n 15 .venv/bin/python survey_assets.py sensevoice
nice -n 15 .venv/bin/python survey_assets.py sensevoice_nano
nice -n 15 .venv/bin/python survey_assets.py qwen06
nice -n 15 .venv/bin/python survey_hub.py funasr_nano
nice -n 15 .venv/bin/python survey_hub.py qwen17
nice -n 15 .venv/bin/python survey_hub.py mimo_tokenizer
nice -n 15 .venv/bin/python survey_hub.py mimo
```

For the two SenseVoice models, the existing engine expects a `sense-voice/`
subdirectory. Create links wholly inside the eval cache:

```sh
mkdir -p .cache/october/sensevoice-layout .cache/october/sensevoice-nano-layout
ln -s ../sensevoice .cache/october/sensevoice-layout/sense-voice
ln -s ../sensevoice_nano .cache/october/sensevoice-nano-layout/sense-voice
```

Run **one model at a time**, letting its process exit before the next one.
The wrappers require a nice value of at least 10. Use `nice -n 15`; set
`OMP_NUM_THREADS=2`, `OPENBLAS_NUM_THREADS=2` and `VECLIB_MAXIMUM_THREADS=2`
when running the MLX models. The native ASR and TTS engines use two threads.
Download only what fits on disk and delete only your own eval weights after
saving their inventories if necessary. Never delete another worker's cache.

```sh
nice -n 15 .venv/bin/python survey_asr.py sensevoice
nice -n 15 .venv/bin/python survey_asr.py sensevoice_nano
nice -n 15 .venv/bin/python survey_asr.py qwen06
nice -n 15 .venv/bin/python survey_asr.py funasr_nano
nice -n 15 .venv/bin/python survey_asr.py qwen17
# This run timed out during warmup with an 8 GiB MLX limit. Bound any retry:
nice -n 15 .venv/bin/python -c "import subprocess,sys; subprocess.run([sys.executable, 'survey_asr.py', 'mimo'], timeout=600, check=True)"
```

`survey_asr.py` calls the existing `evaluate.run_evaluation` harness. The
historical per-utterance scores remain for compatibility; October decisions
use `corpus_accuracy` (summed edit counts). Mandarin uses CER, English WER,
and mixed speech MER with each Han character and Latin word as a token.
`rtf_corpus` divides total inference time by total audio time. Cold loads and
discarded warmups are separate. CPU 100% means one logical core, not the
whole ten-core Mac. RSS includes the Python runtime; Metal peak is reported
separately and must not be added blindly to RSS. Shared-machine timings are
observations, not exclusive-machine performance guarantees.

```sh
nice -n 15 .venv/bin/python ocr_fixture.py
nice -n 15 swiftc -parse-as-library -O vision_eval.swift -o .cache/vision-eval
nice -n 15 .venv/bin/python survey_ocr.py app
nice -n 15 .venv/bin/python survey_ocr.py documents
nice -n 15 .venv/bin/python survey_ocr.py ppocr6
```

The 30 JPEG fixtures cover public UI labels, Go/TypeScript, and Chinese/mixed
text in light/dark themes and 1x/2x resolution. They are PIL-rendered test
screens, not captures of native app sessions. `vision_eval.swift` retains a
native worker so startup/compilation is not charged to every frame. `app`
uses the production language resolution (`zh-Hans`, `zh-Hant`, `en-US`, then
available CJK languages), accurate recognition and language correction.
The score ignores whitespace and folds fullwidth ASCII like the app, while
preserving case and all code punctuation. The existing
[`wails-gui/cmd/ocr-eval`](../../wails-gui/cmd/ocr-eval/README.md) documents
related baseline evaluation and native WebKit fixture generation; this
standalone worker additionally exercises the macOS 26 Swift document API.
`accurate` and `accurate-zh-first` runs are diagnostic settings, not new models.

TTS uses the same three original sentences per language in
[`tts_prompts.json`](../tts_prompts.json). Sample files are normalized to a
0.95 peak after the timed synthesis, written as PCM16, and never played
automatically. The 300-word throughput probe repeats the same text, so it
measures throughput rather than editorial quality. Chinese uses a repeated
brief of comparable length; its count is characters, not English words.
Synthesis time is distinct from audio playback duration. Back-transcription
is an intelligibility proxy and cannot establish naturalness or warmth.

```sh
nice -n 15 .venv/bin/python survey_assets.py kokoro019
nice -n 15 .venv/bin/python survey_assets.py melo
nice -n 15 .venv/bin/python survey_assets.py kokoro11
nice -n 15 .venv/bin/python survey_hub.py kokoro_mlx
nice -n 15 .venv/bin/python survey_hub.py ming_tts
# Run one command at a time. These are the measured preset voices.
nice -n 15 .venv/bin/python survey_tts.py kokoro019 --extra-en-voice 1
nice -n 15 .venv/bin/python survey_tts.py melo
nice -n 15 .venv/bin/python survey_tts.py kokoro11 --en-voice 0 --zh-voice 3 --extra-en-voice 1 --extra-zh-voice 58
nice -n 15 .venv/bin/python survey_tts.py kokoro_mlx --en-voice af_heart --zh-voice zf_xiaobei --extra-en-voice am_adam --extra-zh-voice zm_yunxi
nice -n 15 .venv/bin/python survey_tts.py ming_tts --en-voice UK_Charlotte --zh-voice 灵小柔 --extra-en-voice US_James --extra-zh-voice 灵信远
nice -n 15 .venv/bin/python survey_speakers.py
nice -n 15 .venv/bin/python survey_backtranscribe.py --engine qwen17
.venv/bin/python -m unittest discover -p 'test_survey.py'
python3 validate_survey.py
python3 validate_survey.py --samples-dir ~/src/model-survey-samples
python3 survey_samples.py --verify-dir ~/src/model-survey-samples
```

Do not load a TTS model and an ASR verifier together. Finish synthesis in
separate processes first, then run the ASR verifier over the saved WAVs.
Every runtime adapter performs real local inference; the metric unit tests
contain no model integrations or mock API results.

Full-brief inputs use greedy sentence groups capped at 200 Latin or 80 Han
characters, preserving all text. Do not credit an unchunked Chinese Kokoro
probe: that path truncates above 510 phonemes. `--brief-only` retains completed
listening samples while replacing the throughput probes and saving the prior
run under `diagnostics/`. Completed runs have `status: complete`.

The final dependency snapshot omits optional PyTorch/curated-transformer
packages that Misaki's broad English extra installs but the small spaCy G2P
pipeline does not use. They were present for the initial short Kokoro MLX
exports, and removed before the final long probes to free eval disk space.
The initial snapshot is `requirements-initial.txt`; model bytes, seed, G2P
model and prompt texts are identical. Model/high-water memory observations
include that environmental distinction and are not a production memory budget.

`sources.json` is regenerated from the fetched hub/release metadata by
`survey_sources.py`. `survey_manifest.py` combines the inventories and completed
run evidence; `validate_survey.py` checks checksum/size arithmetic, completed
sample counts, licence states/evidence and the one-recommendation-per-job invariant.
Its standard-library-only default validates committed metadata without the WAVs;
`--samples-dir` additionally checks all external WAV hashes and PCM headers.
Every sample inventory entry also includes exact `size_bytes`, measured from
the WAV and retained in its completed TTS run so the index can rebuild offline.
External verification checks byte size independently of the checksum.
The TTS and back-transcription runners accept `--samples-dir` and default to
`~/src/model-survey-samples/`; synthesis refuses an output folder inside this
eval repository. Historical `path` fields remain logical IDs, resolved by the
filename in the external folder. `survey_samples.py` rebuilds the sample index
from completed runs without synthesizing audio.

The MiMo attempt did not finish warmup by the 600-second process deadline.
Its JSON records zero scored utterances and null accuracy/RTF; the validator
requires that failure to stay explicit. The cache limit was 128 MiB and the
MLX memory limit 8 GiB, set in this survey's config to bound shared-host use.
The base engine leaves its upstream memory limit alone unless configured.
A retry on a larger/quiet machine is a different configuration and should
retain this failure record before replacing it with completed measurements.

Hub downloads reuse each committed asset inventory's revision.
`survey_manifest.py` can rebuild from the committed `sources.json` when the
ignored raw metadata cache is absent; it fetches only small pinned metadata
files whose upstream Git blob IDs are not SHA-256 file hashes. Historical
large-weight SHA-256 values are explicitly labelled as upstream LFS hashes,
not as October download verification.

Round-1 licence review changes the live recommendation to Qwen3-ASR 0.6B int8
with the speaker pack. SenseVoice is pending owner confirmation and disabled
for selection. Kokoro is restricted by its GPL eSpeak component; bundling
requires source-offer/notices compliance. Ming is the cleared voice default
without that component. See the result report and each manifest entry's evidence.

After verification, this survey's temporary `.cache/`, `.venv/`, raw metadata
and Python bytecode folders are removed. The external samples folder is kept.
Recreate the isolated environment/cache using the commands above for a rerun.

The older `data_prep/download.py` entrypoints now pass a Hugging Face
`cache_dir` under the configured evaluation data directory (`data_dir/cache`).
The offline cache regression tests use the real AudioFolder builder and export
API with generated local audio/metadata; they do not mock dataset results.

Round 2 makes `gplComplianceAccepted` a top-level manifest boolean, default
false. `python3 survey_manifest.py` generates the cleared Ming voice default.
Kokoro is an alternative labelled "GPL component". For an explicitly accepted
variant, pass `--gpl-compliance-accepted --output PATH`, then validate it with
`python3 validate_survey.py --manifest PATH`. The flag acknowledges its named
restriction; it does not perform the source-offer/notices work. All jobs use
the same gate: only cleared defaults or specifically accepted restrictions
are permitted, while pending licences and unrelated restrictions stay blocked.

The new policy/size guards need only the standard library and no model cache:

```sh
PYTHONDONTWRITEBYTECODE=1 nice -n 15 python3 test_survey_policy.py
```

Keep generated acceptance variants in temporary folders and delete them after
review. The committed manifest keeps `gplComplianceAccepted: false`.
