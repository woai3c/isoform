# Isoform for iPhone

Your Mac already knows. Read it here.

[Download Isoform for iPhone on the App Store](https://apps.apple.com/app/isoform/id6827347350).

Isoform for iPhone is a companion to the Isoform app on your Mac. The phone has three tabs for the moments you need it:

- **Today**: the latest brief, today's highlights, and earlier briefs from the phone's cache. Library remains reachable from Today. The phone can read cached briefs offline.
- **Chat**: the main chat itself, showing saved messages immediately and refreshing from your Mac. The phone lists no other chats and starts none; they stay on your Mac. Send a message, answer a permission request, or stop a running turn. Text you have not sent is saved on this phone across tab switches, backgrounding and relaunches. Each chat keeps only its last 50 items on this iPhone.
- **Tasks**: work that needs you, work in progress, recent completed work, and the **Up next** offer deck. Review opens Chat with an unsent draft. Start opens Chat with the existing offer handoff; Later records `not_now` through the write journal.
- **Topic** (RC 1, `TopicView.swift`, `TopicStore.swift`): the main chat filtered to one topic, paged from the newest (`GET /topics/{id}`, needs `chat:read`; without it the screen says this phone is not allowed). Its box sends to the main chat with the topic attached (`POST /topics/{id}/messages`) through the write journal, so a retried message keeps its key and is sent once, with no owner check. A card with a prepared reply says to read the draft in Isoform on the computer and offers only its own **Not now** (the chat action route, no owner check); no phone action sends the reply yet. Each card has **Like** and **Not right** (`POST /briefs/{briefId}/items/{itemId}/feedback`); Not right on a card that rests on one memory asks for a reason and the owner check, and removes that memory on the Mac.
- **Ideas**: cards Isoform proposes from work it observed and topic finds on your computer, each with a short reason. **Do it** opens the main chat with the idea in the composer, unsent; existing drafts stay there. **Not for me** asks for Face ID, Touch ID or the passcode, then dismisses the idea through the shared write journal. An unanswered dismissal keeps its key for the next refresh. Pull to refresh reads `GET /offers`; cached ideas stay readable on this phone while your computer is offline. The computer rechecks its read permissions before sharing ideas and again before admitting an idea-derived chat message. Its source reference stays with edits, Undo, saved drafts and offline messages. Pairing with another computer clears the cache (PROTOCOL.md section 20).
- **Goals**: what you are tracking, from the Purpose Map on your Mac (`GET /goals`, PROTOCOL.md section 20.5): each goal with a round checkbox, its title, the Mac's two-to-three-line status sentence (what is due or waiting, with the date or person) and a menu (Mark done, Waiting on someone, Reopen), five at a time with **Show N more**, then the done ones. **Create a goal** lists categories (Health, Relationships, Finance, Career, Interests, Productivity, Something else); one opens a sheet, and **Let's do it** opens the main chat with "I want to create a goal about <category>" in the composer, unsent. The chat refines the goal and the Mac adds it with its `GoalCreate` tool. A check-off, reopen or wait asks for Face ID, Touch ID or the passcode, then goes through the write journal; one your Mac does not answer stays on this iPhone and is sent again on the next refresh. While your Mac is offline the list is read from this iPhone and nothing can change. A goal that repeats one of your mail subjects stays on your Mac.

Every tab has a top-right profile button that opens Settings and a floating Today, Chat, Tasks tab bar. Settings is a sheet with You, Your Mac, Notifications, Brief and Privacy groups. Pairing remains available there; brief scheduling, voice and quiet hours are read-only on the phone. The app can lock on launch and foreground with the iPhone's device-owner authentication.

**Library** groups past briefs by their computer-local day, with the first summary paragraph from each cached brief. An uncached brief opens through the existing brief read route; its summary appears after it is saved. Deliverables are read-only records from those briefs. Observed documents are excluded, and files open on the computer; there is no file download or media sharing route in this release. **History** shows the latest one-time work updates from cached briefs, plus answered and expired requests from the newest 50-item main-chat page. “Answered” does not imply approved or declined. Pending requests stay in Chat. Decision reads need `chat:read`, remain in memory only while the sidebar is open, and clear on a pairing change; brief-derived records follow the existing cache and `brief:read` scope. No new protocol routes or scopes are added.

This iPhone has no model of its own: chat and every write run on your Mac under the Mac's rules. A notice can also be put off from its notification (Later). The Mac is the only source of truth. The phone reads through an end-to-end-encrypted relay, which stores only ciphertext, and keeps briefs for 14 days so they can be read offline.

The Desk tab (decisions, initiatives, priorities and self-update reviews), its `needs_you` events and pushes, the `decision_due` notice with its Approve and Decline buttons, and the Live Activity were removed on 2026-10-13, when the Mac stopped serving them. A pairing that still holds the retired `desk:read` and `decide` scopes keeps working; the phone no longer asks for them.

Requirements: Xcode 16 or later, iOS 17.0 or later, iPhone only.

## Talk about a brief

Open a full brief from Feed, including an earlier day's brief, then choose **Talk about this brief** at its end. Chat opens with an editable opener naming that brief; nothing sends until you send it. The chips below the button send the brief's concrete offers to the main chat while preserving any unrelated unsent draft.

The phone sends only the edited text, brief ID, computer-issued revision and optional offer ID to `POST /chats/{id}/brief-messages`. The computer uses the same context construction and final privacy checks as Today, with phone permissions. A changed or withdrawn brief returns **This brief changed. Refresh and try again.** The draft keeps its revision through edits, Undo, relaunch and retries. Discard the old draft, refresh the brief, and open a new draft to use the updated revision. Pull to refresh a historical brief reads its current copy from the computer and preserves the cached copy if the refresh fails.

`BriefConversationTests` covers localized current/historical openers, persisted draft bindings, stale replies, offer isolation, incompatible drafts and pairing changes. `HubRequestsTests` covers the encrypted route and refusals without an ordinary-message fallback. `FeedUITests.testBriefConversationOpensEditableMainChatAndSendsOffersInBothAppearances` exercises the native flow in English light/dark and Chinese. Older computers without the additive route refuse the send; cryptographic vectors are unchanged.

## Pair with your Mac

1. On the Mac, open Isoform, go to **Settings > Phone**, and choose **Turn on** if the connection is off. Then, under **Pair an iPhone**, choose **Show pairing code**.
2. On the iPhone, open **Settings** and tap **Pair with your Mac**.
3. Scan the code, or paste the `isoform://pair#` link shown on the Mac. Legacy `yansu://pair#` links are also accepted.
4. Compare the six-digit code on the iPhone with the one on the Mac.
5. Approve the pairing on the Mac.

If this iPhone is already paired, choose **Pair again** in its **Settings** to scan or paste a new code. The current pairing keeps working until the new approval is saved. Cancelling or failing an attempt keeps the current pairing; there is no need to unpair first. After saving the new pairing, the iPhone registers its notification token again and asks the relay to revoke the old device. A failed token upload retries with backoff while alerts are on; the old device is kept until the new registration succeeds, you turn alerts off, or a 60-second grace period from the saved pairing expires. That deadline survives a relaunch, so a missing APNs token or the relay's device cap cannot leave the old device paired indefinitely. Pending revocations are kept in Keychain and retried after registration and on launch. If cleanup still cannot reach the relay, you can remove the old iPhone under **Settings > Phone** on the Mac.

After a restore to another iPhone, the saved pairing record may remain while its device-only Keychain credentials are missing. **Pair again** still works. If the old bearer is missing, Isoform skips its automatic revocation and shows a reminder to remove the old iPhone under **Settings > Phone** on the Mac.

Opening another pairing link while a claim is waiting leaves that claim in place. Dismissing it stops further claim requests and prevents it from replacing the current pairing. If a claim was already sent, the running app keeps checking until the code ends and queues any late approval for revocation instead of storing it. If the app closes or the relay cannot be reached during cleanup, remove the unused iPhone under **Settings > Phone** on the Mac.

A code expires after 10 minutes. If it expires, or you decline it, choose **Create new code** on the Mac and start again. Pairing ends when the Mac approves or declines, or when the code expires.

Pairing with a different Mac clears this iPhone's brief and chat caches, notices, notification settings and widget snapshot. Pending write keys belong to their original Mac and cannot be reused on another Mac. A same-Mac re-pair keeps its cache. Caches from older builds without a saved hub identity are cleared once when this build first opens.

If the saved pairing file exists but cannot be read, Isoform keeps its keys and cached data. Settings shows **Saved pairing unavailable** and offers **Retry saved pairing**. Unlock the iPhone or update Isoform before retrying; an unreadable file is never treated as a missing pairing. If it cannot be recovered, choose **Remove saved pairing** and confirm to delete the saved pairing and cached briefs and chats from this iPhone. Then use a new pairing code from the Mac. The old iPhone may still be listed under **Settings > Phone** on the Mac and can be removed there.

The approval is sent once. If the answer from the Mac is lost on the way, the iPhone says so, and you create a new code on the Mac and pair again.

## Notifications and the relay

- The relay address must start with `https://`. Development builds also accept `http://127.0.0.1` and `http://localhost`.
- **Notifications** is off until you turn it on and iOS supplies a push token. Turning it on asks iOS for permission. If permission or push registration is refused, the switch stays off with a reason beneath it, including after a relaunch. Isoform saves only a known failure code alongside the opt-in setting; tokens and raw errors are never saved. A launch re-checks existing iOS permission without prompting. If permission was removed, notifications turn off with a reason. Relay failures stay visible during a launch retry until registration succeeds; a successful registration or an explicit turn-off clears the saved reason. The alert the relay sends names no content.
- After the first brief arrives on a paired iPhone, Isoform shows a short explanation and then asks iOS for notification permission. Settings has a **Send a test notification** button that schedules a local notice in the moment category, so the banner and its buttons can be checked without APNs.
- A sealed notice (PROTOCOL.md section 14) carries its text inside `yansu.ct`. The notification service extension `YansuNotificationService` opens it with a notice key kept in the shared Keychain access group, then shows the sealed title and body. If the key is missing, the notice cannot be opened, or a check fails, the extension leaves the fixed fallback text the relay sent.
- Categories (registered at launch): `YANSU_MOMENT` has Open and Later. `YANSU_BRIEF` has Open. Later reschedules the same content one hour later and sends `acted` with the `later` action. A notice in the retired `YANSU_DECISION` category does not open, so it keeps the relay's fallback text.
- Opening a notice: `work_blocked` and `work_finished` open the main Chat tab (the notice names the work, not a chat). This does not open the work's execution chat. Every other kind, including one this build does not know, opens the Brief. A tap on a legacy brief push opens the Brief, and so does a legacy `needs_you` push from an older Mac.
- Receipts (`POST /v1/notices/{noticeId}/receipts`): `delivered` from the extension, then `opened`, `dismissed` and `acted` from the app. Receipts are best effort and retried once in the app.
- If the relay has no notification setup for this build, Settings reports that registration is unavailable and retries while alerts remain on. Turning alerts off cancels retries and removes the token after any upload finishes. A Mac can have alerts on for at most 10 iPhones.

## Chat, corrections and one-time work

These features need scopes granted at pairing: `chat:read` and `chat:write` for Chat, `memory:correct` for "Not right" on a memory, `handoff:create` for one-time work, and `brief:feedback` for Like and Not right in the Feed. The phone also asks for the other release 4 scopes (`offers:read`, `offers:decide`, `goals:read`, `goals:write`, `library:read`, PROTOCOL.md section 20.1) at the same pairing, so Ideas, Goals and Library need no further re-pair when their routes arrive. A pairing made before these scopes existed shows a note in Settings to pair again, and the features stay hidden until it does.

- **Chat** calls the hub's chat routes over the same encrypted envelopes as the brief: `GET /chats?limit=20`, `POST /chats`, `GET /chats/{id}?after=<cursor>&limit=50`, `POST /chats/{id}/messages`, `POST /chats/{id}/permissions/{requestId}`, and `POST /chats/{id}/cancel` (PROTOCOL.md section 15.3). A chat opens on the Mac's newest page. Later reads start from the `cursor` the Mac returns, not from the last item's seq, because the cursor also covers rows the Mac does not send. While `hasMore` is true, a read follows the cursor for up to ten pages; the rest comes on the next event. There is no backwards read, so a long chat shows its newest page. A pending permission is transient: it arrives on the newest page only, is never stored or cached, and goes away when a read no longer has it. Turn events (`{"event":"chat_turn", ...}`) arrive on the same poll and refresh the open chat; after a reconnect the chat is read again.
- The chat opens at its newest message, from the cached rows and again after the fresh read, and follows new rows while you are at the bottom. Scrolled further up, it leaves you there and shows **Latest** (or **New messages** once newer rows arrive), which jumps back down. Coming back to the chat, from another tab or the list, starts at the newest message again (`ChatScrollPolicy`).
- A message sent from the iPhone leaves the composer at once and shows a small status under it (`ChatDelivery`): **Sending** while it is queued or in flight, including while the Mac is offline (with the offline note); **Sent** once the Mac answers 202 with its `turnId` and the `seq` of the stored item (PROTOCOL.md 15.5); **Seen** once the Mac took it as a turn and started answering (a `chat_turn` event with that `turnId`, or a reply, tool step, error, later message or permission request after its `seq`). Only the Mac's answer ties a message to a row; a row with the same text may have been typed on the Mac. A Mac from before the 202's `seq` is tied by text, only in a read that started after its 202, and a row older than the page is placed just before the page. The newest message keeps its status; older ones keep theirs until the Mac has seen them and their row is on screen or behind the loaded transcript.
- Each message is its own write with its own idempotency key; only a retry of the same message reuses its key. A chat's messages go one at a time, oldest first: a message without an answer, or one waiting for a running turn, holds the ones after it, and they go once a read (or, for a chat that is not open, the list) shows the chat idle. While a turn runs, a queued message whose text the chat already shows is asked about once under its key: the Mac answers with the stored 202 if it took it, or busy if not. The Mac's answer is saved with the message before the journal forgets the key, so a phone that cannot save it (locked) gets the same answer back later instead of starting a second turn. A message that cannot be saved before it goes is not sent: the chat says so and the composer keeps it. A message from the first build of this feature, keyed by its text alone, keeps that key on its retry. These records sit beside the chat's cache (`<chat>.outgoing.json`, which names its chat so a launch resumes waiting messages in every chat, not only the one that opens). First-build bare-array files are identified at launch from their matching cached chat, main-chat marker or saved journal target, then upgraded before retrying. A file that exists but cannot be read is never taken as empty or written over; one that reads but does not decode is set aside as `.damaged-<time>` and the chat starts afresh.
- A sent message stays drawn as its own bubble under the rows until a read brings its row or shows it was seen, including when its row is older than the newest 50-row page. A turn event keeps a bubble whose row is still ahead of the loaded transcript, so it does not vanish when the read fails, across a relaunch, or after switching chats and back to older cached rows.
- A message sent while Isoform is answering waits on screen as Sending and goes when the turn ends. A message over 16 KiB is refused before it is sent. A message the Mac refuses for good (400, 404, a changed chat) stays in its outgoing file as an unsent draft before its retry key is forgotten. The chat list shows these drafts even if the Mac deleted their chat. They survive relaunch until a visible, active composer takes them, in send order; a Goals hand-off appends to the same list instead of replacing refused text. Once a send is saved in the queue, every refusal uses this recovery path, including a refusal of the first attempt.
- The phone has only the main chat's composer, so text that comes back in another chat, such as a message an older build queued there that the computer refuses, comes into the main chat's composer under a line naming that chat. Pruning keeps the journal key of every message still waiting past the usual day, so a retry gets the computer's first answer instead of a second turn.
- Answering a permission that the Mac has moved on from gets a calm notice and a re-read of the chat: 400 means the option is no longer offered, 404 means the request is not waiting, and 409 means it was already answered. Stop reports `cancelled: false` when nothing was running, and the chat shows nothing then.
- Answering a permission request asks for Face ID, Touch ID or your passcode first, the same as "Not right" and one-time work. Stopping a turn does not.
- Proactive messages show the Mac's headline, body, source references, offered actions and status in a chat card. Routine status messages use one expandable line. The card uses the shared light/dark colors and button styles, with VoiceOver labels and a vertical action layout for accessibility text sizes. A card action asks for the owner check, then calls `POST /chats/{id}/proactive/actions` through the write journal. A refusal keeps the card and shows the reason inline; an unanswered request keeps its key for retry. The retired `/decision` endpoint is not used. Open brief switches to Feed, and Review on a work card brings its result into the main chat. Other destinations show a note to continue on the Mac; the Chat tab remains the main chat only.
- Text typed in the composer but never sent is saved per chat in the protected outgoing cache envelope. It is restored when the chat opens after a relaunch and cleared when the outgoing message is saved. The existing refused-message recovery transfers its text into the persisted composer. Pairing with another Mac or removing the pairing clears composer text with the other chat data.
- Every write (send, create, answer, stop, correction, one-time work) reserves an idempotency key in `desk-writes.json` before it is sent, reuses it on a retry, and forgets it once the Mac answers. The journal keeps no message text: it stores a SHA-256 fingerprint of the content. The file keeps the name it had while the Desk existed. Each new entry stores its hub ID; older entries without a hub ID are adopted by the current hub once, preserving their original keys for retries after an upgrade. The adoption is persisted before another write is sent. At launch, `ActionStore.pruneJournal` drops entries older than a day and every entry of a retired Desk write (decision, initiative command or feedback, self-update review, priority create, revise or dismiss); those operation names stay decodable so an older file is never rejected whole.
- **Not right** appears on a brief item that rests on exactly one memory. It sends a reason to `POST /memory/{id}/reject-attribution` after the owner check. An item that rests on several memories has no button in this release.
- **Retract** (`POST /self/directives/{id}/retract`) has no client on the phone.
- **Give Isoform a task** is the compose control on the Feed, Ideas and Goals tabs when the pairing grants `handoff:create`. The sheet sends `POST /handoffs` with a title, what to do, and an optional folder on your Mac, after the owner check, because starting work is the approval for it. The folder must be empty or a full path starting with `/`. The Mac keeps the work waiting until it is started from One-time work on the Mac, so after it is saved the sheet closes and the tab shows a notice. Its Feed card shows the status and all supplied detail as plain text, with no chat link. The current brief builder supplies status sentences, not the execution result body. Routing actual work results and permission requests into the main chat is a later Mac-side change; this phone build has no needs-you Feed permission card and cannot answer a permission in the inaccessible execution chat.
- Chat keeps the last 50 items of each chat and a small main-chat identity marker in the App Group container, using complete file protection. Older snapshots are upgraded when read. There is no dedicated cache encryption key in the pairing vault; its keys are for transport. Permission requests are never restored from cache. Unpairing removes the whole directory.

## Hear your brief

Today and each full brief show **Hear your brief**, with play/pause, a seek bar, elapsed time and duration.
The computer produces the audio. The phone asks for that exact brief version through the authorized,
encrypted bridge (`GET /briefs/{briefId}/audio?version=...`, PROTOCOL.md section 17.4), then downloads
and decrypts its sealed attachment when you choose play. The phone never synthesizes speech or uses
the computer's podcast-feed address.

Playback continues when you leave Today or lock the phone. Now Playing uses **Your brief** as the title
and **Isoform** as the artist, with play, pause and seeking. Interruptions pause playback; removing
headphones pauses it without switching the spoken brief to the speaker.

The app keeps one recording for offline listening, with a 16 MiB audio cap and bounded resume records
keyed by computer and brief version. Files are protected after the phone's first unlock and excluded
from backups. **Delete cached briefs**, unpairing, and changing computers clear audio too. A new version
starts from the beginning. If audio is unavailable, the row explains whether the computer is offline,
still rendering audio, or missing its voice. Tap again to retry once it is ready.

When a received brief withdraws its audio and marks synthesis unavailable, playback stops immediately,
including in the background. Its cached recording is invalidated across reopening and app relaunch;
an older readable view cannot restore it. Offline listening remains available until such a withdrawal
is received. A fresh authorized audio response is required to download an invalidated recording again.
Refetching the same encrypted withdrawal from latest or history preserves audio authorized afterwards,
also after reopening. A newer withdrawal still revokes it. The phone keeps bounded, pairing-scoped
receipt watermarks with the cache so interrupted writes and a re-pair cannot restore withdrawn audio.

Older computer recordings that have a sealed attachment still work. A local-only recording made before
the computer stored its audio digest needs a newly generated brief before it can be uploaded safely.

`BriefAudioTests` covers cache bounds, version and computer isolation, resume, asynchronous replacement,
playback state, interruptions and unavailable reasons through an injected playback engine.
`FeedUITests` adds the `feed-audio` and `feed-audio-rendering` seeded states, using a generated 30-second
WAV and capturing light/dark screenshots. The UI tests require the app runner; host-only tests do not
verify audio-session behavior on a locked phone.

## Widget

- **Widget** (`YansuWidget`, "Brief", small and medium): the Mac's name and the time of the last brief. It reads `widget-snapshot.json` (version 2) in the App Group container, which the app writes whenever the latest brief changes. A version 1 file from the Desk build is ignored until the next brief is written. The widget opens `yansu://brief` and has no networking. The shared code it compiles is `AppGroup.swift` and `WidgetSnapshot.swift`; every other `YansuShared` file is excluded from its target by a membership exception in the project. A new `YansuShared` file must be added to that exception list unless the widget may compile it.
- The hub does not send the next brief time, so the widget shows when the last brief arrived: the time alone for a brief from today, otherwise its date and time (`WidgetSnapshot.briefTimeText`, the same rule as the Brief's "As of" line). The timeline adds an entry at midnight so a brief turns into yesterday's on time.
- Deep links: `yansu://brief` opens the Brief, `yansu://chat` opens Chat, and `isoform://pair#...` starts pairing. Legacy `yansu://pair#...` links are also accepted. `yansu://desk`, which older widgets carry, opens the Brief.

## Run in the Simulator

The Simulator has no camera, so paste the `isoform://pair#` link into the field under **Pair with your Mac** instead of scanning.

Build from the repository root:

```bash
./scripts/build-ios.sh
```

This compiles Debug for the iOS Simulator into `ios/.build` and prints the path of `Yansu.app`. Set `YANSU_IOS_CONFIGURATION=Release` for a Release build.

To run from Xcode, open `ios/Yansu.xcodeproj`, select the `Yansu` scheme, and choose an iPhone simulator. Xcode can default to the newest installed runtime, which may not contain the device you picked. Pin the runtime in the destination, for example `platform=iOS Simulator,name=iPhone 15,OS=17.4`.

## Tests

```bash
./scripts/test-ios-compatibility.sh
```

This runs the unit tests (`YansuTests`) by simulator UDID: iPhone 15 on iOS 17.4 (`EF8428A8-56F2-4A83-8434-FC5051039CAF`) and iPhone SE (3rd generation) on iOS 17.2 (`20F6D41D-37B4-4D28-83EB-BBF1E556A58A`). It checks both IDs before building and fails if either run fails. On another computer, set `YANSU_IOS_IPHONE_15_ID` and `YANSU_IOS_IPHONE_SE_ID` to the intended test devices. Result bundles go to `ios/.build-compatibility/results/`. `./scripts/test-ios-compatibility.sh --self-test` checks destination selection and failure handling without building or using a simulator.

To run the unit tests and the UI smoke test in one run:

```bash
xcodebuild test -scheme Yansu -destination 'platform=iOS Simulator,name=iPhone 15,OS=17.4'
```

The UI smoke tests launch the app, check the four tabs (Chat, Feed, Ideas, Goals), open the sidebar from every tab, and check Library, History, Settings and pairing. The isolated `library` state exercises brief summaries, read-only work and decision records, and navigation back to Chat. The Library fixture uses `YANSU_UITEST_APPEARANCE=Light` or `Dark` for appearance captures in Debug simulator builds. Light and dark sidebar and Library screenshots are attached to the result bundle, alongside the existing Chat and Settings captures. `FeedUITests` runs the Feed, Ideas and Goals over seeded cached briefs (`YANSU_UITEST_SETTINGS_STATE=feed` or `feed-empty`, with `YANSU_UITEST_TAB=feed` to open on the Feed), with no relay. `GoalsUITests` runs Goals over seeded cached goals (`goals` or `goals-empty`, with `YANSU_UITEST_TAB=goals`), including the hand-off from Create a goal to the main chat's composer; `YANSU_UITEST_OWNER_CHECK=allow` or `decline` answers the owner check without the system prompt. `ChatScrollUITests` opens the main chat over a seeded cached transcript (`YANSU_UITEST_SETTINGS_STATE=chat` ends on a Seen message, `chat-sent` on a Sent one) and checks that it opens at the newest message, leaves you where you scrolled, offers Latest, and returns to the newest message; the seeded pairing has real session keys for a relay at an `.invalid` address, so a message typed there stays Sending with the offline note. `MainChatUITests` checks that the Chat tab is the main chat, with no list behind it and no new-chat control, across tab switches and trips to the background, and that unsent text stays in its composer. It runs over `chat`, over `chat-live`, which keeps the cached main chat and answers the chat routes in-process (`ChatUITestHub`, no relay), and over `chat-live-cold`, which starts with nothing cached so the main chat is found in the Mac's list.

Verified SE captures: [sidebar light](../docs/screenshots/iphone-library/sidebar-light.png), [sidebar dark](../docs/screenshots/iphone-library/sidebar-dark.png), [Library light](../docs/screenshots/iphone-library/library-light.png), [Library dark](../docs/screenshots/iphone-library/library-dark.png).

`AgentPushTests` covers persisted notification failures, permission checks on launch, stale permission results after a toggle or pairing change, relay cleanup without an in-memory token, failed cleanup, and English/Chinese failure copy. `SmokeUITests` keeps one isolated fixture across process termination and relaunch, then checks that Notifications stays off with its reason beneath the switch. iPhone 15 captures: [registration failure after relaunch](../docs/screenshots/iphone-settings/registration-failure-after-relaunch.png), [permission denied after relaunch](../docs/screenshots/iphone-settings/permission-denied-after-relaunch.png). Failed relay token deletion remains best effort and has no durable retry; relaunch still preserves opt-out and the permission reason.

`RelayCryptoVectorTests` checks the crypto against `mobile-protocol/vectors-v1.json`, which the Go test in `wails-gui` generates. It skips until that file exists.

`ChatModelsTests` covers proactive/status decoding, allowed action presentation, lifecycle states and cache round trips. `ChatStoreTests` covers per-chat draft restoration in a new store and cache instance, clearing after send, refusal recovery and same-sequence action updates. `ActionStoreTests` covers owner checks, refusal and retry keys. `ChatScrollUITests` adds `chat-proactive` and `chat-status` seeded states, action success/refusal/owner-check cases and light/dark screenshots. These are canned presentation tests, not live Mac action proof. Set `TEST_RUNNER_YANSU_UITEST_SCREENSHOT_DIRECTORY` to the absolute `ios/.build/screenshots` directory when running them to export their PNGs as well as attach them to the result bundle.

### Ideas validation (2026-10-17)

`IdeasStoreTests` lives in the existing `FeedStoreTests.swift`: 24 tests cover cache isolation, owner checks, permission changes, decision retries, pairing and refresh races, and recovery after a protected cache becomes readable. All 24 passed in a temporary macOS host-only package using the production store and journal. The app, unit-test and UI-test sources also passed iOS SDK typechecking without code generation. These checks do not prove rendering or replace simulator runs.

Two hand-off tests in `ChatStoreTests.swift` check the main-chat draft, preservation of earlier text, relaunch and pairing changes. Four new `FeedUITests` cases use `YANSU_UITEST_SETTINGS_STATE=ideas` or `ideas-empty` and `YANSU_UITEST_TAB=ideas`; they cover light/dark cards and an unsent hand-off, a cancelled owner check, dismissal plus refresh, and empty state. The light/dark case attaches `ideas-light` and `ideas-dark` screenshots.

This worker ran **0 iPhone 15 unit tests, 0 SE unit tests and 0 UI tests**, because all app builds and launches are reserved for the app runner. No screenshots were produced. The merge desk must run the unit and UI suites only on its assigned SE simulator, export attachments to `ios/.build/screenshots`, and record counts before the Ideas PR merges. Broader device coverage remains with the app runner. `project.pbxproj` is unchanged.

### Ideas review round: source-bound drafts (2026-10-17)

Ideas now carries the computer's offer ID and version through the main-chat composer, edits, clear/Undo, returned drafts, relaunch and offline delivery. A pending handoff is saved even before the main chat is discovered. Submitting the composer atomically saves the queued text and references and clears the draft; Send and Discard reset the text field and Undo history. The computer re-resolves current sources and filters the complete edited text before storing or dispatching it. Source-linked sends use `/chats/{id}/offer-messages`, so an older computer rejects the route instead of ignoring the provenance field.

Target-format saved composer text and recovery IDs migrate into the new envelope without losing the draft. Text whose source association was never recorded stays blocked until explicitly discarded. Failed handoff writes retain the pending copy; recovering refused text atomically removes its sendable queue entry.

Old unversioned Ideas cards require refresh before Do it is available. Legacy unsent chat records without a provenance field are kept as blocked drafts: they cannot be distinguished safely from earlier idea prefills. Discard those drafts and choose a fresh idea. New ordinary messages store an explicit empty reference list.

The second review round also applies current read gates to accepted source-derived history. The computer stores the source graph independently of a waiting offer, fences activity and file/policy changes through publication, and rechecks stored provenance for model history, phone chat reads and dispatch. Source-bound conversations require an available native model provider so every request, retry and compaction can enforce the current read gate; they do not fall back to external assistant subscriptions. A withdrawn source hides its row and the dependent conversation suffix, including replies and summaries. Continuation fails closed when a provider may retain withdrawn context; an automatic context-reset flow is not included. Existing accepted rows that contain references without a source graph also fail closed. Already delivered offline phone caches and provider context cannot be remotely erased by these server gates.

Ideas actions, notices, chrome, explanations and accessibility labels now support English and Chinese using the native presentation-language helper. The seeded Chinese UI case checks the translated explanation and accessibility labels. A native Undo UI case covers clear, Send and Discard. Both are authored and statically checked; simulator execution remains with the app runner. Source titles and prompts retain their original language.

Rollback: revert the integrated merge with `git revert -m 1 <merge-commit>`. There is no server schema migration to undo. Before installing an older phone build that ignores source references, use **Unpair this phone** in the current build and ensure its local chat/Ideas caches, drafts and outgoing queue have been removed; do not restore those unsent files into the older build. This discards unsent work, so keep the upgraded build until it has been reviewed or safely discarded. A server-only rollback to before Ideas rejects the upgraded phone's source-linked route with 404 and never falls back to ordinary chat. Reverting only the second review fix removes the read gates on previously admitted source-derived rows; keep the fix while those rows remain usable history.

Live pairing has no automated test in this release. `scripts/test-ios-pairing.sh` lists what a future relay-backed test needs.

### PR #14 round 3 (2026-10-16)

Starting from `26b0065`, the composer now has one store-backed binding and a pairing-guarded send/restore operation. Immediate oversized refusals cannot be overwritten by a pending empty-value observer, and initial refusals return before newer typing. Chat tab copy is grouped in `ChatCopy` and says "computer".

Launch recovery reads queued chats independently of the visible main chat, retains their original journal keys through pruning and busy/offline retries, and saves permanently refused text as a main-chat draft with its original chat named. Atomic recovery receipts prevent interrupted cleanup from sending or restoring the same text again. Files: `ChatView.swift`, `ChatTabView.swift`, `ChatStore.swift`, `ChatCache.swift`, `JournaledWriter.swift`, `ActionStore.swift`, and `AppEnvironment.swift`.

One-time-work Feed cards strip links and show status plus untruncated supplied detail as plain text (`FeedAssembly.swift`, `FeedCard.swift`). The task composer points to Feed for status. **Remaining source gap:** `collectMobileBriefWork` currently supplies generic status sentences, not execution result bodies. The phone has no needs-you Feed permission card; its current chat permission card only handles the open chat. Result/permission delivery from the work's execution chat therefore remains unimplemented, rather than being silently routed to the main chat in this round.

Added 20 unit tests in existing files: 5 composer/copy tests in `ChatStoreTests.swift`, 13 queue recovery tests in `ChatDeliveryTests.swift`, and 2 Feed tests in `FeedAssemblyTests.swift`. Updated the existing unpaired Chat smoke test for the new copy. No `project.pbxproj` changes.

Verification respects this round's final instruction reserving all app builds and launches for the app runner. `YansuTests` has `TEST_HOST` set to `Yansu.app`, so even `test-without-building` launches the app; none of the requested simulator suites were run here.

| Check | Executed | Result |
| --- | ---: | --- |
| Host-only Feed model tests, temporary macOS Swift package | 13 | Passed, 0 failures; exact production models and extracted dependencies, no app or Simulator |
| YansuTests, iPhone 15 `EF8428A8-56F2-4A83-8434-FC5051039CAF` | 0 | Pending app runner |
| YansuTests, SE `20F6D41D-37B4-4D28-83EB-BBF1E556A58A` | 0 | Pending app runner |
| MainChatUITests, GoalsUITests, ChatScrollUITests | 0 | Prohibited by this round's iOS rules; pending app runner |

Syntax parsing, source typechecking without code generation, `git diff --check`, and the configured pre-commit hooks are the local checks. Host-only tests do not prove SwiftUI rendering or replace the requested simulator runs. PR #13 overlaps queue recovery, durable drafts and send result types; preserve this round's single composer value and prepend ordering when integrating it.

### Chat startup diagnostics

Debug builds log timestamped startup phases under subsystem `com.yansu.companion`, category `FirstLoad`: app launch, cached main-chat selection, transcript readiness, view appearance/update, and relay send/poll/ACK durations. They contain no message text, identifiers, or credentials. Compare `app.launch` with `chat.view.cached` or `chat.view.updated` while using the app; XCTest measures the store's render-ready state rather than a displayed frame.

`ChatStoreTests` exercises the real encrypted `HubChannel` against a delayed fake relay. It retains an explicit replay of the old serial presence/list/ACK/presence/detail/ACK sequence for comparison, asserts zero relay requests before cached rendering, and enforces budgets of one second for the cached transcript and three seconds for fresh messages. It also holds independent list, brief, presence and ACK operations for four seconds. `HubRequestsTests` covers immediate first send, shared polling and background ACK cleanup across pairing changes, replies to cancelled requests (dropped, never parked in place of a waiting one), and envelopes the vault could not save (never acknowledged, so the relay keeps them). File-protection attributes require hardware validation; Simulator tests cover persistence, isolation and permission exclusion.

A phone with no saved main-chat identity still discovers it through `GET /chats?limit=20`, followed by `GET /chats/{id}?limit=50`. The existing detail route already returns the newest page when `after` is absent. A future `GET /chats/main?limit=50` route returning the same `ChatDetail` envelope could remove discovery on a completely uncached install. It must resolve the hub's actual main chat, enforce `chat:read`, and take precedence over the generic chat-ID route. No hub route change is needed for cached startup.

That optional route would return HTTP 200 with `{"ok":true,"data":{"chat":{"id":"actual-main-id","title":"Isoform","state":"idle","kind":"main","updatedAt":"2026-10-16T17:00:00Z"},"items":[{"seq":42,"role":"assistant","kind":"text","text":"Example reply","toolName":"","at":"2026-10-16T17:00:00Z"}],"cursor":42,"hasMore":false}}`, using the existing bounded newest-page and transient-permission rules. Return 403 without `chat:read` and 404 when no main chat exists; reading must not create a chat. This is a proposed contract, not an implemented route.

## Install on a paired iPhone

```bash
export YANSU_IOS_TEAM_ID='YOURTEAMID'
./scripts/install-ios-wireless.sh check 'YOUR-IPHONE-UDID' --personal-team
./scripts/install-ios-wireless.sh install 'YOUR-IPHONE-UDID' --personal-team
```

The iPhone must be paired and trusted in Xcode Device Hub, have Developer Mode on, and be connected over the local network. `check` reads the device inventory and signs nothing. Use `--personal-team` only with a free Personal Team, whose builds have no remote push. Omit it for an enrolled team.

`scripts/distribute-ios.sh verify` compiles Release for the device SDK without signing. The signed actions (`archive`, `export`, and `upload`) require `YANSU_IOS_TEAM_ID` and `YANSU_IOS_EXPORT_METHOD=app-store-connect`. `YANSU_IOS_BUILD_NUMBER` sets the build number for `archive`, and existing outputs are never overwritten. `export` writes a local package. Only `upload` sends the build to App Store Connect.

## Shared access and the notification extension

The app, `YansuNotificationService` and `YansuWidget` share the App Group `group.com.isoform.companion` (brief cache, chat cache, widget snapshot) and the Keychain access group `$(AppIdentifierPrefix)com.isoform.companion.shared` (device bearer, notice key and relay context). Both targets carry the entitlements; Info.plist's `YansuKeychainAccessGroup` gives the expanded group name at run time. Keychain items that the extension reads use `AfterFirstUnlockThisDeviceOnly`, because pushes arrive while the phone is locked. The device private key stays `WhenUnlockedThisDeviceOnly`.

The Simulator build is ad-hoc signed, and this Xcode drops the entitlements from it. The shared access group still worked in the Simulator test run, but the notification extension has not yet been seen opening a sealed notice in the Simulator. Device runs need the owner's Apple team for the App Group and the shared access group.

## Not provisioned in this repository

- The Apple Developer team, signing identity, and provisioning profiles.
- The APNs authentication key and the relay's APNs configuration. Notification delivery needs both.
- The App Group `group.com.isoform.companion` and the shared Keychain access group on the Apple team, and the bundle id `com.isoform.companion.notificationservice` for the extension and `com.isoform.companion.widget` for the widget. The widget's App Group entitlement is unconfirmed in the ad-hoc Simulator build, which strips entitlements; confirm the widget on a team-signed build.
- The App Store Connect app record for `com.isoform.companion`.
- The export-compliance answer (`ITSAppUsesNonExemptEncryption` in `Info.plist`). Answer it again before any submission.
- The app icon. `AppIconOpaque.png` is a blue mark, not the Isoform icon, and must be replaced before release.

## Code layout

App code lives in `ios/Yansu`. Tests live in `ios/YansuTests`, and the UI smoke test lives in `ios/YansuUITests`.

| File | Responsibility |
| --- | --- |
| `YansuApp.swift`, `RootView.swift`, `TabRootBar.swift` | App entry point, the four tabs, the sidebar and Settings sheets, and the shared top bar |
| `AppEnvironment.swift` | Creates the stores and sets the launch order |
| `AgentPushAppDelegate.swift` | APNs callbacks, notice actions and notification taps (name kept from the push-first build) |
| `NotificationCategories.swift` | Categories and buttons, registered at launch, and the app's local notices (Later and the Settings test) |
| `KeychainMigration.swift` | Moves Keychain items into the shared access group, once |
| `PushPermissionView.swift` | The explanation shown before the system permission prompt |
| `YansuNotificationService/NotificationService.swift` | Notification Service Extension: opens sealed notices and posts `delivered` |
| `YansuShared/` | Code compiled into both the app and the extension: `RelayCrypto.swift`, `RelayClient.swift` (relay HTTP API, each status code mapped to a typed error), `Keychain.swift` (shared access group), `SharedSecrets.swift` (notice key and relay context), `NoticeModels.swift`, `NoticeCrypto.swift`, `NoticeDelivery.swift`, `NoticeReceipts.swift`, `AppGroup.swift` |
| `HubRequests.swift` | Encrypted requests to the Mac, their replies, the shared long-poll, and the POST path every write takes |
| `PendingWriteJournal.swift` | Idempotency keys for writes not yet answered, kept on disk |
| `DecisionGuard.swift` | Owner authentication before a chat permission answer, "Not right", and one-time work |
| `ActionStore.swift` | "Not right" on a memory and one-time work: owner check, journaled write, notices, and pruning the journal at launch |
| `CorrectionModels.swift`, `HubCorrectionRequests.swift` | Request bodies and routes for "Not right" and one-time work |
| `OneTimeWorkComposer.swift` | The Give Isoform a task sheet, opened from the compose control on Feed, Ideas and Goals |
| `JournaledWriter.swift` | The single path every journaled write takes: reserve the key, send, forget on settle |
| `ReasonSheet.swift` | The one-line reason sheet for "Not right" |
| `ChatModels.swift`, `HubChatRequests.swift` | Chat read models (decoded tolerantly), the chat routes and the `chat_turn` event |
| `ChatStore.swift`, `ChatCache.swift` | The main chat's identity from the Mac's chat list, unsent text per chat, open transcript, incremental reads, journaled sends, permission answers, stop, and the 50-item cache |
| `ChatDelivery.swift`, `ChatScrollPolicy.swift` | Sending, Sent and Seen for messages sent from this iPhone; when the open chat follows its newest message |
| `ChatTabView.swift`, `ChatView.swift` | The Chat tab (the main chat, or why it is not available yet), the transcript, the permission card and the composer |
| `WidgetSnapshotPublisher.swift` | Writes the widget snapshot from the latest brief |
| `YansuWidget/` | WidgetKit extension: the brief widget |
| `YansuShared/WidgetSnapshot.swift` | The widget snapshot, shared with the widget |
| `PairingInvitation.swift` | Parses and checks `isoform://pair#` and legacy `yansu://pair#` links |
| `PairingStore.swift` | Pairing state machine |
| `PairingVault.swift`, `Keychain.swift` | Paired record, sequence counters, and Keychain secrets |
| `PairingView.swift`, `QRScannerView.swift` | Pairing sheet, six-digit check, and camera scanner |
| `BriefModels.swift` | Brief types, decoded tolerantly |
| `BriefStore.swift`, `BriefCache.swift` | Latest brief, history, refresh, the read-only Library projection and decision reader, and the 14-day cache (in the App Group container) |
| `FeedView.swift`, `FeedCard.swift`, `FeedAssembly.swift` | The Feed tab, its cards and their art (`FeedArt-*` in the asset catalog), and the merge of cached briefs into days of cards |
| `BriefCardsView.swift` | The brief's paragraphs with their topics and questions, what needs attention, and the closing question with its answers and Talk |
| `TopicModels.swift`, `HubTopicRequests.swift`, `TopicStore.swift`, `TopicView.swift` | RC 1 topics: the `/topics` routes, Your topics, and the Topic screen with its journaled send (`TopicUITestSeed.swift` seeds `feed-topics` and `feed-topics-no-chat-read`) |
| `FeedStore.swift`, `HubFeedRequests.swift` | The interest sentence, Like and Not right through the write journal, and the feedback route |
| `IdeasView.swift` | Ideas cards, source projections, scoped offline cache, owner-checked decisions and unsent main-chat hand-off |
| `GoalsView.swift`, `GoalRow.swift` | The Goals tab with Create a goal and its sheet, and one goal's row with its menu |
| `GoalModels.swift`, `GoalStore.swift` | Goals as the Mac sends them and the create categories; the list kept for offline reading, and check-offs, reopenings and waits through the owner check and the write journal |
| `BriefView.swift`, `BriefContentView.swift` | Shared brief text helpers, links, and the brief layout with evidence sheets |
| `HistoryDetailView.swift` | Library and History lists, and one brief opened from Feed or Library |
| `SettingsView.swift` | Settings, opened from the sidebar |
| `PushRegistration.swift`, `NotificationRouter.swift` | Notifications and routing of taps |
| `LegacyPurge.swift` | Removes data left by earlier builds, once |
| `Theme.swift` | Colors, type, and shared components |

## Pairing links and the live pairing test

The app registers both `isoform` and `yansu` URL schemes, so an `isoform://pair#...` link opened on the phone (or in the Simulator with `xcrun simctl openurl booted "<link>"`) starts pairing directly; legacy `yansu://pair#...` links remain supported.

`YansuUITests/ChatFlowUITests` pairs with a live hub, opens the Chat tab, opens a chat (or starts one from the New menu), sends a message and waits for an assistant reply that arrives after that send. It is skipped without an invitation. The Go harness answers a chat turn with a canned reply, so the test runs against the harness's hold mode. A Simulator presents the owner check even without a passcode, so the test enrolls the Simulator's Face ID for its run (`YansuUITests/SimulatorBiometrics.swift`, the same notifications the Simulator app's Features menu sends), then un-enrolls it.

`YansuUITests/PairingFlowUITests` drives the whole flow against a live hub: it pairs with a pasted link, waits for the Mac's approval, and checks that the brief renders. It is skipped unless an invitation is supplied. The quickest live hub is the Go harness in `wails-gui`, which also approves the pairing and sends a sample brief:

```bash
cd wails-gui && YANSU_MOBILE_E2E=1 YANSU_MOBILE_E2E_HOLD=20m YANSU_MOBILE_E2E_INVITATION_FILE=/tmp/yansu-invitation.txt go test -run TestMobileEndToEndThroughRelayBinary -count=1 -v . -timeout 40m
```

```bash
TEST_RUNNER_YANSU_UITEST_INVITATION_FILE=/tmp/yansu-invitation.txt xcodebuild test -project ios/Yansu.xcodeproj -scheme Yansu -destination 'platform=iOS Simulator,name=iPhone 15,OS=17.4' -derivedDataPath ios/.build -only-testing:YansuUITests/PairingFlowUITests
```

Debug builds also prefill the paste field from `YANSU_UITEST_INVITATION` in the launch environment; release builds ignore it.
