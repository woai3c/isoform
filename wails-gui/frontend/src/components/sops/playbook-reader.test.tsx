import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { SopEntry } from '@/lib/wails-shim-app';
import { PlaybookReader } from './PlaybookReader';

const mocks = vi.hoisted(() => ({
    runNow: vi.fn(),
    runSop: vi.fn(),
    toast: vi.fn(),
}));
vi.mock('@/lib/wails-shim-app', () => ({
    AcceptPlaybookSuggestion: vi.fn(),
    DeclinePlaybookSuggestion: vi.fn(),
    DeleteSOP: vi.fn(),
    GetSOPScript: vi.fn(() => Promise.resolve('')),
    RunPlaybookNow: mocks.runNow,
    RunSOP: mocks.runSop,
    SaveSOP: vi.fn(),
    SetPlaybookSchedule: vi.fn(),
}));
vi.mock('@/components/sops/playbook-io', () => ({
    usePlaybookTransfer: () => ({ exportPlaybook: vi.fn(), importPlaybook: vi.fn() }),
}));
vi.mock('sonner', () => ({ toast: Object.assign(mocks.toast, { success: mocks.toast, error: mocks.toast }) }));

const entry: SopEntry = {
    id: 'juniper-weekly-numbers',
    title: 'Juniper Bakery weekly numbers',
    description: 'Pulls the week’s orders and sends them to Ravi.',
    trigger: 'Mondays around 9:00',
    status: 'confirmed',
    source: 'observed',
    sourceIds: [],
    version: 1,
    createdAt: 1,
    updatedAt: 1,
    scripts: [],
    sites: [],
    body: '# Goal\nSend the numbers.\n',
};

function renderReader() {
    return render(
        <PlaybookReader
            entry={entry}
            plan={null}
            channels={null}
            defaultChannels={[]}
            onClose={vi.fn()}
            onChanged={vi.fn()}
            onStartSop={vi.fn(() => Promise.resolve())}
        />,
    );
}

describe('Playbook reader (design review v1.0.0 W10)', () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        await i18n.changeLanguage('en');
    });
    afterEach(() => cleanup());

    it('Run now says it is running, in place, never a toast', async () => {
        mocks.runNow.mockResolvedValue(undefined);
        renderReader();
        await act(async () => {
            fireEvent.click(screen.getByRole('button', { name: 'Run now' }));
        });
        expect(mocks.runNow).toHaveBeenCalledWith(entry.id, []);
        expect(screen.getByText('Running. Isoform will send you the result.')).toBeInTheDocument();
        expect(mocks.toast).not.toHaveBeenCalled();
    });

    it('a Run now that failed is copper in place and Try again runs it', async () => {
        mocks.runNow.mockRejectedValueOnce(new Error('exit status 2')).mockResolvedValue(undefined);
        renderReader();
        await act(async () => {
            fireEvent.click(screen.getByRole('button', { name: 'Run now' }));
        });
        expect(screen.getByText('Didn’t run.')).toBeInTheDocument();
        expect(document.body.textContent).not.toContain('exit status 2');
        await act(async () => {
            fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
        });
        expect(mocks.runNow).toHaveBeenCalledTimes(2);
        expect(screen.getByText('Running. Isoform will send you the result.')).toBeInTheDocument();
        expect(mocks.toast).not.toHaveBeenCalled();
    });

    it('a Run in chat that failed is copper in place', async () => {
        mocks.runSop.mockRejectedValueOnce(new Error('no session'));
        renderReader();
        await act(async () => {
            fireEvent.click(screen.getByRole('button', { name: 'Run in chat' }));
        });
        expect(screen.getByRole('button', { name: 'Try again' })).toBeInTheDocument();
        expect(document.body.textContent).not.toContain('no session');
    });

    it('reads in Chinese', async () => {
        await i18n.changeLanguage('zh-CN');
        mocks.runNow.mockRejectedValueOnce(new Error('exit status 2'));
        renderReader();
        const run = screen.getAllByRole('button').find((b) => b.getAttribute('aria-busy') !== null)!;
        await act(async () => {
            fireEvent.click(run);
        });
        expect(screen.getByRole('button', { name: '重试' })).toBeInTheDocument();
    });
});
