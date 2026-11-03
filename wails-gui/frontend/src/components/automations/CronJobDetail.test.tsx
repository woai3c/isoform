import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { CronJobState, CronRunState } from '@/hooks/useCronJobs';
import type { StreamItem } from '@/types';
import { CronJobDetail } from './CronJobDetail';

const mocks = vi.hoisted(() => ({ refreshPlatforms: vi.fn(), openDeliverable: vi.fn(), selectWorkDir: vi.fn(), recordTasks: vi.fn(async () => undefined), toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }) }));
vi.mock('../../../wailsjs/go/main/App', () => ({
    OpenAutomationDeliverable: mocks.openDeliverable,
    SelectWorkDir: mocks.selectWorkDir,
}));
vi.mock('sonner', () => ({ toast: mocks.toast }));
vi.mock('@/lib/wails-shim-app', async () => ({ ...await vi.importActual<object>('@/lib/wails-shim-app'), OpenAutomationDeliverable: mocks.openDeliverable, SelectWorkDir: mocks.selectWorkDir, RecordTasksAction: mocks.recordTasks, RecordTasksSectionSeen: vi.fn() }));
vi.mock('../../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {} }));
vi.mock('@/hooks/useConnectedPlatforms', () => ({
    useConnectedPlatforms: () => ({ connected: [], refresh: mocks.refreshPlatforms }),
}));
vi.mock('../stream-card', async () => ({
    ...await vi.importActual('@/lib/chat-error'),
    StreamCard: ({ item }: { item: StreamItem }) => <div>{item.content}</div>,
}));
vi.mock('../processing-block', () => ({ ProcessingBlock: () => <div /> }));
vi.mock('../grouped-stream-list', () => ({
    GroupedStreamList: ({ items, onOpenDeliverable }: { items: StreamItem[]; onOpenDeliverable?: (path: string) => void }) => (
        <div>{items.map((item) => <div key={item.id}>{item.content}</div>)}<button type="button" onClick={() => onOpenDeliverable?.('/tmp/out/report.md')}>report.md</button></div>
    ),
}));
vi.mock('../markdown-content', () => ({ MarkdownContent: ({ content }: { content: string }) => <div>{content}</div> }));

const job = {
    id: 'job', name: 'Daily report', description: '', enabled: true,
    scheduleKind: 'manual', scheduleConfig: {}, scheduleDescription: '',
    targetKind: 'agent', targetConfig: { prompt: 'Prepare the report' },
    notifyDm: false, notifyPlatform: 'none', sourceSessionId: '', surfaceKind: '',
} as CronJobState;

function run(patch: Partial<CronRunState> = {}): CronRunState {
    return {
        id: 'run', jobId: job.id, startedAt: 1000, finishedAt: 2000,
        status: 'ok', triggerKind: 'manual', error: '', durationMs: 1000,
        resultSummary: '', hasLogs: false, notifyResult: '', backend: 'claude-code',
        ...patch,
    } as CronRunState;
}

beforeEach(async () => {
    await i18n.changeLanguage('en');
});
afterEach(async () => { cleanup(); await i18n.changeLanguage('en'); });

describe('CronJobDetail attribution and blocked results', () => {
    it.each([
        ['en', 'Claude Code stopped after 1 action. Automatic retry is blocked.', 'Isoform stopped after 2 actions. The outcome is unknown. Automatic retry is blocked.'],
        ['zh-CN', 'Claude Code 在执行 1 项操作后停止。不会自动重试。', 'Isoform 在执行 2 项操作后停止。结果尚不确定。不会自动重试。'],
    ])('localizes blocked errors in the latest result and history row in %s', async (language, latestMessage, historyMessage) => {
        await i18n.changeLanguage(language);
        const rawLatest = 'assistant stopped after 1 action: automatic retry is blocked';
        const rawHistory = 'Isoform stopped after 2 actions; unknown outcome: context canceled';
        const loadRuns = vi.fn().mockResolvedValue([
            run({ status: 'blocked', actionsStarted: 1, error: rawLatest }),
            run({ id: 'older', startedAt: 500, status: 'blocked', backend: 'native', actionsStarted: 2, error: rawHistory }),
        ]);
        render(<CronJobDetail job={job} onUpdate={vi.fn()} loadRuns={loadRuns} loadRunLogs={vi.fn().mockResolvedValue([])} />);
        expect(await screen.findByText(latestMessage)).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: /Run history|运行历史/ }));
        expect(await screen.findByText(historyMessage)).toBeInTheDocument();
        expect(screen.queryByText(rawLatest)).not.toBeInTheDocument();
        expect(screen.queryByText(rawHistory)).not.toBeInTheDocument();
        expect(screen.queryByTitle(rawLatest)).not.toBeInTheDocument();
        expect(screen.queryByTitle(rawHistory)).not.toBeInTheDocument();
    });

    it('keeps one outer attribution when the real nested transcript opens', async () => {
        const logs: StreamItem[] = [
            { id: 'prompt', kind: 'user', content: 'Prepare the report' },
            { id: 'reply', kind: 'assistant', content: 'Saved the report', runId: 'agent-run' },
        ];
        const loadRuns = vi.fn().mockResolvedValue([run({ hasLogs: true })]);
        const loadRunLogs = vi.fn().mockResolvedValue(logs);
        render(<CronJobDetail job={job} onUpdate={vi.fn()} loadRuns={loadRuns} loadRunLogs={loadRunLogs} />);
        expect(await screen.findByText('Ran on Claude Code')).toBeInTheDocument();
        fireEvent.click(await screen.findByRole('button', { name: 'Run details' }));
        await waitFor(() => expect(screen.getAllByText('Saved the report')).toHaveLength(2));
        expect(screen.getAllByText('Ran on Claude Code')).toHaveLength(1);
    });

    it.each([
        ['en', 'assistant stopped after 3 actions: process exited', 'Claude Code stopped after 3 actions. Automatic retry is blocked.'],
        ['en', 'assistant stopped before any actions: process exited', 'Claude Code stopped before any actions. Automatic retry is blocked.'],
        ['zh-CN', 'assistant stopped; unknown outcome: context canceled', 'Claude Code 已停止。结果尚不确定。不会自动重试。'],
        ['zh-CN', 'interrupted by app restart; run outcome unknown; automatic retry is blocked', 'Claude Code 已停止。结果尚不确定。不会自动重试。'],
    ])('localizes older blocked records without action metadata: %s, %s', async (language, error, message) => {
        await i18n.changeLanguage(language);
        render(<CronJobDetail
            job={job}
            onUpdate={vi.fn()}
            loadRuns={vi.fn().mockResolvedValue([run({ status: 'blocked', error })])}
            loadRunLogs={vi.fn().mockResolvedValue([])}
        />);
        expect(await screen.findByText(message)).toHaveAttribute('title', message);
        expect(screen.queryByText(error)).not.toBeInTheDocument();
    });
});

// K6: Scheduled's detail says every failed read and write in place.
describe('CronJobDetail failures in place (K6)', () => {
    const noToast = () => { expect(mocks.toast).not.toHaveBeenCalled(); expect(mocks.toast.error).not.toHaveBeenCalled(); };

    it("says the run history couldn't load, never 'no runs'", async () => {
        const loadRuns = vi.fn().mockRejectedValue(new Error('offline'));
        render(<CronJobDetail job={job} onUpdate={vi.fn()} loadRuns={loadRuns} loadRunLogs={vi.fn()} />);
        const line = await screen.findByTestId('run-history-failed');
        expect(line).toHaveTextContent("Couldn't load the run history. Try again");
        expect(line.className).not.toMatch(/error/);
        expect(screen.queryByText(/No runs yet/)).toBeNull();
        noToast();
    });

    it("says a run's output couldn't load, never 'No log entries'", async () => {
        const loadRunLogs = vi.fn().mockRejectedValue(new Error('offline'));
        render(<CronJobDetail job={job} onUpdate={vi.fn()} loadRuns={vi.fn().mockResolvedValue([run({ hasLogs: true })])} loadRunLogs={loadRunLogs} />);
        expect(await screen.findByTestId('run-output-failed')).toHaveTextContent("Couldn't load this run's output. Try again");
        expect(screen.queryByText('No log entries')).toBeNull();
        loadRunLogs.mockResolvedValue([{ id: 'r', kind: 'assistant', content: 'Fresh output' }]);
        fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
        expect(await screen.findAllByText('Fresh output')).not.toHaveLength(0);
        expect(mocks.recordTasks).toHaveBeenCalledWith('C13', 'do', 'done', expect.any(Number));
    });

    it("says the folder picker and Save failed, keeping the editor and its words", async () => {
        mocks.selectWorkDir.mockRejectedValue(new Error('no picker'));
        const onUpdate = vi.fn().mockRejectedValue(new Error('disk full'));
        const ref = { current: null as null | import('./CronJobDetail').CronJobDetailHandle };
        render(<CronJobDetail ref={ref} job={job} onUpdate={onUpdate} loadRuns={vi.fn().mockResolvedValue([])} loadRunLogs={vi.fn()} />);
        await waitFor(() => expect(ref.current).not.toBeNull());
        await act(async () => { ref.current!.enterEdit(); });
        fireEvent.click(screen.getByRole('button', { name: 'Select' }));
        await waitFor(() => expect(mocks.selectWorkDir).toHaveBeenCalled());
        expect(await screen.findByTestId('folder-picker-failed')).toHaveTextContent("Couldn't open the folder picker. Try again");
        const name = screen.getByDisplayValue('Daily report');
        fireEvent.change(name, { target: { value: 'Renamed report' } });
        await act(async () => { await ref.current!.save(); });
        const line = await screen.findByTestId('schedule-save-failed');
        expect(line).toHaveTextContent("Didn't save. Try again");
        expect(line).toHaveAttribute('data-failure-line', 'copper');
        expect(screen.getByDisplayValue('Renamed report')).toBeInTheDocument();
        expect(mocks.recordTasks).toHaveBeenCalledWith('C08', 'do', 'failed', expect.any(Number));
        noToast();
    });

    it("says a file that won't open under the output, not as a toast", async () => {
        mocks.openDeliverable.mockRejectedValue(new Error('missing'));
        const logs: StreamItem[] = [{ id: 'r', kind: 'assistant', content: 'Done' }];
        render(<CronJobDetail job={job} onUpdate={vi.fn()} loadRuns={vi.fn().mockResolvedValue([run({ hasLogs: true })])} loadRunLogs={vi.fn().mockResolvedValue(logs)} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Run details' }));
        fireEvent.click(await screen.findByRole('button', { name: 'report.md' }));
        await waitFor(() => expect(mocks.openDeliverable).toHaveBeenCalledWith('job', '/tmp/out/report.md'));
        const line = await screen.findByTestId('run-file-failed');
        expect(line).toHaveTextContent("Couldn't open report.md. Try again");
        expect(line).toHaveAttribute('data-failure-line', 'gray');
        expect(mocks.recordTasks).toHaveBeenCalledWith('C15', 'open', 'failed', expect.any(Number));
        noToast();
    });
});
