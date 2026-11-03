import { readFileSync } from 'node:fs';
import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { useState } from 'react';
import { expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import enActivity from '@/i18n/locales/en/activity.json';
import { ActivityPage, type ActivitySectionId } from './activity-page';
import type { CronJobsProp } from './timeline/useCalendarData';

// Replays real payloads and measured binding durations from the temporary Go
// fixture. This measures React/jsdom loading order, not native WebView speed.
// See docs/hub-reviews/claude-codex-activity-speed-self.md for reproduction.
const fixture = vi.hoisted(() => ({ profile: {} as Record<string, any>, completed: new Set<string>(), monthRequests: [] as Promise<unknown>[], monthCalls: 0 }));
vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        return () => {
            const monthCall = name === 'GetWeeklySummary' || name === 'GetActivityMonthStats';
            const ms = name === 'GetWeeklySummary'
                ? fixture.profile.monthMs * ((fixture.monthCalls++ % 6) + 1) / 6
                : fixture.profile.timingsMs?.[name] ?? 0;
            const pending = new Promise((resolve) => {
                setTimeout(() => { fixture.completed.add(name); resolve(fixture.profile[name] ?? []); }, ms);
            });
            if (monthCall) fixture.monthRequests.push(pending);
            return pending;
        };
    },
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {}, ClipboardSetText: vi.fn() }));
vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }));

const cronJobs = { jobs: [], loading: false } as unknown as CronJobsProp;
function Page() {
    const [section, setSection] = useState<ActivitySectionId>('workedOn');
    return <ActivityPage initialDate={fixture.profile.date} section={section} onSectionChange={setSection} cronJobs={cronJobs} />;
}

it.skipIf(!process.env.ACTIVITY_SPEED_FIXTURE)('measures first rows, full day data and a session reopen with the database fixture', async () => {
    fixture.profile = JSON.parse(readFileSync(process.env.ACTIVITY_SPEED_FIXTURE!, 'utf8'));
    await i18n.changeLanguage('en');
    Element.prototype.scrollIntoView = vi.fn();
    globalThis.ResizeObserver ??= class { observe() {} unobserve() {} disconnect() {} } as unknown as typeof ResizeObserver;
    let started = performance.now();
    const page = render(<Page />);
    await screen.findByText('Public fixture task 0');
    const firstRowsMs = performance.now() - started;
    await waitFor(() => {
        for (const name of ['GetActivitiesInRange', 'EnsureDailySummary', 'GetKnowledgeEntries', 'GetMemoryEntries']) expect(fixture.completed.has(name)).toBe(true);
    });
    const fullPageMs = performance.now() - started;
    expect(screen.getByText('Public fixture task 25')).toBeInTheDocument();
    page.unmount();
    started = performance.now();
    const again = render(<Page />);
    await screen.findByText('Public fixture task 0');
    const reopenMs = performance.now() - started;
    // Header day arrows previously refired the hidden six-week heatmap.
    fixture.monthRequests = [];
    const previousDay = screen.getByRole('button', { name: enActivity.dayNav.previous });
    started = performance.now();
    fireEvent.click(previousDay);
    await act(async () => { await Promise.all(fixture.monthRequests); });
    const heatmapArrowMs = performance.now() - started;
    console.info(JSON.stringify({ firstRowsMs, fullPageMs, reopenMs, heatmapArrowMs, heatmapArrowRequests: fixture.monthRequests.length, optimized: fixture.profile.optimized }));
    await act(async () => again.unmount());
});
