# Automations Components

UI components for managing cron-based automations in the Yansu desktop (Wails) app.

## Overview

This directory provides the UI for viewing, editing, enabling/disabling, running, and deleting scheduled automation jobs. Automations run AI agent tasks on a cron schedule, with support for manual triggering and run history inspection.

Scheduled work lives in Tasks; selecting a schedule renders its detail through `AutomationDetailPane`. Creating a new automation happens through chat, not a dedicated form.

Tasks uses `GetUpcoming` (`wails-gui/upcoming.go`, loaded by `useUpcoming`) for schedules, briefs and Daily News. A schedule opens `AutomationDetailPane`; a brief opens Today.

## Files

| File | Purpose |
|---|---|
| `upcoming-text.ts` | Shared schedule names and timing labels for Tasks and Today |
| `AutomationDetailPane.tsx` | Single-job detail view for the right main content column: detail header (Run now / status toggle / delete / Save / Cancel) + `CronJobDetail` body. No job list — selection is driven by the sidebar Automations list. |
| `AutomationDetailDialog.tsx` | Modal dialog wrapper around `CronJobDetail` for inline automation inspection (used by the calendar/timeline page) |
| `CronJobDetail.tsx` | Detail body with status info, inline editing, run history, and expandable run logs |
| `schedule-utils.ts` | Pure helpers for converting between cron expressions and human-readable presets |

## Architecture

Components are purely presentational — all data fetching and mutation callbacks are injected via props from the parent (see `useCronJobs` hook).

```
AutomationDetailPane     (right main content column)
└── CronJobDetail        (view/edit mode)
    ├── FeaturedRunOutput (latest / fallback successful run, auto-expanded)
    │   └── InlineChatInput (continue in chat after completion)
    └── RunItem          (per run in history)
        └── InlineChatInput (continue in chat after completion)

AutomationDetailDialog   (standalone modal — wraps CronJobDetail; used by the Activity page's Day view)
└── CronJobDetail
```

### State flow

- `App.tsx` owns `automationsSelectedJobId`; non-null renders `AutomationDetailPane` in `<main>` instead of the chat body.
- Entering chat (`handleSetAppMode('chat')`) clears the selection, so chat and automation views stay mutually exclusive.
- The selection clears automatically when the job is deleted.
- `Escape` cancels an in-progress edit first, then closes the detail (returns to chat).

### Real-time updates

`CronJobDetail` receives live run state updates via two mechanisms: it subscribes to `cron:run` Wails runtime events for push-based updates, and also polls the run list at 1-second intervals via `setInterval`. Run logs for in-progress runs are polled at 1-second intervals while expanded.

## Schedule Presets (`schedule-utils.ts`)

| Preset | Cron pattern |
|---|---|
| `none` | manual job (no cron expression; edit mode only) |
| `hourly` | `{m} * * * *` |
| `daily` | `{m} {h} * * *` |
| `weekdays` | `{m} {h} * * 1-5` |
| `weekly` | `{m} {h} * * {days}` |
| `custom` | raw cron expression entered by user |

Key exports:

```ts
parseCronToPreset(cronExpr: string): { preset, hour, minute, weekdays, customCron }
cronToHumanReadable(cronExpr: string): string   // e.g. "Daily 09:00"
getPresetSummary(preset, hour, minute, weekdays): string
weekdaysToCron(selected: Set<string>): string   // e.g. "1,3,5"
```

## Dependencies

- `lucide-react` — functional icons (buttons, status indicators)
- `@/components/ui/select` — Radix-based Select for schedule preset picker
- `@/components/ui/collapsible` — Radix Collapsible for run history and config sections
- `@/components/ui/dialog` — Radix Dialog used by `AutomationDetailDialog`
- `@/components/add-to-group-menu` — `AddToGroupButton` for assigning jobs to sidebar groups
- `@/components/grouped-stream-list` — renders AI agent output stream in run log viewer
- `@/components/automation-indicator` — `AutomationIndicator` badge used in dialog header
- `@/hooks/useCronJobs` — provides `CronJobState`, `CronRunState`, `CreateAutomationInput` types
- `@/hooks/useConnectedPlatforms` — provides the set of connected notification platforms for the form selector
- Wails runtime (`EventsOn`) — for live `cron:run` event subscription
- Wails Go bridge (`SelectWorkDir`) — native folder picker dialog

## Props Reference

### `AutomationDetailPane`

```ts
interface AutomationDetailPaneProps {
    job: CronJobState;                       // the selected automation
    onClose: () => void;                     // return to the chat view
    onToggleEnabled: (id: string, enabled: boolean) => void;
    onDelete: (id: string) => void;
    onRunNow: (id: string) => void;
    onUpdate: (id: string, patch: Record<string, unknown>) => Promise<void>;
    loadRuns: (jobId: string) => Promise<CronRunState[]>;
    loadRunLogs: (jobId: string, runId: string) => Promise<StreamItem[]>;
    sourceSessionLabel?: string;
    onNavigateToSourceSession?: () => void;
    onContinueInChat?: (jobId: string, jobName: string, ranAt: number, logsContent: string, userMessage: string) => void;
    onEditInChat?: (jobId: string, jobName: string, description: string, prompt: string, schedule: string, userMessage: string) => void;
}
```

## Usage

```tsx
import { AutomationDetailPane } from '@/components/automations/AutomationDetailPane';

{selectedAutomationJob && (
    <AutomationDetailPane
        job={selectedAutomationJob}
        onClose={() => setAutomationsSelectedJobId(null)}
        onToggleEnabled={toggleEnabled}
        onDelete={deleteJob}
        onRunNow={runNow}
        onUpdate={updateJob}
        loadRuns={loadRuns}
        loadRunLogs={loadRunLogs}
    />
)}
```

### `AutomationDetailDialog`

```ts
interface AutomationDetailDialogProps {
    job: CronJobState | null;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    // ...same CronJobDetail callbacks (onToggleEnabled, onDelete, onRunNow, onUpdate, loadRuns, loadRunLogs)
    sourceSessionLabel?: string;
    onNavigateToSourceSession?: () => void;
    onContinueInChat?: CronJobDetail['onContinueInChat'];
    onEditInChat?: CronJobDetail['onEditInChat'];
}
```

## Run Status Values

| Status | Display |
|---|---|
| `running` | Spinning loader (blue) |
| `ok` | Check circle (green) |
| `missed` | Alert circle (muted), with "the Mac was asleep or Isoform was closed" |
| `error` | Alert circle (red) |
| _(other)_ | "Idle" text |

## Trigger Kinds

| Kind | Badge color |
|---|---|
| `manual` | Blue |
| `catchup` | Amber |
| `schedule` | Neutral |
