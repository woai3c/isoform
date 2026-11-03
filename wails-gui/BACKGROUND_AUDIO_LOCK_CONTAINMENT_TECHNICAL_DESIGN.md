# Background Audio Lock Containment

Status: implemented and validated on `vision/shadow-agent`

## Problem

The natural-task Shadow release gate correctly stops at `desktop_locked`, and
visual activity capture already skips locked frames. The live audio diagnostics
nevertheless showed five-second microphone and system-audio chunks continuing
to arrive while macOS was at `loginwindow`.

Meeting filtering prevented those lock-screen chunks from reaching ASR or raw
meeting-audio persistence in the observed state, but the native recorder still
opened the microphone and collected bytes in memory. A downstream filter is not
a sufficient privacy boundary.

## Product Contract

1. A locked desktop stops native microphone and system-audio capture at the
   source.
2. Queued, pending, and in-flight audio is discarded on lock. Lock transition
   is not a normal shutdown drain.
3. Startup refuses to open the recorder while the desktop is locked and checks
   again after native startup before publishing a capture.
4. Unlock never changes the user's setting. Capture may resume only when
   background listening remains enabled and privacy pause is off.
5. Lock detection accepts either the OS session-lock flag or the protected
   `loginwindow` system surface. Fast-user switching can expose the latter
   without the former; arbitrary application names do not count.
6. Recorder lifecycle calls are serialized across startup, settings, privacy
   transitions, and shutdown.
7. The boundary creates no transcript, meeting audio, activity event, memory,
   knowledge, Task Episode, Shadow run, Initiative, permission, or authority.
8. Browser preview exposes only the two audio diagnostic reads. It cannot start,
   resume, stop, or reconfigure capture.
9. Dev Tools explains that capture is paused by the desktop lock; a generic red
   `stopped` indicator is not enough to debug expected privacy behavior.

## Runtime Flow

```text
OS session lock
  -> privacy monitor observes a state transition
  -> serialize background-audio lifecycle
  -> clear active capture from App state
  -> stop native recorder
  -> discard pending and queued transcription
  -> release Whisper background-consumer lease
  -> report desktop_locked in Audio input Dev Tools

OS session unlock
  -> confirm background listening is still enabled
  -> confirm privacy pause is off
  -> ensure local speech engine is ready
  -> start a fresh native recorder
  -> recheck lock before publishing it
```

The monitor reacts only to state transitions. It does not repeatedly restart a
failed recorder or override the user's configuration.

## Verification

1. Prove initial unlocked, repeated, lock, and unlock transition semantics.
2. Prove a locked startup cannot create native capture even if the feature is
   configured.
3. Prove lock stops the recorder and discards a queued microphone chunk without
   ASR or `onSpeech` delivery.
4. Run focused and race tests, the complete Wails package, vet, frontend tests,
   and the production frontend build.
5. Rebuild while macOS is genuinely locked. Confirm Audio input reports the
   lock reason, recorder status is off, chunk logs stop, and speech/meeting
   ledgers remain unchanged across an observation window.
6. Unlock only through the normal user path before testing automatic resume.

## Validation Result

- Focused lifecycle and Browser RPC tests pass, including a fake native
  recorder proving that lock stops capture and drops queued microphone audio
  before ASR or `onSpeech` delivery.
- Focused race coverage, the complete Wails Go package, and `go vet` pass.
- All 47 frontend test files and 243 tests pass. The production frontend build
  passes with only the established large-chunk warning.
- The rebuilt live process starts at `loginwindow`, reports that the desktop is
  locked, and refuses to create the native recorder after the local speech
  engine becomes ready. No audio-chunk logs follow startup.
- Across the locked observation window, the activity database remained fixed
  at 42,162 speech events and stored meeting audio remained fixed at 393 WAV
  files with the same newest artifact.
- Read-only Browser RPC reports `running=false`, `screenLocked=true`, and
  `suspendedReason=desktop_locked`. The event ledger reports the source-level
  pause, while `SetBackgroundListening` remains forbidden with HTTP 403.
- Browser QA confirms the functional Audio input surface uses the expected
  amber `paused` state, explains the desktop-lock boundary, refreshes on
  demand, and displays the matching lifecycle receipt.

## Remaining Native Gate

Automatic resume has not been exercised against a genuine macOS unlock because
the desktop is still at `loginwindow`. Unit coverage proves the transition and
startup guards, but the normal user unlock path remains an explicit live QA
gate. No lock bypass or synthetic unlock was used.
