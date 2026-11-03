import { describe, expect, it, vi } from "vitest";
import { openTodayMainChat, openTodayBriefChat } from "./today-chat-navigation";

const context = { sourceKind: "brief_item", sourceId: "meeting-1", briefId: "brief-1" };

describe("Today main chat navigation", () => {
    it("opens the main session before prefilling its unsent draft and context", async () => {
        let resolveMain!: (id: string) => void;
        const navigation = {
            openMainChat: vi.fn(() => new Promise<string>((resolve) => { resolveMain = resolve; })),
            prefill: vi.fn(),
            focus: vi.fn(),
            openSideChat: vi.fn(),
            send: vi.fn(),
        };
        const opening = openTodayMainChat('Help me prepare for "Design review".', context, navigation);
        expect(navigation.openMainChat).toHaveBeenCalledOnce();
        expect(navigation.prefill).not.toHaveBeenCalled();
        resolveMain("main-session");
        expect(await opening).toBe(true);
        expect(navigation.prefill).toHaveBeenCalledWith("main-session", 'Help me prepare for "Design review".', context);
        expect(navigation.focus).toHaveBeenCalledOnce();
        expect(navigation.openSideChat).not.toHaveBeenCalled();
        expect(navigation.send).not.toHaveBeenCalled();
    });

    it("does not prefill a different session when opening the main chat fails", async () => {
        const navigation = { openMainChat: vi.fn().mockResolvedValue(null), prefill: vi.fn(), focus: vi.fn() };
        expect(await openTodayMainChat("Ask about the brief.", context, navigation)).toBe(false);
        expect(navigation.prefill).not.toHaveBeenCalled();
        expect(navigation.focus).not.toHaveBeenCalled();
    });

    it('ignores a superseded click or composer edit while main chat is loading', async () => {
        let finish!: (id: string) => void;
        let current = true;
        const navigation = {
            openMainChat: vi.fn(() => new Promise<string>((resolve) => { finish = resolve; })),
            prefill: vi.fn(), focus: vi.fn(), isCurrent: () => current,
        };
        const opening = openTodayMainChat('Ask about the brief.', context, navigation);
        current = false;
        finish('main-session');
        expect(await opening).toBe(true);
        expect(navigation.prefill).not.toHaveBeenCalled();
        expect(navigation.focus).not.toHaveBeenCalled();
    });

    it('focuses the composer as soon as the draft text is in, before preparation finishes', async () => {
        let finish!: () => void;
        const navigation = {
            openMainChat: vi.fn().mockResolvedValue('main-session'),
            prefill: vi.fn(() => new Promise<void>((resolve) => { finish = resolve; })),
            focus: vi.fn(), isCurrent: () => true,
        };
        const opening = openTodayMainChat('Ask about the brief.', context, navigation);
        await Promise.resolve();
        await Promise.resolve();
        expect(navigation.prefill).toHaveBeenCalledOnce();
        expect(navigation.focus).toHaveBeenCalledOnce();
        finish();
        expect(await opening).toBe(true);
        expect(navigation.focus).toHaveBeenCalledOnce();
    });

    it('passes the start options through to the prefill', async () => {
        const navigation = { openMainChat: vi.fn().mockResolvedValue('main-session'), prefill: vi.fn(), focus: vi.fn() };
        const options = { paused: true };
        expect(await openTodayMainChat('Draft a plan.', context, navigation, options)).toBe(true);
        expect(navigation.prefill).toHaveBeenCalledWith('main-session', 'Draft a plan.', context, options);
    });

    it("leaves an empty draft alone and propagates a main chat failure", async () => {
        const navigation = { openMainChat: vi.fn().mockRejectedValue(new Error("offline")), prefill: vi.fn(), focus: vi.fn() };
        expect(await openTodayMainChat("  ", context, navigation)).toBe(false);
        expect(navigation.openMainChat).not.toHaveBeenCalled();
        await expect(openTodayMainChat("Ask about the brief.", context, navigation)).rejects.toThrow("offline");
        expect(navigation.prefill).not.toHaveBeenCalled();
    });
});

describe('brief chat opening', () => {
    const opened = { ref: { sourceKind: 'brief', sourceId: 'revision', briefId: 'brief' }, draft: { prefill: { kind: 'brief', title: 'Today' }, counts: { memories: 0, activities: 0, people: 0 }, fingerprint: 'hash' } };

    it('opens and focuses Chat before the brief check resolves', async () => {
        let resolve!: (value: typeof opened) => void;
        const finish = vi.fn();
        const navigation = { openMainChat: vi.fn().mockResolvedValue('main'), draft: { select: vi.fn(), finish }, focus: vi.fn(), isCurrent: () => true };
        const result = openTodayBriefChat(() => new Promise((done) => { resolve = done; }), navigation);
        await vi.waitFor(() => expect(navigation.draft.select).toHaveBeenCalledWith('main'));
        expect(navigation.focus).toHaveBeenCalledOnce();
        expect(finish).not.toHaveBeenCalled();
        resolve(opened);
        expect(await result).toBe(true);
        expect(finish).toHaveBeenCalledWith(opened);
    });

    it('releases the loading draft when preparation fails', async () => {
        const finish = vi.fn();
        const navigation = { openMainChat: vi.fn().mockResolvedValue('main'), draft: { select: vi.fn(), finish }, focus: vi.fn(), isCurrent: () => true };
        expect(await openTodayBriefChat(() => Promise.reject(new Error('offline')), navigation)).toBe(false);
        expect(finish).toHaveBeenCalledWith();
    });

    it('times out a stalled binding and ignores its late result', async () => {
        vi.useFakeTimers();
        try {
            let resolve!: (value: typeof opened) => void;
            const finish = vi.fn();
            const navigation = { openMainChat: vi.fn().mockResolvedValue('main'), draft: { select: vi.fn(), finish }, focus: vi.fn(), isCurrent: () => true };
            const result = openTodayBriefChat(() => new Promise((done) => { resolve = done; }), navigation, 100);
            await Promise.resolve();
            await vi.advanceTimersByTimeAsync(100);
            expect(await result).toBe(false);
            expect(finish).toHaveBeenCalledTimes(1);
            expect(finish).toHaveBeenCalledWith();
            resolve(opened);
            await Promise.resolve();
            expect(finish).toHaveBeenCalledTimes(1);
        } finally {
            vi.useRealTimers();
        }
    });

    it('does not start preparation if navigation times out', async () => {
        vi.useFakeTimers();
        try {
            let resolve!: (value: string) => void;
            const prepare = vi.fn();
            const navigation = { openMainChat: () => new Promise<string>((done) => { resolve = done; }), draft: { select: vi.fn(), finish: vi.fn() }, focus: vi.fn(), isCurrent: () => true };
            const result = openTodayBriefChat(prepare, navigation, 100);
            await vi.advanceTimersByTimeAsync(100);
            expect(await result).toBe(false);
            resolve('main');
            await Promise.resolve();
            expect(prepare).not.toHaveBeenCalled();
            expect(navigation.draft.select).not.toHaveBeenCalled();
        } finally {
            vi.useRealTimers();
        }
    });
});
