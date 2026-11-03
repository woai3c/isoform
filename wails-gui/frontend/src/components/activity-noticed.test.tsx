import { useState } from 'react';
import { act, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { toast } from 'sonner';
import type { NoticedSuggestion } from '@/lib/wails-shim-app';
import { ActivityNoticed, formatRoutineWhen, parseRoutineWhen } from './activity-noticed';
import { resetPageQueries } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';

const fixture = vi.hoisted(() => ({
    bindings: new Map<string, ReturnType<typeof vi.fn>>(),
    listeners: new Map<string, Set<(payload: unknown) => void>>(),
}));

vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        if (!fixture.bindings.has(name)) fixture.bindings.set(name, vi.fn().mockResolvedValue([]));
        return fixture.bindings.get(name);
    },
}));

vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, handler: (payload: unknown) => void) => {
        const listeners = fixture.listeners.get(name) ?? new Set();
        listeners.add(handler);
        fixture.listeners.set(name, listeners);
        return () => listeners.delete(handler);
    },
}));

vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }));

function binding(name: string) {
    if (!fixture.bindings.has(name)) fixture.bindings.set(name, vi.fn().mockResolvedValue([]));
    return fixture.bindings.get(name)!;
}

const DAY = '2026-10-19';
const AT = new Date(2026, 9, 19, 10, 5).getTime();

const followUp: NoticedSuggestion = {
    id: 's-follow',
    kind: 'follow_up',
    momentId: 'act-lamp',
    momentTitle: 'Comparing Halcyon Arc desk lamps for Priya',
    momentStartMs: AT,
    text: 'Follow up with Priya about the Halcyon Arc lamp',
    why: 'They asked which Halcyon Arc finish you ordered.',
};
const routine: NoticedSuggestion = {
    id: 's-routine',
    kind: 'routine',
    momentId: 'act-sheet',
    momentTitle: 'Filling the expense sheet',
    momentStartMs: AT,
    text: 'Weekly expense report',
    why: 'You did the same steps last Friday.',
    routine: {
        name: 'Weekly expense report',
        when: 'weekly fri 16:00',
        steps: ['Open the expense sheet', "Add this week's receipts"],
        will: ['Fill the sheet'],
        wont: ['Submit anything'],
    },
};

describe('Isoform noticed', () => {
    beforeEach(async () => {
        resetPageQueries();
        fixture.bindings.forEach((mock) => mock.mockReset().mockResolvedValue([]));
        fixture.listeners.clear();
        vi.mocked(toast).mockReset();
        vi.mocked(toast.success).mockReset();
        vi.mocked(toast.error).mockReset();
        binding('GetDayNoticed').mockResolvedValue([followUp, routine]);
        await i18n.changeLanguage('en');
    });

    it('shows each suggestion with its reason and the moment it points at, and nothing when there are none', async () => {
        const onOpenMoment = vi.fn();
        const { unmount } = render(<ActivityNoticed dateStr={DAY} onOpenMoment={onOpenMoment} />);
        expect(await screen.findByText('Isoform noticed')).toBeInTheDocument();
        expect(binding('GetDayNoticed')).toHaveBeenCalledWith(DAY);
        expect(screen.getByText('Follow up with Priya about the Halcyon Arc lamp')).toBeInTheDocument();
        expect(screen.getByText('They asked which Halcyon Arc finish you ordered.')).toBeInTheDocument();
        fireEvent.click(screen.getByText(/From .* · Comparing Halcyon Arc desk lamps for Priya/));
        expect(onOpenMoment).toHaveBeenCalledWith('act-lamp');
        expect(screen.getByRole('button', { name: 'Add follow-up' })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Make a routine' })).toBeInTheDocument();
        expect(screen.getAllByRole('button', { name: 'Not now' })).toHaveLength(2);
        unmount();

        // A revisit inside the TTL paints the cached rows with no read; an
        // invalidation (the summary/follow-up events) is what re-reads.
        binding('GetDayNoticed').mockResolvedValue([]);
        pageQueryCache.invalidate(['GetDayNoticed']);
        const { container } = render(<ActivityNoticed dateStr={DAY} onOpenMoment={vi.fn()} />);
        await waitFor(() => expect(binding('GetDayNoticed')).toHaveBeenCalledTimes(2));
        expect(container).toBeEmptyDOMElement();
    });

    it('adds the follow-up from an editable field, with Undo', async () => {
        binding('AddActivityFollowUp').mockResolvedValue({ openLoopId: 'loop', eventId: 'ev', text: 'x' });
        binding('UndoActivityFollowUp').mockResolvedValue(undefined);
        render(<ActivityNoticed dateStr={DAY} onOpenMoment={vi.fn()} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Add follow-up' }));
        const field = screen.getByRole('textbox', { name: 'Follow-up' }) as HTMLInputElement;
        expect(field.value).toBe('Follow up with Priya about the Halcyon Arc lamp');
        fireEvent.change(field, { target: { value: 'Ask Priya which Halcyon Arc finish they want' } });
        fireEvent.keyDown(field, { key: 'Enter' });
        await waitFor(() =>
            expect(binding('AddActivityFollowUp')).toHaveBeenCalledWith('act-lamp', 'Ask Priya which Halcyon Arc finish they want', expect.any(String)),
        );
        expect(await screen.findByRole('status')).toHaveTextContent('Added to What needs your attention.');
        expect(toast.success).not.toHaveBeenCalled();
        const calls = binding('GetDayNoticed').mock.calls.length;
        fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
        await waitFor(() => expect(binding('UndoActivityFollowUp')).toHaveBeenCalledWith('loop', 'ev'));
        await waitFor(() => expect(binding('GetDayNoticed').mock.calls.length).toBeGreaterThan(calls));
    });

    it('keeps the follow-up field and shows a failed add in place', async () => {
        binding('AddActivityFollowUp').mockRejectedValueOnce(new Error('offline'));
        render(<ActivityNoticed dateStr={DAY} onOpenMoment={vi.fn()} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Add follow-up' }));
        fireEvent.click(screen.getByRole('button', { name: 'Add' }));
        expect(await screen.findByRole('alert')).toHaveTextContent("Couldn't add the follow-up. Try again");
        expect(screen.getByRole('textbox', { name: 'Follow-up' })).toHaveValue('Follow up with Priya about the Halcyon Arc lamp');
        expect(toast.error).not.toHaveBeenCalled();
    });

    it('Not now hides the suggestion at once and can be undone', async () => {
        binding('DismissNoticed').mockResolvedValue(undefined);
        binding('UndoDismissNoticed').mockResolvedValue(undefined);
        render(<ActivityNoticed dateStr={DAY} onOpenMoment={vi.fn()} />);
        await screen.findByText('Follow up with Priya about the Halcyon Arc lamp');
        fireEvent.click(screen.getAllByRole('button', { name: 'Not now' })[0]);
        await waitFor(() => expect(binding('DismissNoticed')).toHaveBeenCalledWith(DAY, 's-follow'));
        await waitFor(() => expect(screen.queryByText('Follow up with Priya about the Halcyon Arc lamp')).not.toBeInTheDocument());
        expect(screen.getByRole('status')).toHaveTextContent("Isoform won't suggest this again.");
        expect(toast).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button', { name: 'Undo' }));
        await waitFor(() => expect(binding('UndoDismissNoticed')).toHaveBeenCalledWith(DAY, 's-follow'));
    });

    it('Make a routine opens the draft, saves nothing on Cancel, and saves the edits on Save', async () => {
        binding('SaveNoticedRoutine').mockResolvedValue({ home: 'schedule', id: 'job-1' });
        render(<ActivityNoticed dateStr={DAY} onOpenMoment={vi.fn()} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Make a routine' }));
        const dialog = await screen.findByRole('dialog');
        expect(within(dialog).getByText(/Nothing is saved or run until you press Save/)).toBeInTheDocument();
        expect((within(dialog).getByRole('textbox', { name: 'Name' }) as HTMLInputElement).value).toBe('Weekly expense report');
        expect((within(dialog).getByRole('combobox', { name: 'When it runs' }) as HTMLSelectElement).value).toBe('weekly');
        expect((within(dialog).getByRole('combobox', { name: 'Day' }) as HTMLSelectElement).value).toBe('fri');
        expect((within(dialog).getByLabelText('Time') as HTMLInputElement).value).toBe('16:00');
        fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        expect(binding('SaveNoticedRoutine')).not.toHaveBeenCalled();

        fireEvent.click(screen.getByRole('button', { name: 'Make a routine' }));
        const again = await screen.findByRole('dialog');
        fireEvent.change(within(again).getByRole('textbox', { name: 'Name' }), { target: { value: 'Friday expenses' } });
        fireEvent.change(within(again).getByRole('combobox', { name: 'When it runs' }), { target: { value: 'weekdays' } });
        fireEvent.change(within(again).getByLabelText('Time'), { target: { value: '08:30' } });
        fireEvent.click(within(again).getByRole('button', { name: 'Save' }));
        await waitFor(() =>
            expect(binding('SaveNoticedRoutine')).toHaveBeenCalledWith(DAY, 's-routine', {
                name: 'Friday expenses',
                when: 'weekdays 08:30',
                steps: ['Open the expense sheet', "Add this week's receipts"],
                will: ['Fill the sheet'],
                wont: ['Submit anything'],
            }),
        );
        expect(await screen.findByRole('status')).toHaveTextContent("Scheduled. It runs on the schedule you picked, and you'll find it in Tasks.");
    });

    it('a routine with no set time goes to Knowledge, and Save needs a name and a step', async () => {
        binding('SaveNoticedRoutine').mockResolvedValue({ home: 'knowhow', id: 'routine-1' });
        render(<ActivityNoticed dateStr={DAY} onOpenMoment={vi.fn()} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Make a routine' }));
        const dialog = await screen.findByRole('dialog');
        fireEvent.change(within(dialog).getByRole('textbox', { name: 'Name' }), { target: { value: ' ' } });
        expect(within(dialog).getByRole('button', { name: 'Save' })).toBeDisabled();
        expect(within(dialog).getByText('Give the routine a name.')).toBeInTheDocument();
        fireEvent.change(within(dialog).getByRole('textbox', { name: 'Name' }), { target: { value: 'Expenses' } });
        fireEvent.change(within(dialog).getByRole('textbox', { name: 'Steps (one per line)' }), { target: { value: '  \n ' } });
        expect(within(dialog).getByRole('button', { name: 'Save' })).toBeDisabled();
        fireEvent.change(within(dialog).getByRole('textbox', { name: 'Steps (one per line)' }), { target: { value: 'Open the sheet\n\nAdd receipts' } });
        fireEvent.change(within(dialog).getByRole('combobox', { name: 'When it runs' }), { target: { value: 'none' } });
        fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
        await waitFor(() =>
            expect(binding('SaveNoticedRoutine')).toHaveBeenCalledWith(DAY, 's-routine', expect.objectContaining({
                name: 'Expenses',
                when: '',
                steps: ['Open the sheet', 'Add receipts'],
            })),
        );
        expect(await screen.findByRole('status')).toHaveTextContent('Saved to Knowledge.');
    });

    it('reads the suggestions again when a new summary is ready or a follow-up changes', async () => {
        render(<ActivityNoticed dateStr={DAY} onOpenMoment={vi.fn()} />);
        await screen.findByText('Isoform noticed');
        for (const event of ['activity:summary-ready', 'agency:updated']) {
            const before = binding('GetDayNoticed').mock.calls.length;
            act(() => fixture.listeners.get(event)?.forEach((fn) => fn(DAY)));
            await waitFor(() => expect(binding('GetDayNoticed').mock.calls.length).toBeGreaterThan(before));
        }
    });

    it('translates the box and the routine draft into Chinese', async () => {
        await i18n.changeLanguage('zh-CN');
        render(<ActivityNoticed dateStr={DAY} onOpenMoment={vi.fn()} />);
        expect(await screen.findByText('Isoform 注意到')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: '添加跟进' })).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: '设为例行事务' }));
        expect(await screen.findByRole('button', { name: '保存' })).toBeInTheDocument();
    });
});

describe('routine "when"', () => {
    it('round-trips Go\'s forms', () => {
        for (const when of ['daily 09:30', 'weekdays 08:00', 'weekly fri 16:00']) {
            const parsed = parseRoutineWhen(when);
            expect(formatRoutineWhen(parsed.mode, parsed.day, parsed.time)).toBe(when);
        }
        expect(parseRoutineWhen('').mode).toBe('none');
        expect(formatRoutineWhen('none', 'mon', '09:00')).toBe('');
    });
});

describe('Activity focus from the brief', () => {
    beforeEach(async () => {
        resetPageQueries();
        fixture.bindings.forEach((mock) => mock.mockReset().mockResolvedValue([]));
        fixture.listeners.clear();
        binding('GetDayNoticed').mockResolvedValue([followUp, routine]);
        await i18n.changeLanguage('en');
    });

    it.each(['en', 'zh-CN'])('opens the follow-up field from a fresh read without saving in %s', async (lang) => {
        await i18n.changeLanguage(lang);
        const focus = { date: DAY, suggestionId: followUp.id, action: 'follow_up' as const };
        const { rerender } = render(<ActivityNoticed dateStr={DAY} focus={focus} onOpenMoment={vi.fn()} />);
        const field = await screen.findByDisplayValue(followUp.text);
        fireEvent.change(field, { target: { value: 'An unsaved edit' } });
        rerender(<ActivityNoticed dateStr={DAY} focus={focus} onOpenMoment={vi.fn()} />);
        expect(screen.getByDisplayValue('An unsaved edit')).toBeInTheDocument();
        expect(binding('AddActivityFollowUp')).not.toHaveBeenCalled();
    });

    it('edits the existing scheduled follow-up from Today', async () => {
        const saved = { id: 'job', revision: 'v1', proof: 'proof' };
        const focus = { date: DAY, suggestionId: followUp.id, action: 'follow_up' as const, schedule: saved };
        binding('EditTodayNoticedSchedule').mockResolvedValue({ ...saved, revision: 'v2' });
        render(<ActivityNoticed dateStr={DAY} focus={focus} onOpenMoment={vi.fn()} />);
        const field = await screen.findByRole('textbox', { name: 'Follow-up' });
        fireEvent.change(field, { target: { value: 'Ask Priya next week' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(binding('EditTodayNoticedSchedule')).toHaveBeenCalledWith(focus, saved, 'Ask Priya next week', null));
        expect(binding('AddActivityFollowUp')).not.toHaveBeenCalled();
    });

    it('edits the existing scheduled routine from Today', async () => {
        const saved = { id: 'job', revision: 'v1', proof: 'proof' };
        const focus = { date: DAY, suggestionId: routine.id, action: 'routine' as const, schedule: saved };
        binding('EditTodayNoticedSchedule').mockResolvedValue({ ...saved, revision: 'v2' });
        render(<ActivityNoticed dateStr={DAY} focus={focus} onOpenMoment={vi.fn()} />);
        const dialog = await screen.findByRole('dialog');
        fireEvent.change(within(dialog).getByRole('textbox', { name: 'Name' }), { target: { value: 'Edited expenses' } });
        fireEvent.click(within(dialog).getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(binding('EditTodayNoticedSchedule')).toHaveBeenCalledWith(focus, saved, '', expect.objectContaining({ name: 'Edited expenses', steps: routine.routine!.steps })));
        expect(binding('SaveNoticedRoutine')).not.toHaveBeenCalled();
    });

    it('opens the routine draft without saving, and consumes a missing suggestion', async () => {
        const focus = { date: DAY, suggestionId: routine.id, action: 'routine' as const };
        const { rerender } = render(<ActivityNoticed dateStr={DAY} focus={focus} onOpenMoment={vi.fn()} />);
        expect(await screen.findByRole('dialog')).toBeInTheDocument();
        expect(binding('SaveNoticedRoutine')).not.toHaveBeenCalled();
        fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }));
        rerender(<ActivityNoticed dateStr={DAY} focus={focus} onOpenMoment={vi.fn()} />);
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
        binding('GetDayNoticed').mockResolvedValue([]);
        rerender(<ActivityNoticed dateStr="2026-10-18" focus={{ date: '2026-10-18', suggestionId: 'gone', action: 'follow_up' }} onOpenMoment={vi.fn()} />);
        await waitFor(() => expect(binding('GetDayNoticed')).toHaveBeenLastCalledWith('2026-10-18'));
        expect(screen.queryByRole('textbox')).not.toBeInTheDocument();
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('keeps a closed draft consumed when its day unmounts and returns', async () => {
        function Harness() {
            const [day, setDay] = useState(DAY);
            const [focus, setFocus] = useState<{ date: string; suggestionId: string; action: 'routine' } | null>({ date: DAY, suggestionId: routine.id, action: 'routine' });
            return <>
                <button onClick={() => setDay(day === DAY ? '2026-10-18' : DAY)}>Change day</button>
                <ActivityNoticed key={day} dateStr={day} focus={focus} onFocusConsumed={() => setFocus(null)} onOpenMoment={vi.fn()} />
            </>;
        }
        render(<Harness />);
        expect(await screen.findByRole('dialog')).toBeInTheDocument();
        fireEvent.click(within(screen.getByRole('dialog')).getByRole('button', { name: 'Cancel' }));
        fireEvent.click(screen.getByRole('button', { name: 'Change day' }));
        await waitFor(() => expect(binding('GetDayNoticed')).toHaveBeenLastCalledWith('2026-10-18'));
        fireEvent.click(screen.getByRole('button', { name: 'Change day' }));
        // Back on the first day, the cached rows paint with no new read, and
        // the consumed draft stays closed.
        expect(await screen.findByText('Weekly expense report')).toBeInTheDocument();
        expect(binding('GetDayNoticed')).toHaveBeenCalledTimes(2);
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
    });

    it('ignores a late response from another day', async () => {
        let finishOld!: (items: NoticedSuggestion[]) => void;
        binding('GetDayNoticed').mockReturnValueOnce(new Promise((resolve) => { finishOld = resolve; }));
        const focus = { date: '2026-10-18', suggestionId: followUp.id, action: 'follow_up' as const };
        const { rerender } = render(<ActivityNoticed dateStr={DAY} focus={focus} onOpenMoment={vi.fn()} />);
        binding('GetDayNoticed').mockResolvedValue([]);
        rerender(<ActivityNoticed dateStr={focus.date} focus={focus} onOpenMoment={vi.fn()} />);
        await act(async () => { finishOld([followUp]); });
        expect(screen.queryByDisplayValue(followUp.text)).not.toBeInTheDocument();
        expect(screen.queryByText(followUp.text)).not.toBeInTheDocument();
    });
});
