import { cleanup, render, screen, waitFor, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { CronJobState } from '@/hooks/useCronJobs';
import { AutomationDetailPane } from './AutomationDetailPane';

const mocks = vi.hoisted(() => ({ recordTasks: vi.fn(async () => undefined), toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }));
vi.mock('./CronJobDetail', () => ({ CronJobDetail: () => <div data-testid="cron-detail" /> }));
vi.mock('sonner', () => ({ toast: mocks.toast }));
vi.mock('@/lib/wails-shim-app', async () => ({ ...await vi.importActual<object>('@/lib/wails-shim-app'), RecordTasksAction: mocks.recordTasks, RecordTasksSectionSeen: vi.fn() }));

const job = {
    id: 'job', name: 'Private weekly report', description: '', enabled: true,
    scheduleKind: 'manual', scheduleConfig: {}, scheduleDescription: '',
    targetKind: 'agent', targetConfig: {}, notifyDm: false, notifyPlatform: 'none',
} as unknown as CronJobState;

function mount(extra: Partial<Parameters<typeof AutomationDetailPane>[0]> = {}) {
    const props = {
        job, onClose: vi.fn(), onToggleEnabled: vi.fn(async () => true), onDelete: vi.fn(async () => true),
        onRunNow: vi.fn(async () => true), onUpdate: vi.fn(async () => undefined), loadRuns: vi.fn(async () => []), loadRunLogs: vi.fn(async () => []),
        ...extra,
    };
    render(<AutomationDetailPane {...props} />);
    return props;
}
const noToast = () => { expect(mocks.toast).not.toHaveBeenCalled(); expect(mocks.toast.error).not.toHaveBeenCalled(); };

beforeEach(async () => { vi.clearAllMocks(); await i18n.changeLanguage('en'); });
afterEach(() => cleanup());

// K6: Scheduled's detail (C02-C04) says each failure in place.
describe("Scheduled's detail failures in place (K6)", () => {
    it('puts the switch back with "Didn\'t save. Try again" when a pause fails', async () => {
        const onToggleEnabled = vi.fn(async (_id: string, _on: boolean, opts?: { onRefused?: (reason?: string) => void }) => { opts?.onRefused?.('storage'); return false; });
        mount({ onToggleEnabled });
        await userEvent.click(screen.getByRole('button', { name: 'Active' }));
        const line = await screen.findByTestId('schedule-toggle-failed');
        expect(line).toHaveTextContent("Didn't save. Try again");
        expect(line).toHaveAttribute('data-failure-line', 'copper');
        expect(screen.getByRole('button', { name: 'Active' })).toBeInTheDocument();
        expect(onToggleEnabled).toHaveBeenCalledWith('job', false, expect.anything());
        expect(mocks.recordTasks).toHaveBeenCalledWith('C03', 'do', 'failed', expect.any(Number));
        noToast();
    });

    it('says why Run now was refused, in gray under the buttons', async () => {
        mount({ onRunNow: vi.fn(async (_id: string, opts?: { onRefused?: (reason?: string) => void }) => { opts?.onRefused?.('Waiting for your approval'); return false; }) });
        await userEvent.click(screen.getByRole('button', { name: 'Run now' }));
        const line = await screen.findByTestId('schedule-run-refused');
        expect(line).toHaveTextContent('Waiting for your approval');
        expect(line).toHaveAttribute('data-failure-line', 'gray');
        noToast();
    });

    it('keeps the delete dialog open with its line when a delete fails', async () => {
        const onDelete = vi.fn(async () => false);
        mount({ onDelete });
        await userEvent.click(screen.getByTitle('More actions'));
        await userEvent.click(await screen.findByRole('menuitem', { name: 'Delete' }));
        const dialog = await screen.findByRole('dialog');
        await userEvent.click(within(dialog).getByRole('button', { name: 'Delete' }));
        expect(await within(dialog).findByTestId('schedule-delete-failed')).toHaveTextContent("Didn't delete. Try again");
        expect(screen.getByRole('dialog')).toBeInTheDocument();
        onDelete.mockResolvedValueOnce(true);
        await userEvent.click(within(dialog).getByRole('button', { name: 'Try again' }));
        await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
        expect(mocks.recordTasks).toHaveBeenCalledWith('C04', 'do', 'failed', expect.any(Number));
        expect(mocks.recordTasks).toHaveBeenCalledWith('C04', 'do', 'done', expect.any(Number));
        expect(JSON.stringify(mocks.recordTasks.mock.calls)).not.toContain('Private');
        noToast();
    });
});
