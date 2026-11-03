# Call turns and local voice memory

Backlog item 5, step 2; design approved 2026-10-19. Built on #62's aligned
microphone/system tracks, #67's Activity notes and the existing sherpa-onnx
v1.13.0 runtime. No new service, executable or microphone capture path.

## Models and installation

The existing voice setup (`InitVoiceInput` -> `ensureSherpaAssets`) installs the
additional segmentation asset under `sherpa/speaker`. Call processing and Rename
only open verified installed files; neither starts a download or changes consent.
Existing speaker-embedding URL overrides must supply the pinned bytes too.

| Model | Version | Size | SHA-256 |
|---|---|---|---|
| pyannote segmentation 3.0 int8 | Hugging Face `csukuangfj/sherpa-onnx-pyannote-segmentation-3-0`, revision `9403a6902bb58e3d5ae8c7e77c3422de279db2e0` | 1,540,506 bytes | `d582f4b4c6b48205de7e0643c57df0df5615a3c176189be3fc461e9d18827b5d` |
| 3D-Speaker CAM++ 16k-common | sherpa upstream release asset `198893103` | about 28 MB | `f682b514c05d947ee3fa91cd6ec6c5c7543479a128373fa29b1faedccd21fd11` |

Both hashes were checked against downloaded upstream bytes. Segmentation is MIT;
3D-Speaker is Apache-2.0. Notices are in `wails-gui/THIRD_PARTY_NOTICES.md`.
The models run on the CPU with one thread each, only after capture. Five-minute
windows bound PCM allocation; acoustic embeddings align clusters between windows.
Long calls can still take time to process and native accuracy needs call QA.

## Turns and names

The microphone is always You, independently of enrollment. System speech is
segmented and clustered by sherpa, then each segment is transcribed with the
existing long-form engine. Sorting preserves overlapping timestamps and both
tracks, including repeated words. A voice change or pause starts a turn.
New mono recordings keep their track source in the sidecar, through recovery
and compression: microphone-only audio stays You, and system-only audio still
gets diarization. Legacy mono files without source proof keep an unknown label.
Short adjacent fragments from the same speaker can join. Transcript files carry
generic labels; names and signatures are never added to model-facing transcripts.

The Activity transcript uses structured turns and localized labels. Naming uses
a strong remembered voice match (cosine >= 0.85, margin >= 0.10 over a competing
name), or a single remote voice with a single non-declined invitee. An exact,
unique email match in the consented Contacts cache supplies its display name.
Multiple invitees are never assigned by list order. Otherwise the window shows
Speaker 2, Speaker 3, and so on across the call. The user can tap any remote label
to change every turn of that speaker in the call. Rejoined parts can reuse an
identity by comparing clean speech from the earlier retained recording.

Existing recordings rebuild on first transcript open when local models and ASR
are available. Otherwise timestamped legacy lines render with source labels and
a visible limitation message; they can be renamed. Installing models permits a
later rebuild. Missing/retired audio cannot supply acoustic identities. Short
replies without enough voice evidence may remain separate numbered speakers.
Only turns explicitly marked as legacy text are eligible for that later rebuild.
Completed audio turns are reused on subsequent loads, even when a speaker remains
unknown. Speaker labels never determine whether audio needs processing. The
legacy provenance also preserves the limitation message across reloads.
Mixed system speech can retain both detector spans, but the models do not separate
the mixed waveform; recognition of simultaneous remote voices can be imperfect.

## Persistence and privacy

Named activity migration `meeting_call_turns` adds turns, call-wide labels,
`meeting_voice_signatures` and a Forget generation. Shared storage's numbered
schema/ceiling do not change. A signature contains the embedding model version,
name, embedding and source meeting/speaker. Unnamed candidate embeddings are
transient. Naming extracts at least 1.5 seconds of clean system speech (at most
10 seconds), excluding other overlapping system speakers. If no clean recording
is available, the text rename succeeds without learning a new signature.
The additive named migration `meeting_call_turn_provenance` records legacy-text
provenance on turns. Existing rows default to non-legacy: their origin cannot be
inferred safely from an unknown speaker label, so opening them preserves their
saved text instead of starting another audio pass.

Signatures stay in the activity database. They have no exported DTO, identity
handle, transcript/header, model context, phone route, sync path or event payload.
Automatic matching never enrolls another sample: deletion of the original
teaching call therefore removes its voice, unless independently named samples
from other calls remain. Names inferred from a voice carry a read dependency on
the teaching call. Protecting it hides those inferred names and stops matching.

Every read, rebuild and rename uses #67's whole-meeting gate and #62's recording
dependencies: Closed Eyes, excluded/sensitive apps, protected/deleted sources and
pauses fail closed. Production writes retain the automatic-recording epoch fence.
Rename checks privacy before and after extraction and after commit. Forget bumps
a durable generation so in-flight extraction cannot repopulate erased signatures.
It also removes older explicit voice enrollments and refreshes the Self cache,
with a separate generation fencing any in-flight explicit enrollment. Existing
event-JSON microphone embeddings are erased too; new Activity events omit that
field from persistence and general DTOs entirely. Existing
names written in notes remain. Deleting notes cascades through signatures and
turns; explicit activity-session deletion also removes that call's signatures.

## Verification and revert

Portable Go fixtures cover two tracks, two system voices, overlaps, invite/contact
naming, call-wide renames, second-call matching, privacy and erasure. A
two-load regression checks that a legacy upgrade leaving an unassigned speaker
does not diarize or transcribe again on the second load. The optional
`ISOFORM_CALL_TEST_MODELS` fixture runs actual local segmentation and embeddings,
public Rename, second-call matching and Forget on Flite speech. No device or app
is opened. Frontend tests cover turns, localized renames, errors, stale privacy
responses and the Forget control.

Revert the feature commit to restore the old view and processing. Keep the additive
tables; do not roll back the numbered storage schema. To remove retained biometric
data before a code rollback, use Forget voices first. Revert does not resurrect
erased signatures or recordings. Restoring old code removes the new Forget
control. Installed database deletion triggers remain; retain equivalent erasure
while keeping any signature table.
