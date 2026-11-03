import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { TodayChatContextRef, TodayChatDraft, TodayChatDraftResult } from './today-chat-navigation';
import { peekTodayChatDraft, prepareTodayChatDraftNow, resetTodayChatDrafts, warmTodayChatDrafts } from './today-chat-prepared';

const calls = vi.hoisted(() => ({
    batch: vi.fn<(refs: TodayChatContextRef[]) => Promise<TodayChatDraftResult[]>>(),
    single: vi.fn<(ref: TodayChatContextRef) => Promise<TodayChatDraft>>(),
}));
vi.mock('../../wailsjs/go/main/App', () => ({
    PrepareTodayChatDrafts: calls.batch,
    PrepareTodayChatDraft: calls.single,
}));

const brief = { sourceKind: 'brief', sourceId: 'revision-1', briefId: 'brief-1' };
const item = { sourceKind: 'purpose', sourceId: 'payroll' };
const draft = (title: string): TodayChatDraft => ({ prefill: { kind: 'purpose', title }, counts: { memories: 1, activities: 0, people: 0 }, fingerprint: `${title}-facts` });
const settle = (...refs: TodayChatContextRef[]) => vi.waitFor(() => {
    for (const ref of refs.length ? refs : [brief, item]) expect(peekTodayChatDraft(ref)?.status).not.toBe('pending');
});

describe('Today drafts prepared before the click', () => {
    beforeEach(() => {
        vi.clearAllMocks();
        resetTodayChatDrafts();
        calls.batch.mockImplementation(async (refs) => refs.map((ref) => ({ draft: draft(ref.sourceId) })));
    });

    it('prepares every shown action in one read, and only once while nothing changes', async () => {
        warmTodayChatDrafts('brief', [{ ref: brief, signature: 'v1' }, { ref: item, signature: 'v1' }]);
        expect(peekTodayChatDraft(item)?.status).toBe('pending');
        await settle();
        expect(calls.batch).toHaveBeenCalledOnce();
        expect(calls.batch.mock.calls[0][0]).toEqual([brief, item]);
        expect(peekTodayChatDraft(item)).toMatchObject({ status: 'ready', draft: draft('payroll') });
        warmTodayChatDrafts('brief', [{ ref: brief, signature: 'v1' }, { ref: item, signature: 'v1' }]);
        expect(calls.batch).toHaveBeenCalledOnce();
    });

    it('prepares again only the action whose item or brief changed', async () => {
        warmTodayChatDrafts('brief', [{ ref: brief, signature: 'v1' }, { ref: item, signature: 'v1' }]);
        await settle();
        calls.batch.mockImplementationOnce(async () => [{ draft: draft('payroll renamed') }]);
        warmTodayChatDrafts('brief', [{ ref: brief, signature: 'v1' }, { ref: item, signature: 'v2' }]);
        expect(peekTodayChatDraft(item)?.status).toBe('pending');
        expect(peekTodayChatDraft(brief)?.status).toBe('ready');
        await settle();
        expect(calls.batch).toHaveBeenCalledTimes(2);
        expect(calls.batch.mock.calls[1][0]).toEqual([item]);
        expect(peekTodayChatDraft(item)).toMatchObject({ status: 'ready', draft: draft('payroll renamed') });
    });

    it('drops actions a group no longer shows, and keeps other groups', async () => {
        const news = { sourceKind: 'news', sourceId: 'https://example.com/story', briefId: '2026-10-19' };
        warmTodayChatDrafts('brief', [{ ref: brief, signature: 'v1' }, { ref: item, signature: 'v1' }]);
        warmTodayChatDrafts('news', [{ ref: news, signature: 'v1' }]);
        await settle(brief, item, news);
        warmTodayChatDrafts('brief', [{ ref: brief, signature: 'v2' }]);
        expect(peekTodayChatDraft(item)).toBeUndefined();
        expect(peekTodayChatDraft(news)?.status).toBe('ready');
    });

    it('records unavailable items and failed reads apart, and a click prepares again', async () => {
        calls.batch.mockImplementationOnce(async () => [{ error: 'unavailable' }, { error: 'failed' }]);
        warmTodayChatDrafts('brief', [{ ref: brief, signature: 'v1' }, { ref: item, signature: 'v1' }]);
        await settle();
        expect(peekTodayChatDraft(brief)).toMatchObject({ status: 'failed', unavailable: true });
        expect(peekTodayChatDraft(item)).toMatchObject({ status: 'failed', unavailable: false });
        calls.single.mockResolvedValueOnce(draft('payroll'));
        expect(await prepareTodayChatDraftNow(item)).toMatchObject({ status: 'ready', draft: draft('payroll') });
        expect(peekTodayChatDraft(item)?.status).toBe('ready');
        calls.single.mockRejectedValueOnce(new Error('TODAY_CHAT_CONTEXT_UNAVAILABLE'));
        expect(await prepareTodayChatDraftNow(brief)).toMatchObject({ status: 'failed', unavailable: true });
    });

    it('treats a batch that could not run as failed, without throwing', async () => {
        calls.batch.mockRejectedValueOnce(new Error('Wails binding PrepareTodayChatDrafts is not available'));
        warmTodayChatDrafts('brief', [{ ref: brief, signature: 'v1' }]);
        await vi.waitFor(() => expect(peekTodayChatDraft(brief)?.status).toBe('failed'));
        expect(peekTodayChatDraft(brief)).toMatchObject({ unavailable: false });
    });

    it('lets a click prepare its own action without waiting for a slow batch', async () => {
        let finish!: (results: TodayChatDraftResult[]) => void;
        calls.batch.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
        warmTodayChatDrafts('brief', [{ ref: item, signature: 'v1' }]);
        calls.single.mockResolvedValueOnce(draft('payroll now'));
        expect(await prepareTodayChatDraftNow(item)).toMatchObject({ status: 'ready', draft: draft('payroll now') });
        expect(calls.single).toHaveBeenCalledOnce();
        // The batch still settles its own entry.
        finish([{ draft: draft('payroll') }]);
        await settle(item);
        expect(peekTodayChatDraft(item)).toMatchObject({ status: 'ready', draft: draft('payroll') });
    });

    it('keeps a click from overwriting a newer preparation of a changed item', async () => {
        warmTodayChatDrafts('brief', [{ ref: item, signature: 'v1' }]);
        await settle(item);
        let finish!: (draft: TodayChatDraft) => void;
        calls.single.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
        const clicked = prepareTodayChatDraftNow(item);
        calls.batch.mockImplementationOnce(async () => [{ draft: draft('payroll renamed') }]);
        warmTodayChatDrafts('brief', [{ ref: item, signature: 'v2' }]);
        await settle(item);
        finish(draft('payroll stale'));
        expect(await clicked).toMatchObject({ status: 'ready', draft: draft('payroll stale') });
        expect(peekTodayChatDraft(item)).toMatchObject({ status: 'ready', draft: draft('payroll renamed') });
    });

    it('records a paused observation from the batch and from a click', async () => {
        calls.batch.mockImplementationOnce(async () => [{ error: 'paused' }]);
        warmTodayChatDrafts('brief', [{ ref: item, signature: 'v1' }]);
        await settle(item);
        expect(peekTodayChatDraft(item)).toMatchObject({ status: 'failed', unavailable: true, paused: true });
        calls.single.mockRejectedValueOnce(new Error('TODAY_CHAT_OBSERVATION_PAUSED'));
        expect(await prepareTodayChatDraftNow(item)).toMatchObject({ status: 'failed', paused: true });
    });
});
