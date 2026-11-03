# Product telemetry

Isoform uses PostHog for anonymous product usage and retention measurement.
The desktop integration builds on the existing `posthog-go` client: it owns
batching and delivery, while Isoform restricts the event and property schema,
checks consent, and bounds shutdown work. There is no account identity.

## Build configuration

Supply both variables when running `wails-gui/scripts/build-wails.sh`:

| Build environment variable | Purpose |
|---|---|
| `ISOFORM_DESKTOP_POSTHOG_KEY` | The desktop project's PostHog ingest key. |
| `ISOFORM_DESKTOP_POSTHOG_HOST` | The ingest origin, such as `https://us.i.posthog.com`. |

`YANSU_DESKTOP_POSTHOG_KEY` and `YANSU_DESKTOP_POSTHOG_HOST` are still read as a
deprecated fallback, with a warning; the build stops when both names are set
with different values.

Never put values in source control. The script injects
`main.DefaultDesktopPostHogKey` and `main.DefaultDesktopPostHogHost` into the
binary. Both default to empty; missing either disables sending. This applies to
release and `--flavor isoform` builds. Runtime environment variables cannot
enable an unconfigured binary. Trailing slashes on the ingest host are removed
before the SDK appends its `/batch/` path.

The script does not print the values. It rejects whitespace, quotes, and
backslashes before placing either value in Go's linker arguments, and redacts
the key from the `wails build` output, which prints the linker flags. The key
is an ingest key intended to ship in a client, not a PostHog personal API key.

### Release key file

Releases are built by `scripts/release/ship.sh` from a fresh worktree per
release, so the settings live in one file outside every checkout:
`~/.config/isoform-release/posthog.env`, owned by the release owner, mode 600.

```bash
export ISOFORM_DESKTOP_POSTHOG_KEY=<project ingest key>
export ISOFORM_DESKTOP_POSTHOG_HOST=https://us.i.posthog.com
```

`scripts/release/posthog-env.sh` parses the two assignments (the file is never
executed), refuses a file other people can read, and exports both values for
`build-wails.sh`. `ISOFORM_RELEASE_POSTHOG_ENV` names a different file.

- `--channel stable` stops before any build when the file or either value is
  missing. `--no-telemetry` builds without PostHog on purpose; the choice is
  recorded in the rehearsal's `telemetry.txt`, and `--publish` refuses a
  rehearsal built the other way.
- `--channel beta` builds without telemetry when the file does not exist.
- `ship.sh` refuses to run when `wails-gui/.env.local` sets
  `ISOFORM_DESKTOP_POSTHOG_*` (or the deprecated `YANSU_` names), since
  `build-wails.sh` would let it replace the
  release values.
- `scripts/release/verify-artifact.sh` reads the binary's build info with
  `go version -m`, logs it with the key redacted, and checks with quiet
  matches that the key and host are present and equal to the file's values
  (`ISOFORM_EXPECT_TELEMETRY=on`), or absent (`off`).

To check delivery without building or launching the app, run the opt-in
integration test. It reads the same file, sends one `app.open` through the
desktop sender (same client options and consent transport) with the marked
test install ID `7e570000-0000-4000-8000-000000000001`, flushes, and prints the
event UUID. It never prints the key.

```bash
cd wails-gui && ISOFORM_POSTHOG_INTEGRATION=1 go test -run TestPostHogIngestIntegration -count=1 -v .
```

The insights to build from these events are in
[the dashboards spec](product-telemetry-dashboards.md).

## Consent and identity

Anonymous product telemetry is **on by default** (user decision, 2026-10-16).
The welcome step in onboarding explains the counts and offers a switch before
setup continues, with a link to the full in-app description. Settings > Privacy
keeps its own switch. Both read and save the same `GetTelemetryTier` /
`SetTelemetryTier` preference and follow `telemetry:tier-changed` notifications;
reopening setup preserves a saved opt-out. On maps to `detailed`, off to `off`.
Neither the default nor the switch broadens the event contract below: the
current product sender counts use and active time, not errors, and activity
includes a random session ID as well as the random install ID.

Every event checks the existing telemetry tier. **Share diagnostic data off
means no queued events and no telemetry network calls.** The transport checks
the tier again before each request. Revoking consent discards the old queue;
opting back in starts with an empty queue. The tiers do not permit observed
content in product telemetry.

The anonymous `distinct_id` is a random install UUID stored in
`telemetry_install_id` inside the app's data folder. It survives app restarts
and is independent of names, email, accounts, device names, and machine IDs.
The Isoform flavor uses its own data folder and therefore its own identity.
First-brief and daily-ping markers live in `telemetry_product_state.json` in
the same folder. Daily retention dates use UTC.

The first-brief marker is saved locally even while sharing is off. This does
not create a sender or install identity, enqueue an event, or make a network
call. That view is never replayed if sharing is enabled later.

## Event contract

Only the events below are allowed. Unknown legacy agent events are dropped.

| Event | Meaning |
|---|---|
| `app.open` | The desktop app opens. |
| `app.active_time` | A bounded interval of active, foreground desktop usage. |
| `app.daily_active` | At most one active-day ping per install per UTC day. |
| `brief.first_seen` | The install's first brief view. |
| `chat.turn_sent` | A sent chat turn, counted without its text. A validated Today source can identify its surface and product-map action. |
| `playbook.run` | A successfully completed playbook run (terminal `ok`), without its name, instructions, or output. Running, skipped, and failed attempts are not counted. |
| `iphone.paired` | A completed iPhone pairing, without device or person details. |
| `today.action`, `phone_today.action` | A Today action's product-map ID, kind, outcome, and elapsed milliseconds. |
| `today.section_seen` | A visible Today section and the number of rows shown. |

All events carry `app_version`, `flavor`, `build_channel` (`release` for
`build-wails.sh` release builds, otherwise `development`), `os`, `os_version`,
`locale`, and `telemetry_tier`. Activity events also use a random session UUID and numeric
duration. Event-specific properties are allowlisted; arbitrary caller
properties cannot pass through. PostHog person-profile processing is disabled,
and `$geoip_disable` prevents IP-based geographic enrichment.
Locale is cached from the persisted language setting at startup and updated
only after a successful language save; event capture does not read SQLite.

`$geoip_disable` does not discard the source IP. Even though the event schema
does not include an IP property, the ingest request still exposes the client's
source IP to the receiving service. The project must separately enable
**Discard client IP data**, and the release owner must verify the stored-event
result before shipping a configured key. An anonymous install UUID does not
make the network connection anonymous. Ingest proxies and service access logs
have their own retention settings; the PostHog project setting does not prove
those logs discard IP addresses.

The schema never includes screen text, audio, file names, URLs, people, memory,
chat text, playbook content, or agent tool inputs and outputs. No browser
autocapture, page views, session recording, or identify calls are enabled.

## Release gate: verify IP discard before adding a key

The release evidence is tracked in
[the release sign-off record](hub-reviews/release-telemetry-sign-off.md).

**Current status: signed off on 2026-10-28** for project isoform-prod (221015)
on `https://us.i.posthog.com`, with the evidence in that record. The checklist
below is the template for any other project or host: unit tests cannot
establish a deployed project's IP-retention policy, so keep
`ISOFORM_DESKTOP_POSTHOG_KEY` unset until every item below is verified for it.
`TestPostHogIngestIntegration` sends the test event without launching the app;
do not ship a telemetry-enabled build while the gate is incomplete.

- [ ] Enable **Discard client IP data** in the exact PostHog project's settings.
  Record the project ID, ingest host, verification time (UTC), reviewer, and a
  redacted screenshot or settings-record link in the release evidence. Never
  put a key in source, screenshots, or the evidence record.
- [ ] Inspect a stored test event from an opted-in test install against that
  same project and host. Inspect the raw event after ingestion, not only the
  outgoing request. Confirm `$ip` and `ip` contain no source address, no other
  property retains it, and no IP-derived location fields were added. Record
  the event UUID and a redacted screenshot or query result. A request-body
  unit test or `$geoip_disable: true` alone is not this proof.
- [ ] Review any ingest proxy, access-log, and export retention separately.
  Record the applicable IP-retention policy and any configured discard rule;
  do not infer log deletion from the project setting or the stored-event check.
- [ ] Record release sign-off with links to the settings and stored-event
  evidence, the build commit/version, and the reviewer. Recheck the gate when
  the project, ingest host, proxy, or ingestion/export configuration changes.

The release pre-flight and build-script header link to this checklist. The
documentation regression test checks that the gate and evidence requirements
remain present; it does not verify any remote project setting.

## Active time and delivery

The frontend reports a heartbeat every 30 seconds while the app is focused
and visible and has received input in the last five minutes. An idle gap
starts a new random session. Gaps longer than 60 seconds are not backfilled,
so sleep or a suspended WebView cannot inflate active time.

The Go client sends batches of up to 20 events and flushes every 30 seconds.
The SDK's `MaxEnqueuedRequests` is 50 pending batches, in addition to its
200-event input buffer and any in-flight uploads. Full batch queues discard
new batches immediately. Requests time out after two seconds, and the SDK's
`MaxRetries` is explicitly zero: a failed batch is discarded after its first
attempt. There is no durable upload queue.

Quit starts a two-second asynchronous flush at the beginning of app shutdown,
alongside service cleanup, then joins the flush after cleanup. The window
callback does not wait on HTTP, and retries cannot keep the app alive. An
immediate process exit can lose the final batch.

Tests use an `httptest` PostHog receiver to check the allowed event shapes,
metadata, batching, off-tier queue and network behavior, and quit flushing.
They also cover stable anonymous identity and daily/first-brief deduplication.
