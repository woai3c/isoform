import { Profiler } from 'react';
import { act, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import '@/i18n';
import { resetPageQueries } from '@/lib/page-queries';
import { useTodayTopics } from '@/hooks/useTodayTopics';
import { TodayTopicsGrid } from './TodayTopicsGrid';

const probe = vi.hoisted(() => ({ get: vi.fn(), listeners: new Map<string, Set<() => unknown>>() }));
vi.mock('@/lib/wails-shim-app', () => ({ GetTodayTopics: probe.get, RefreshTodayTopics: vi.fn().mockResolvedValue(undefined) }));
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: (name: string, fn: () => unknown) => {
    const set = probe.listeners.get(name) ?? new Set<() => unknown>();
    set.add(fn);
    probe.listeners.set(name, set);
    return () => { set.delete(fn); };
} }));

beforeEach(() => { resetPageQueries(); vi.useFakeTimers(); vi.clearAllMocks(); probe.listeners.clear(); });
afterEach(() => { vi.useRealTimers(); });

// Today review v1.1.7 requires a sourced line on each visible card.
const latest = { text: 'Work moved', at: '2026-10-20T12:00:00Z' };

it('measures ten minutes of identical publications', async () => {
    const topics = [{ id: 'a', kind: 'project', name: 'Atlas', latest }, { id: 'b', kind: 'following', name: 'Research', latest }];
    probe.get.mockImplementation(async () => ({ topics: structuredClone(topics), refreshing: false }));
    let renders = 0;
    const onPick = vi.fn(), onEdit = vi.fn();
    function Harness() {
        const view = useTodayTopics(true);
        return <Profiler id="list" onRender={() => renders++}><TodayTopicsGrid {...view} selectedId={null} onPick={onPick} onRetry={view.refresh} onEdit={onEdit} /></Profiler>;
    }
    const host = render(<Harness />);
    await mounted();
    const cards = screen.getAllByTestId('today-topic-card');
    const initialRenders = renders;
    let indicatorChanges = 0;
    const observer = new MutationObserver((entries) => {
        for (const entry of entries) for (const node of [...entry.addedNodes, ...entry.removedNodes]) {
            if (node instanceof Element && (node.matches('[data-testid="today-topics-refreshing"]') || node.querySelector('[data-testid="today-topics-refreshing"]'))) indicatorChanges++;
        }
    });
    observer.observe(host.container, { childList: true, subtree: true });
    for (let batch = 0; batch < 120; batch++) {
        await vi.advanceTimersByTimeAsync(5000);
        await fire();
    }
    observer.disconnect();
    expect(probe.get).toHaveBeenCalledTimes(121);
    expect(renders - initialRenders).toBe(0);
    expect(indicatorChanges).toBe(0);
    expect(screen.getAllByTestId('today-topic-card')[0]).toBe(cards[0]);
});

/** The mount read follows the shared hook's 250 ms coalescing window. */
async function mounted() {
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
}

/** A topics:updated event. The shared change hook coalesces it for 250 ms, so the read follows. */
async function fire() {
    await act(async () => {
        for (const handler of probe.listeners.get('topics:updated') ?? []) void handler();
        await vi.advanceTimersByTimeAsync(300);
    });
}
const publish = fire;

function TopicHarness() {
    const view = useTodayTopics(true);
    return <TodayTopicsGrid {...view} selectedId={null} onPick={() => {}} onRetry={view.refresh} onEdit={() => {}} />;
}

it('updates one changed card in place and appends new cards despite incoming priority order', async () => {
    const a = { id: 'a', kind: 'project', name: 'Atlas', latest };
    const b = { id: 'b', kind: 'project', name: 'Beacon', latest };
    probe.get.mockResolvedValue({ topics: [a, b], refreshing: false });
    render(<TopicHarness />);
    await mounted();
    const cards = screen.getAllByTestId('today-topic-card');
    probe.get.mockResolvedValue({ topics: [{ id: 'c', kind: 'project', name: 'New', status: 'waiting_on_you', latest }, { ...b, name: 'Beacon updated' }, a], refreshing: false });
    await publish();
    const updated = screen.getAllByTestId('today-topic-card');
    expect(updated.map(card => card.firstElementChild?.textContent)).toEqual(['Atlas', 'Beacon updated', 'New']);
    expect(updated[0]).toBe(cards[0]);
    expect(updated[1]).toBe(cards[1]);
});

it('never shows an indicator for a subsecond refresh, including repeated pending events', async () => {
    const topics = [{ id: 'a', kind: 'project', name: 'Atlas', latest }];
    probe.get.mockResolvedValue({ topics, refreshing: false });
    render(<TopicHarness />);
    await mounted();
    const card = screen.getByTestId('today-topic-card');
    // Timeline (ms): the pending read starts about 250 ms after each event; the
    // indicator would show only after a full second of pending.
    probe.get.mockResolvedValue({ topics: [], refreshing: true });
    await publish();
    await act(async () => { await vi.advanceTimersByTimeAsync(300); });
    await publish();
    await act(async () => { await vi.advanceTimersByTimeAsync(49); });
    expect(screen.queryByTestId('today-topics-refreshing')).not.toBeInTheDocument();
    probe.get.mockResolvedValue({ topics, refreshing: false });
    await publish();
    await act(async () => { await vi.advanceTimersByTimeAsync(2000); });
    expect(screen.queryByTestId('today-topics-refreshing')).not.toBeInTheDocument();
    expect(screen.getByTestId('today-topic-card')).toBe(card);
});

it('shows a slow refresh once after a second and keeps its indicator mounted until completion', async () => {
    const topics = [{ id: 'a', kind: 'project', name: 'Atlas' }];
    probe.get.mockResolvedValue({ topics, refreshing: true });
    render(<TopicHarness />);
    await mounted();
    await act(async () => { await vi.advanceTimersByTimeAsync(1001); });
    const indicator = screen.getByTestId('today-topics-refreshing');
    for (let i = 0; i < 5; i++) {
        await publish();
        expect(screen.getByTestId('today-topics-refreshing')).toBe(indicator);
    }
    probe.get.mockResolvedValue({ topics, refreshing: false });
    await publish();
    expect(screen.queryByTestId('today-topics-refreshing')).not.toBeInTheDocument();
});

// Changed 2026-11-04: the server's refresh cadence no longer drives a timer. The
// topics read once on mount and again on topics:updated; no reads happen with no event.
it('reads once on mount and never on the server supplied ten-minute cadence', async () => {
    probe.get.mockResolvedValue({ topics: [], refreshing: false, refreshIntervalMs: 600_000 });
    const hook = render(<TopicHarness />);
    await mounted();
    await act(async () => { await vi.advanceTimersByTimeAsync(600_001); });
    expect(probe.get).toHaveBeenCalledTimes(1);
    hook.unmount();
});
