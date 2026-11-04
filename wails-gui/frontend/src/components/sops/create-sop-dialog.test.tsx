import { act, cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { SopAuthorStatus } from '@/lib/wails-shim-app';
import { CreateSopDialog } from './CreateSopDialog';

const mocks = vi.hoisted(() => ({ generate: vi.fn(), toast: vi.fn() }));
vi.mock('@/lib/wails-shim-app', () => ({ GenerateSOPFromDescription: mocks.generate }));
vi.mock('sonner', () => ({ toast: Object.assign(mocks.toast, { success: mocks.toast, error: mocks.toast }) }));

const DESCRIPTION = 'Every Monday I pull the numbers for Juniper Bakery and send them to Ravi.';

function status(extra: Partial<SopAuthorStatus>): SopAuthorStatus {
    return { running: false, lastEvent: '', ...extra } as SopAuthorStatus;
}

async function submit(authorStatus: SopAuthorStatus | null = null) {
    const view = render(<CreateSopDialog open onOpenChange={() => {}} authorStatus={authorStatus} />);
    fireEvent.change(screen.getByRole('textbox'), { target: { value: DESCRIPTION } });
    await act(async () => {
        fireEvent.click(screen.getByTitle(i18n.language === 'en' ? 'Create' : '创建'));
    });
    return view;
}

describe('Create playbook (design review v1.0.0 W1)', () => {
    beforeEach(async () => {
        vi.resetAllMocks();
        Element.prototype.scrollTo = vi.fn() as unknown as Element['scrollTo'];
        await i18n.changeLanguage('en');
        mocks.generate.mockResolvedValue(undefined);
    });
    afterEach(() => cleanup());

    it('says where the draft will show while it drafts, then that it is in Tasks › Up next', async () => {
        const view = await submit(status({ running: true }));
        expect(mocks.generate).toHaveBeenCalledWith(DESCRIPTION);
        expect(
            screen.getByText('Drafting a playbook from your description and your recent work. You can close this. It shows in Tasks, under Up next.'),
        ).toBeInTheDocument();
        view.rerender(<CreateSopDialog open onOpenChange={() => {}} authorStatus={status({ lastEvent: 'saved' })} />);
        expect(screen.getByText('Drafted. It’s in Tasks, under Up next.')).toBeInTheDocument();
        expect(mocks.toast).not.toHaveBeenCalled();
    });

    it('says nothing new was made when it matches a playbook you have', async () => {
        const view = await submit(status({ running: true }));
        view.rerender(<CreateSopDialog open onOpenChange={() => {}} authorStatus={status({ lastEvent: 'skipped' })} />);
        expect(screen.getByText('This matches a playbook you already have, so nothing new was made.')).toBeInTheDocument();
    });

    it('a draft that failed is copper with Try again, which drafts the same words again', async () => {
        const view = await submit(status({ running: true }));
        view.rerender(<CreateSopDialog open onOpenChange={() => {}} authorStatus={status({ lastEvent: 'failed' })} />);
        expect(screen.getByText('Didn’t draft the playbook.')).toBeInTheDocument();
        await act(async () => {
            fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
        });
        expect(mocks.generate).toHaveBeenNthCalledWith(2, DESCRIPTION);
    });

    it('a start that failed keeps the words and Try again sends them', async () => {
        mocks.generate.mockRejectedValueOnce(new Error('no model'));
        await submit();
        expect(screen.getByText('Didn’t start the playbook.')).toBeInTheDocument();
        expect(screen.getByRole('textbox')).toHaveValue(DESCRIPTION);
        await act(async () => {
            fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
        });
        expect(mocks.generate).toHaveBeenCalledTimes(2);
        expect(document.body.textContent).not.toContain('no model');
    });

    it('reads in Chinese', async () => {
        await i18n.changeLanguage('zh-CN');
        const view = await submit(status({ running: true }));
        expect(screen.getByText('正在根据你的描述和最近的工作起草操作手册。可以关掉这个窗口，起草好后会出现在「任务」的「接下来」里。')).toBeInTheDocument();
        view.rerender(<CreateSopDialog open onOpenChange={() => {}} authorStatus={status({ lastEvent: 'saved' })} />);
        expect(screen.getByText('起草好了，在「任务」的「接下来」里。')).toBeInTheDocument();
    });
});
