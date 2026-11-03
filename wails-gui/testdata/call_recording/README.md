# Synthetic call speech

`two_channel.wav`: 13.495 seconds, 16 kHz stereo signed 16-bit PCM, with a silent
second before and between the lines. Left represents the microphone; right
represents system audio. Generated offline with FFmpeg's libflite filter, voices
`slt` (left) and `rms` (right); no person or microphone was recorded.

Script, in order:

1. Left: "Let's begin the review."
2. Right: "The recorder keeps both channels."
3. Left: "Please send the report today."
4. Right: "I will send it after lunch."

Each line was synthesized separately with
`ffmpeg -f lavfi -i 'flite=textfile=<line.txt>:voice=<voice>' -ar 16000 -ac 1 <line.wav>`.
The mono samples were placed on alternating channels, padded with silence on the
other channel and interleaved into this WAV. The checked-in file is the stable
fixture; a different Flite build may synthesize slightly different timing.

`TestMeetingRecordingSpokenFixtureEndToEnd` uses the actual local SenseVoice and
Silero models, rotates activity between sibling chunks and tears down/recreates
ASR mid-call, then checks every spoken line once in order and the exact stereo
sample count. It runs when `YANSU_MEETING_E2E_MODELS` names the standard model
folder. The always-running PCM fixture test verifies interruption/retry, replay,
Library, memory and silence without requiring models.

`two_remote_voices.wav`: 14 seconds, 16 kHz stereo PCM. Synthesized offline with
FFmpeg's Flite filter, with the same mono conversion and interleaving method:

- Microphone, at 2 s, voice `awb`: "I will send the report after this call."
- System audio, at 1 s, voice `rms`: "Please send the report to the team today."
- System audio, at 7 s, voice `slt`: "Thank you, I will review the numbers after lunch."

The microphone overlaps the first system voice. `TestCallNativeModels`, with
`ISOFORM_CALL_TEST_MODELS` pointing to verified `segmentation.onnx` and
`embedding.onnx`, runs actual sherpa segmentation and CAM++ embeddings, then
exercises Rename, second-call matching and Forget through real temporary stores.
The portable `TestCallTurnsTwoTrackTwoVoicesAndOverlap` supplies deterministic
model boundaries to verify the complete two-track turn-building path, including
simultaneous system voices and microphone speech. Neither test opens a device.
