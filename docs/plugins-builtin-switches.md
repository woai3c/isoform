# Built-in plugin switches

Phase 1 exposes existing Isoform feature switches. `internal/plugins/builtin.go`
is the catalogue and `plugin_api.go` adapts its reads and writes. There is no
new settings store, migration, third-party loader or background initialization.
`ListPlugins` never applies a dependency plan or writes defaults. It isolates
read failures to the affected row (`readErrorKey`); other rows remain usable.
A toggle touching an unreadable row or dependency is refused before any write.
Permission-backed rows follow the existing platform catalogue: 53 controls on
macOS, 48 on Windows and 47 on Linux.

## Source and writer inventory

| Plugin id | Existing source | Only writer used |
|---|---|---|
| authority, cron-api, notification-pacing | Always enabled host essentials | None; disabling is refused before any settings read |
| activity-monitor | `GetActivityMonitorStatus().Monitoring` (runtime) | `SetActivityMonitorEnabled`; enabling first checks the existing observation pause and Watch my screen host gates |
| background-listening | Existing configured/disabled config flags through `GetBackgroundListening` | `SetBackgroundListening` |
| background-microphone | `background_microphone_paused` inverse on macOS; background listening elsewhere | `SetPermissionEnabled("microphone", ...)` |
| meeting-recording | `automatic_meeting_recording_disabled` inverse | `SetAutomaticMeetingRecordingEnabled` |
| calendar-sync | `permission_enabled:calendars` | `SetPermissionEnabled` |
| calendar-peek | `calendar_peek_enabled` | `SetCalendarPeekEnabled` |
| mail-sync | `permission_enabled:automation_mail` | `SetPermissionEnabled` |
| reminders-sync | `permission_enabled:reminders` | `SetPermissionEnabled` |
| files-activity | `permission_enabled:files` | `SetPermissionEnabled` |
| clipboard | `permission_enabled:clipboard` | `SetPermissionEnabled` |
| triage-memory, triage-knowledge, triage-shadow, triage-sop, triage-events | Corresponding `triage_enable_memory`, `triage_enable_knowledge`, `triage_enable_shadow`, `triage_enable_sop`, `triage_enable_events` | `SaveUIState`, as in Learning & memory |
| shadow-confidence | `shadow_confidence_adjustment_enabled` | `SaveUIState` |
| work-pattern-profile | `work_pattern_profile_influence_enabled` | `SaveUIState` |
| memory-dream, memory-dream-llm | `dream-settings.json`: `enabled`, `llmEnabled` | `MemoryDreamService.SetSwitch`, patching only the requested boolean and retaining unknown or unnormalized unrelated values |
| daily-news | Existing hidden Daily News cron job's `Enabled`; the existing unprovisioned default is on | `UpdateCronJob`; an explicit off before provisioning first uses `ensureTodayNewsJob` |
| topic-finds | `topics/topics.json`: nullable `enabled` | `SetTopicFindsEnabled` |
| auto-speak, auto-speak-automation, auto-speak-notifications | Respective nullable `auto_speak_*` config fields | Respective `SetAutoSpeak*Enabled` setters |
| browser-bridge | `GetBrowserBridgeEnabled()` (runtime, matching the existing Settings control) | `SetBrowserBridgeEnabled`; config and environment still determine startup |
| cursor-overlay | Nullable `cua_cursor_overlay_enabled`, existing getter default | `SetCuaCursorOverlayEnabled` |
| start-at-login | `GetStartAtLogin()` OS login-item state, matching General | `SetStartAtLogin` |
| text-channel | `text-channel.json`: `enabled`; listing uses the existing file reader without probing Messages | `SetTextChannel`, preserving the number and name setting, never accepting consent on the user's behalf |
| mobile-connection | `mobile/relay.json`: `enabled` | `EnableMobileConnection("")` / `DisableMobileConnection`, retaining the saved relay and pairing |
| mobile-brief, brief-podcast | `mobile/brief-schedule.json`: `enabled`, `feed` | `SetMobileBriefSchedule`, preserving times and the other switch |
| notify-* | Every `engagement.AllKinds()` entry, existing effective preference/default | `SetEngagementKindEnabled` |

The 19 notification ids are `notify-work_blocked`, `notify-work_finished`,
`notify-playbook_result`, `notify-commitment_slipping`, `notify-meeting_prep`,
`notify-capture_paused`, `notify-meeting_ended`, `notify-meeting_followup`,
`notify-brief_ready`, `notify-calendar_conflict`, `notify-waiting_on_reply`,
`notify-brief_missed`, `notify-schedule_missed`, `notify-suggestion`,
`notify-topic_finds`, `notify-playbook_suggested`, `notify-goal_check_in`,
`notify-person_reappeared`, and `notify-learned_to_confirm`. Individual
notification kinds can be disabled; the pacing and authority services cannot.

Missing ordinary `ui_state` switches retain their existing on default. Files
activity alone defaults off. A nonempty value is off only when exactly `false`;
values such as `FALSE`, `0` and ` false ` are not silently rewritten or
reinterpreted. Work-pattern influence retains its existing trimmed,
case-insensitive `true` rule and off default. Config pointers, inverse booleans
and file defaults retain their existing interpretations. Runtime activity and
bridge rows deliberately reflect the existing runtime getters, not a promise
that a saved preference successfully started a service.

## Switch-off retention

Switching off mail, reminders, recent-file collection or topic finds pauses
future collection without deleting saved observations or derived records.
Topic finds also keeps its journal, visited links, interests, results, hidden
schedule, run history and chat log; the existing schedule resumes in place.
Mail also retains saved headers and reply reminders when macOS refuses access;
only explicit Mail protection deletes them. Reminders still removes its saved
rows and closes derived commitments when macOS access is revoked. Private-folder
and protected-app redaction, retention rules and explicit topic deletion keep
their existing behavior. Turning off automatic meeting recording discards an
unfinished automatic capture but keeps saved meetings. Settings describes these
per-switch effects instead of promising blanket data retention.

## Dependencies and failure behavior

Shadow confidence and work-pattern influence require the shadow observer;
the Dream LLM layer requires Memory Dream. Turning a provider off applies
dependent setters before the provider. Turning a dependent on applies the
provider first. The three speech categories are independent. Reading the list
leaves pre-existing inconsistent preferences untouched.

The registry independently owns scoped lifecycle contributions: dependency
order, reverse disposal, and rollback if an apply fails. Phase 1 does not move
the existing app services into registry startup. The Settings adapter calls
existing setters; if a setter fails, it stops and reports the error. Earlier
successful setters retain their documented effects, because these independent
services do not provide a common transaction or reversible runtime operation.

## Switches that are not independent plugins

- Browser history shares `screenWatchingEnabled()` (`screen_recording` and
  `accessibility` permission switches), and respects observation pause. It has
  no independent feature boolean or setter. The activity-monitor row does not
  grant these permissions or turn them back on.
- Desktop delivery has no app-owned global boolean. The OS notification grant,
  engagement kind preferences, and per-playbook delivery channels control it.
  There is no invented desktop-channel setting.
- Private windows, sensitive apps, PII masking, closed eyes, global observation
  pause and OS grants are host gates. They are absent from the plugin switch
  list and are never changed by dependency resolution. Source enablement rows
  do not grant macOS permission or override any of these gates.
- Chat apps (Slack, Teams, Feishu/Lark, RingCentral, Discord, Telegram, WeChat)
  use account connection/setup/disconnection and scan filters, not feature
  booleans. Their credentials and connection lifecycle stay in Chat apps.
  Email and hosted SMS channels likewise use setup/verification records.
- Computer-use policy is per-app authority/access configuration. The actual
  boolean cursor-overlay feature is mapped; permission to control an app is
  not a plugin dependency. MCP servers and skills have their own per-installed
  item switches, outside the compiled-in catalogue.
- `triage_enable_automation` and `triage_enable_handoff` remain historical
  preferences read by the legacy flags object; production summary triage
  forces those features off after Agency removal. No rows resurrect them.
  Background Understanding is also retired and no coordinator starts.
- Self Dream rollout and its production gate are evidence/release controls,
  not user feature switches. Memory Dream's `dryRunOnly` is a destructive-write
  safeguard and remains in its preview/confirmation flow.
- Notification level is a multi-setting preset; quiet hours, pacing budgets,
  mute windows, retention, model/provider selection, telemetry tiers, filters,
  consent, private folders and per-schedule/per-playbook settings are options
  or individual records, not global plugin enablement. They remain on their
  current Settings or product surfaces.
- `use_yansu_gateway` is a retired provider preference forced on by its
  existing config reader. Background-listening source selection is a legacy
  no-op. Neither is exposed as a working plugin switch.

The catalogue does not implement the separately owned flavor retirement or
data-folder migration. It uses the existing app path functions unchanged.
