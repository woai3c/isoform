# Observation Pause Audio Containment

Status: implemented and validated on `vision/shadow-agent`

## Problem

The existing privacy control stops activity monitoring and cancels background
understanding, but background audio uses an in-place transcription pause. The
native microphone and system-audio recorder remains open and continues reading
chunks that are discarded later.

That behavior violates the meaning of an explicit privacy pause. A user-facing
pause must stop acquisition at the source, not merely suppress downstream
interpretation.

The visible name `Closed Eyes Mode` also describes an internal metaphor rather
than the user's action. It does not clearly promise what happens to audio.

## Product Contract

1. **Pause observation** is the user-facing name. Internal `closedEyes` symbols
   may remain to avoid unrelated migration churn.
2. Entering the pause stops activity monitoring and native microphone/system-
   audio capture. It discards queued and in-flight transcription.
3. The pause creates no transcript, audio file, activity event, memory,
   knowledge, Task Episode, Shadow run, Initiative, permission, or authority.
4. Yansu remembers whether activity and audio were running immediately before
   the pause. It does not turn disabled sources on later.
5. Resuming observation starts a fresh audio recorder only when audio was
   previously running, background listening remains enabled, the desktop is
   unlocked, and the local speech engine is available.
6. Resume never changes a user setting, bypasses a permission, or treats
   confidence/readiness as authority.
7. Recorder lifecycle transitions remain serialized with settings, desktop
   lock, startup, and shutdown.
8. Audio input Dev Tools reports `privacy_pause` as an expected amber pause and
   records source stop/resume receipts in plain language.

## Runtime Flow

```text
User chooses Pause observation
  -> remember which primary sources are running
  -> mark observation paused
  -> cancel background understanding
  -> stop native audio recorder
  -> discard queued and in-flight transcription
  -> release speech-engine background lease
  -> stop activity monitor
  -> show privacy overlay

User chooses Resume observation
  -> clear observation pause
  -> restore activity only if it was previously running
  -> attempt audio restore only if it was previously running
  -> re-check current background-listening setting
  -> re-check desktop lock
  -> ensure local speech engine is ready
  -> start a fresh recorder
```

The order is fail closed: the pause flag is set before source shutdown, and it
is cleared before guarded restart so the shared startup function can evaluate
all current gates.

## UX Language

| Surface | Before | After |
| --- | --- | --- |
| Menu action when active | Turn Off Closed Eyes Mode | Resume Observation |
| Menu action when inactive | Turn On Closed Eyes Mode | Pause Observation |
| Overlay title | Yansu is in Closed Eyes Mode | Observation is paused |
| Overlay message | Metaphor about closing eyes | Yansu is not recording your screen or audio. |
| Overlay action | Turn Off Closed Eyes Mode | Resume observation |

Chinese overlay surfaces use the equivalent direct language: `观察已暂停`,
`恢复观察`, and `Yansu 不会录制你的屏幕或音频。`

## Verification

1. Prove the pause stops a fake native recorder and discards queued microphone
   audio before ASR, storage, or `onSpeech` delivery.
2. Prove the status projection reports `privacy_pause` with no active source.
3. Prove menu action labels describe the next action in English.
4. Keep desktop-lock lifecycle tests green because the two privacy boundaries
   share the same serialized stop/discard primitive.
5. Run focused and race tests, the complete Wails package, vet, frontend tests,
   and the production frontend build.
6. Exercise native pause/resume through the normal user path only after macOS
   is unlocked. Do not bypass `loginwindow` to manufacture proof.

## Validation Results

- Focused pause, background-listening, desktop-lock, status, and transcription-
  filter tests pass. The source-stop/discard regression also passes 20
  consecutive runs.
- Focused race coverage passes with only the established macOS linker metadata
  warning. The complete Wails Go package passes in 210.428 seconds, and
  `go vet ./...` passes.
- All 47 frontend test files and 243 tests pass. The production frontend build
  passes with only the established large-chunk warning.
- The rebuilt native process remains fail-closed at the real `loginwindow` and
  reports `desktop_locked`; no privacy mutation is exposed through Browser RPC.
- A Windows cross-test cannot reach package compilation in the current
  `CGO_ENABLED=0` environment because the existing Sherpa ONNX Windows module
  excludes all source files. This is an environment/baseline limitation, not a
  failure in the Windows overlay wording change.
- Native pause-overlay and fresh-resume visual QA remains pending until macOS is
  unlocked through the normal user path. No lock bypass or synthetic production
  evidence is permitted.
