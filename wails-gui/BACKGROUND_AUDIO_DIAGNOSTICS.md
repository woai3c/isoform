# Background Audio Diagnostics

This note documents the macOS release crash class fixed in the background audio pipeline and the logs to check before changing the native audio code again.

## Release Crash Root Cause

Do not use `AVAudioEngine` for background microphone capture while the system-audio process tap can run in the same app process.

Release-style builds can start both sources at once:

- `system_audio`: CoreAudio process tap plus aggregate device.
- `microphone`: explicit CoreAudio input `AudioDeviceIOProcID`.

The previous microphone recorder used `AVAudioEngine`. When a user switched the default playback device, macOS could rebind the engine's AUHAL device while the process tap was active, terminating the app in native code around:

```text
AVAudioIOUnit_OSX::BindToDeviceInternal
AUInterfaceIOV3::SetCurrentDevice
AUHALOutputUnit setDeviceID
AudioDeviceCreateIOProcID
```

The durable fix is the CoreAudio input IOProc recorder in `background_audio_darwin.go`. Keep the IOProc realtime-safe: no allocation, logging, Objective-C object work, Go calls, or locks in the callback. It should only copy PCM into preallocated memory; Go drains and chunks the ring buffer off the realtime callback thread.

## Rules For Future Changes

- Do not reintroduce `AVAudioEngine` for background microphone capture.
- Preserve input-device restart listeners for default input, stream format, and nominal sample rate changes.
- Preserve the Bluetooth policy: use the default input unless it is Bluetooth, then prefer the built-in microphone to avoid degrading Bluetooth playback quality.
- Treat capture and transcription as separate stages. Captured chunks do not prove the speech engine accepted speech.
- Build a release-style Wails app when testing playback-device switching. Dev builds can miss this path if bundled native libs (sherpa-onnx + onnxruntime in `Contents/Frameworks/`) or production background listening differs.

## Release-Like Reproduction

```bash
cd wails-gui
bash scripts/build-wails.sh --skip-acp --skip-cli --skip-ffmpeg --skip-codex-acp --skip-claude
open build/bin/Yansu.app
```

Enable background listening for both microphone and system audio, play speech through the current output device, then switch playback devices from macOS Control Center or System Settings.

## Logs To Check

Use the app log in the selected flavor home (`~/.isoform-agent/yansu-gui.log` for Isoform development builds, `~/.yansu-agent/yansu-gui.log` for the legacy release identity). Normal capture logging records state transitions and cumulative counters, not every chunk:

```text
CoreAudio microphone recorder started: selection=... device=... rate=...
system audio recorder started: rate=... ch=...
first sound after silence: source=... bytes=... duration=... max_window_rms=...
capture summary: source=... chunks=... asr_eligible=... silence_skipped=... bytes=... duration=...
system capture pause deferred: output_active=... activity_known=...
system capture paused after silence: duration=...
system capture resume failed: failures=... retry_in=... suppressed=...
system capture resumed: output_active=... activity_known=... failures=... suppressed=...
system capture idle summary: pauses=... pause_deferrals=... resume_failures=... suppressed=...
transcription batch ready: source=... chunks=... duration=... bytes=...
accepted transcript: source=... chunks=... chars=... words=...
empty transcript returned: source=... chunks=... bytes=...
dropped short sherpa segment: source=... duration=... min=...
dropped non-speech sherpa segment: source=... event=... lang=... bytes=...
dropped disallowed-language sherpa segment: source=... event=... lang=... allowed=... bytes=...
periodic flush: speech accumulating for ... flushing to DB
persisted speech session: session=... speech=...
```

The capture summary is emitted at most once per minute per source and at capture stop. Its counters cover chunks admitted by the privacy filter: `asr_eligible` passed the noise gate, while `silence_skipped` did not. Both still reach the permitted recording writer and audio taps. A nonzero eligible count does not prove ASR accepted speech; inspect batch, VAD, and transcript counters next. Normal logs exclude transcript text. `YANSU_DEBUG_TRANSCRIPTS=1` explicitly enables private transcript snippets for a diagnostic process; leave it unset for dogfood retention checks.

Speech events are flushed periodically rather than immediately. After a flush, inspect persisted transcript counts with:

```bash
sqlite3 ~/.yansu-agent/activity.db \
  "select json_extract(data,'$.speechSource'), count(*), sum(length(json_extract(data,'$.speechText'))) from activity_events where kind='speech' and session_id='<session-id>' group by 1;"
```

## Noise Gate And Idle Capture Boundaries

The noise gate checks the maximum RMS over rolling 20 ms windows of the 16 kHz, mono, int16 PCM payload. A window at or above 100 admits the whole chunk to VAD and ASR. Averaging all five seconds would lose a short, quiet utterance surrounded by silence, so the gate must not use whole-chunk RMS. Short windows also mean clicks can pass this inexpensive gate; VAD still decides whether they contain speech. Audio below the floor is kept in an allowed meeting recording or subscription even when ASR is skipped.

On macOS 14.2 and later, system capture can stop after more than a minute of low-energy chunks **only when CoreAudio reports that other processes are not running output**. This is an output-activity wake mechanism, not an exact sound detector. An application holding a silent output stream open keeps capture active, avoiding repeated teardown and startup during quiet parts of a call. The noise gate still saves ASR work in that case. Unknown output activity also keeps an active tap running; if the query becomes unknown while the tap is asleep, capture attempts to resume conservatively.

Automatic call recording now keeps the system tap warm while that preference
is enabled. A stopped tap has no pre-roll, so waking it only after detection
could lose the remote opening. The idle policy below applies when automatic
recording is off; the privacy gates still determine what reaches storage or ASR.
This favors call completeness at the cost of some idle power savings. Native
capture also no longer waits for ASR to load: finished calls can be transcribed
when the current engine becomes ready.

While asleep, the wrapper checks output activity every 250 ms. Beginning playback can therefore lose up to a polling interval plus native tap startup time before capture resumes; there is no pre-roll buffer while the tap is stopped. Do not claim sample-accurate wake or preserved first syllables without a hardware measurement. Microphone capture continues independently. Windows and other platforms currently keep continuous capture because this wrapper has no output-activity wake implementation there; their ASR noise gate still applies.

A failed native resume retries after 1, 2, 4, 8, 16, then 30 seconds, capped at 30 seconds. Stopping capture cancels the wait. Recovery resets the retry sequence. The wrapper logs the first failure, at most one further failure summary per minute, and recovery with suppressed-error counts. Native start attempts may also emit their own diagnostics, but the retry cap prevents rapid tap creation and duplicate outer-loop errors.

These boundaries leave two requirements unproven: sleeping through silent but open output streams, and lossless sound onset after idle. Changing either needs a native activity or pre-roll design and Mac measurements, not a tighter polling loop.

## Hub Hardware Verification

Headless tests exercise PCM gating, state transitions, retries, and log volume; they do not establish CPU savings or native wake latency. On the Mac, compare an identical 30-minute sequence before and after the change: idle desktop, a call with quiet intervals, quiet brief speech, and playback beginning after the tap sleeps. Include headphones, microphone paused, and an output-device switch.

Check that short quiet speech reaches ASR, that an open quiet call does not repeatedly pause and resume, that inactive output permits sleep, and that playback wakes capture. Record any missing onset and measure CPU over the same windows. Retain start/end markers and the capture, transcript, and idle summaries from all rotated logs. A passing simulated retention test is evidence of bounded log volume, not evidence of a successful hardware recording or a battery improvement.
