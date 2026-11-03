# Setup

The first run is setup v2. The flow is Notices, then Your AI, Sees, Runs on this Mac, Send and Reaches you, then the finish. Isoform design specifies it in three handoffs:

- `docs/handoff-2026-10-28/handoff-2026-10-28-1215-setup-v2.0.0.md`
- `docs/handoff-2026-10-28/handoff-2026-10-28-1315-setup-v2.1.0.md`, which adds "On this Mac"
- `docs/handoff-2026-10-29/handoff-2026-10-29-1523-setup-v2.2.0.md`, which adds "Runs on this Mac"

The v2.2.0 prototype in `docs/handoff-2026-10-29/setup-2.2/` is the reference for looks and timing.

## The shell: `../onboarding-screen.tsx`

The shell owns:

- the six progress dashes and Back
- the "On this Mac" pill (`v2/OnThisMac.tsx`)
- the saved screen
- Escape
- the handoff to Chat

The saved screen is kept in `yansu.onboarding.step`. It is read before it is written, so the reopen that macOS forces after Screen Recording comes back to the same screen. `resumeScreen` maps the steps the first setup saved to v2 screens.

Escape leaves setup for this session. While the popover is open, Escape only closes it: the popover listens in the capture phase and marks the event handled.

## Data-driven by design

A new handoff should only change data, not scene code:

- **Order:** `SETUP_V2_SCREENS` in `v2/types.ts`.
- **Timings:** `v2/timings.ts`. Every wait and tween is there, and the scenes only read it.
- **Copy:** the `onboarding` namespace's `v2.*` keys, in en and zh-CN.
- **Privacy lines:** the four "On this Mac" lines (`v2.local.lines.*`). Settings › Privacy reuses them through `OnThisMacLines`, so the two never drift apart.

## The scenes: `v2/scenes/`

The scenes animate imperatively, through refs and the `Run` class in `v2/motion.ts`. Under Reduce Motion each tween finishes at once.

Each scene reads real state:

- **Your AI:** the agent probe and the account order. Past the happy path, `assistant_setup.go` installs, re-scans with a fresh PATH and checks the sign-in. Before a card turns on, it also makes a one-token test call through the gateway. Each outcome is its own card state with its real next step (`CARD`, `CODE_STATE` and `CODE_NOTICE` in the scene), never a bare "Try again". Its log lines start with `[assistant-setup]`.
- **Sees:** the permission sequencer (`setup_permissions.go`, event `setup:permissions`). Turn on only asks; it downloads nothing.
- **Runs on this Mac:** the model choices and the planner (`PlanSetupModelDownload`, `v2/models.ts`), and the confirmed downloads (event `models:downloads`). Download starts the reviewed plan; Later confirms it for when the computer is plugged in and idle (`DeferModelDownloads`, `setup_models_deferred.go`).
- **Reaches you:** the mobile connection code, pending pairings and paired devices.

## Native parts: `setup_window*.go`, `setup_window_darwin.m`

Native code handles:

- the fixed 600 × 560 setup window
- the menu bar mark's first appearance
- the finish flight into the menu bar
- the main window's reveal

The frontend reaches them through `v2/native.ts`. Each call there resolves `null` when a binding is missing, so the browser harness and other platforms keep working.

## The gallery

`e2e/setup-v2.html?still=<state>&theme=light|dark&lang=en|zh-CN` renders one frozen state with fixture data and no backend writes. `docs/setup-v2-qa/` compares each state with the prototype.

## Settings › Storage › Models

`models-panel.tsx` and `setup.css` still draw Settings › Storage › Models, which kept the board styles from the first setup.
