# Timeline Components

Data hook for the Activity page's Day view and header date picker in the
Yansu desktop app. (The standalone Calendar page merged into the Activity
page; this directory keeps the shared day/month data hook.)

## Files

### `useCalendarData.ts`

Central data hook that owns the day/month state and side effects for the
Activity page's date-driven views. The selected day is lifted to the caller:
`useCalendarData(cronJobs, selectedDate, setSelectedDate)` takes a
`CronJobsProp` (from `useCronJobs`) plus the controlled date, and returns
everything the Day view and the header's date-picker popover need.

**State managed:**
- `displayMonth` — the popover calendar's month (synced to `selectedDate`)
- `heatmapData` — per-day `DayHeatmapStats` for the visible month grid (the day dots)
- `dailyData` — detailed summary for the selected date (`main.DailySummaryData`)
- `automationRuns`, `createdAutomations`, `knowledge`, `memoryEntries` — typed lists for the day
- `hoveredHour` — the hour column under the cursor in the hourly activity bar

**Data loading:**
- `loadMonthHeatmap` — fetches weekly summaries, automation runs, knowledge, and memory in parallel for the entire calendar view window (6-week span)
- `loadDayData` — fetches all categories filtered to the selected date; kicks off digest generation for past days whose cached digest is missing or partial

**Real-time updates:** subscribes to Wails events (`cron:state`, `activity:summary-ready`, `activity:moments-ready`) and refetches on window focus / visibility change.

**Hook return value:**

| Field | Type | Description |
|---|---|---|
| `selectedDate` / `setSelectedDate` | `Date` | Currently selected day (controlled by the caller) |
| `displayMonth` / `setDisplayMonth` | `Date` | Month shown in the heatmap grid |
| `heatmapData` | `Map<string, DayHeatmapStats>` | Per-day stats for the visible grid |
| `heatmapLoading` | `boolean` | Loading state for heatmap fetch |
| `dailyData` | `main.DailySummaryData \| null` | Hourly activity summary for the selected day |
| `dayLoading` | `boolean` | Loading state for day-data fetch |
| `automationRuns`, `createdAutomations`, `knowledge`, `memoryEntries` | typed arrays | Per-category lists for the selected day |
| `hoveredHour` / `setHoveredHour` | `number \| null` | Hour column under cursor in the hourly activity bar |
| `hourRange` | `{ start: number; end: number }` | Visible hour window for the activity timeline bar |
| `maxHourly` | `number` | Peak minute-count in `hourlyActive` (for bar scaling) |
| `handleDeleteAutomation(jobId)` | `(string) => void` | Deletes an automation job and clears its runs from local state |
| `reloadDailySummary()` | `() => Promise<void>` | Force-refetches the daily summary for the selected date |

**Exported helpers:**

| Function | Description |
|---|---|
| `formatDateStr(d)` | `YYYY-MM-DD` string from a `Date` |
| `formatDurationMs(ms)` | Human-readable duration (`1h 30m`) |
| `formatTimeFromMs(ms)` | Locale time string from epoch ms |
| `dayStartMs(d)` / `dayEndMs(d)` | Day bounds in epoch ms (end is exclusive) |

**Exported types:** `DayHeatmapStats`, `CronRunWithJobName`, `CronJobsProp`

---

## Dependencies

- **Wails bindings** (`wailsjs/go/main/App`, `wailsjs/runtime/runtime`) — all backend calls and event subscriptions
- `@/hooks/useCronJobs` — provides `CronJobState` / `CronRunState` types and job management callbacks
- `@/types` — `StreamItem` type

## Usage

The hook is consumed by the Activity page (`components/activity-page.tsx`),
which passes the day data to the Day view (`components/activity-day-view.tsx`)
and the heatmap to the header's date-picker popover:

```tsx
const [selectedDate, setSelectedDate] = useState(new Date());
const calendarData = useCalendarData(cronJobs, selectedDate, setSelectedDate);
```
