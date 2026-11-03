import { act, renderHook, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { AgentStatus, FileAttachment, ImageAttachment } from '@/types';
import { createSession, createStandaloneChatProject, toSessionData } from '@/lib/stream-helpers';
import { openTodayBriefChat, TodayBriefRequestGate, type TodayBriefOpened } from '@/lib/today-chat-navigation';
import { useComposerInput } from './useComposerInput';
import { useProjects } from './useProjects';

const mocks = vi.hoisted(() => ({ bindings: new Map<string, ReturnType<typeof vi.fn>>() }));
vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        if (!mocks.bindings.has(name)) mocks.bindings.set(name, vi.fn().mockResolvedValue(undefined));
        return mocks.bindings.get(name);
    },
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {} }));
vi.mock('@/lib/notify', () => ({ notifyImportant: vi.fn() }));
vi.mock('sonner', () => ({ toast: Object.assign(vi.fn(), { error: vi.fn() }) }));

function binding(name: string) {
    if (!mocks.bindings.has(name)) mocks.bindings.set(name, vi.fn().mockResolvedValue(undefined));
    return mocks.bindings.get(name)!;
}

const images: ImageAttachment[] = [{ mediaType: 'image/png', dataBase64: 'image' }];
const files: FileAttachment[] = [{ id: 'file', fileName: 'notes.txt', mediaType: 'text/plain', dataBase64: 'notes', fileSize: 5, category: 'text' }];
const opened: TodayBriefOpened = {
    ref: { sourceKind: 'brief', sourceId: 'revision', briefId: 'brief' },
    draft: { prefill: { kind: 'brief', title: 'Today' }, counts: { memories: 1, activities: 0, people: 0 }, fingerprint: 'brief-facts' },
};

async function mount() {
    const project = createStandaloneChatProject('sonnet');
    const workspace = project.workspaces[0];
    const prior = { ...createSession('sonnet', workspace.id, 'Prior'), localId: 'prior', sessionId: 'prior-agent',
        permissionOptions: [{ id: 'question_other', name: 'Other', kind: 'allow_once' as const }], permissionToolName: 'AskUserQuestion' };
    const other = { ...createSession('sonnet', workspace.id, 'Other'), localId: 'other', sessionId: 'other-agent' };
    const main = { ...createSession('sonnet', workspace.id, 'Isoform', { kind: 'main' }), localId: 'main', sessionId: 'main-agent' };
    workspace.sessions = [prior, other];
    workspace.activeSessionId = prior.localId;
    project.activeSessionId = prior.localId;
    const renderer = renderHook(() => {
        const projects = useProjects({ model: 'sonnet', setModel: vi.fn(), setError: vi.fn() });
        const composer = useComposerInput({
            activeProject: projects.activeProject, activeWorkspace: projects.activeWorkspace, activeSession: projects.activeSession,
            updateProject: projects.updateProject, status: {} as AgentStatus, model: 'sonnet', setError: vi.fn(),
            hasPendingPermission: (projects.activeSession?.permissionOptions?.length ?? 0) > 0,
            awaitingPlanApproval: projects.activeSession?.awaitingPlanApproval ?? false,
            onRespondToPermission: projects.handleRespondToPermission, onDismissPlanReview: projects.handleDismissPlanReview,
            sendPromptCombo: { meta: false, ctrl: false, alt: false, shift: false, key: 'enter' },
        });
        return { projects, composer };
    });
    await act(async () => {
        renderer.result.current.projects.setProjects([project]);
        renderer.result.current.projects.setActiveProjectId(project.id);
    });
    const info = { projectId: project.id, workspaceId: workspace.id, session: toSessionData(main) };
    return { renderer, project, workspace, info };
}

function start(renderer: Awaited<ReturnType<typeof mount>>['renderer'], prepare: () => Promise<TodayBriefOpened>, timeout = 12000, gate = new TodayBriefRequestGate()) {
    let modeCurrent = true;
    let selection = renderer.result.current.projects.getSelectionRevision();
    const isCurrent = () => modeCurrent && selection === renderer.result.current.projects.getSelectionRevision();
    const draft = renderer.result.current.composer.beginTodayBrief(isCurrent);
    const controller = gate.begin();
    if (!controller) throw new Error('brief request is already pending');
    const promise = openTodayBriefChat(prepare, {
        draft,
        signal: controller.signal,
        openMainChat: (current) => {
            const opening = renderer.result.current.projects.handleOpenMainChat(current);
            selection = renderer.result.current.projects.getSelectionRevision();
            return opening;
        },
        isCurrent,
        focus: vi.fn(),
    }, timeout).finally(() => gate.finish(controller));
    return { promise, controller, leave: () => { modeCurrent = false; gate.cancel(); } };
}

describe('brief opening through actual main-chat selection', () => {
    beforeEach(() => {
        for (const mock of mocks.bindings.values()) mock.mockReset().mockResolvedValue(undefined);
        binding('DetectInstalledApps').mockResolvedValue([]);
        binding('PrepareTodayChatTurn').mockImplementation(async (_tag: string, text: string) => text);
        binding('SendPromptWithTodayContext').mockResolvedValue(1);
    });

    it('blocks sending and answering the previous chat while GetMainChat is pending, then sends the checked brief with attachments', async () => {
        const { renderer, info } = await mount();
        let resolveMain!: (value: typeof info) => void;
        binding('GetMainChat').mockReturnValueOnce(new Promise((resolve) => { resolveMain = resolve; }));
        act(() => {
            renderer.result.current.composer.setPrompt('My question');
            renderer.result.current.composer.setImages(images);
            renderer.result.current.composer.setFiles(files);
        });
        let request!: ReturnType<typeof start>;
        act(() => { request = start(renderer, async () => opened); });
        expect(renderer.result.current.projects.activeSession?.localId).toBe('prior');
        await act(async () => { await renderer.result.current.composer.sendPrompt(); await renderer.result.current.composer.sendImmediate('Quick question'); });
        expect(binding('RespondToPermission')).not.toHaveBeenCalled();
        expect(binding('SendPrompt')).not.toHaveBeenCalled();
        expect(binding('SendPromptWithTodayContext')).not.toHaveBeenCalled();
        expect(renderer.result.current.composer.queuedMessages).toEqual([]);
        await act(async () => { resolveMain(info); await request.promise; });
        await waitFor(() => expect(renderer.result.current.projects.activeSession?.localId).toBe('main'));
        expect(renderer.result.current.composer.prompt).toBe('My question');
        expect(renderer.result.current.composer.images).toEqual(images);
        expect(renderer.result.current.composer.files).toEqual(files);
        await act(async () => { await renderer.result.current.composer.sendPrompt(); });
        expect(binding('SendPromptWithTodayContext')).toHaveBeenCalledOnce();
        const args = binding('SendPromptWithTodayContext').mock.calls[0];
        expect(args[0]).toBe('My question');
        expect(args[4]).toBe('main');
        expect(args[5]).toEqual([{ media_type: 'image/png', data_base64: 'image' }]);
        expect(args[6]).toEqual([{ file_name: 'notes.txt', media_type: 'text/plain', data_base64: 'notes', file_size: 5 }]);
        expect(args.at(-1)).toMatchObject({ ...opened.ref, generatedFingerprint: 'brief-facts' });
        renderer.unmount();
    });

    it('does not queue a message or dismiss a plan in the previous streaming chat while loading', async () => {
        const { renderer, project, info } = await mount();
        await act(async () => renderer.result.current.projects.updateProject(project.id, (current) => ({
            ...current, workspaces: current.workspaces.map((workspace) => ({
                ...workspace, sessions: workspace.sessions.map((session) => session.localId === 'prior'
                    ? { ...session, streaming: true, awaitingPlanApproval: true } : session),
            })),
        })));
        let resolveMain!: (value: typeof info) => void;
        binding('GetMainChat').mockReturnValueOnce(new Promise((resolve) => { resolveMain = resolve; }));
        act(() => renderer.result.current.composer.setPrompt('My question'));
        let request!: ReturnType<typeof start>;
        act(() => { request = start(renderer, async () => opened); });
        await act(async () => { await renderer.result.current.composer.sendPrompt(); await renderer.result.current.composer.sendImmediate('Quick question'); });
        expect(binding('RespondToPermission')).not.toHaveBeenCalled();
        expect(binding('SendPrompt')).not.toHaveBeenCalled();
        expect(renderer.result.current.composer.queuedMessages).toEqual([]);
        expect(renderer.result.current.composer.prompt).toBe('My question');
        await act(async () => { resolveMain(info); await request.promise; });
        expect(renderer.result.current.projects.activeSession?.localId).toBe('main');
        renderer.unmount();
    });

    it.each(['main session', 'brief preparation'] as const)('allows a second request after leaving during %s and ignores the old completion', async (stage) => {
        const { renderer, info } = await mount();
        const gate = new TodayBriefRequestGate();
        const secondOpened: TodayBriefOpened = { ...opened, ref: { ...opened.ref, sourceId: 'second-revision' } };
        let finishOldMain!: (value: typeof info) => void;
        let finishOldPrepare!: (value: TodayBriefOpened) => void;
        let finishNewMain!: (value: typeof info) => void;
        let finishNewPrepare!: (value: TodayBriefOpened) => void;
        const oldMain = new Promise<typeof info>((resolve) => { finishOldMain = resolve; });
        const oldPrepare = new Promise<TodayBriefOpened>((resolve) => { finishOldPrepare = resolve; });
        const newMain = new Promise<typeof info>((resolve) => { finishNewMain = resolve; });
        const newPrepare = new Promise<TodayBriefOpened>((resolve) => { finishNewPrepare = resolve; });
        const prepareFirst = vi.fn(() => oldPrepare);
        const prepareSecond = vi.fn(() => newPrepare);
        binding('GetMainChat').mockReturnValueOnce(stage === 'main session' ? oldMain : Promise.resolve(info));
        if (stage === 'main session') binding('GetMainChat').mockReturnValueOnce(newMain);
        act(() => { renderer.result.current.composer.setPrompt('Keep this question'); renderer.result.current.composer.setImages(images); renderer.result.current.composer.setFiles(files); });
        let first!: ReturnType<typeof start>;
        act(() => { first = start(renderer, prepareFirst, 12000, gate); });
        if (stage === 'brief preparation') await waitFor(() => expect(prepareFirst).toHaveBeenCalledOnce());
        act(() => first.leave());
        expect(await first.promise).toBe(true);
        expect(gate.pending).toBe(false);
        let second!: ReturnType<typeof start>;
        act(() => { second = start(renderer, prepareSecond, 12000, gate); });
        if (stage === 'main session') {
            await act(async () => { finishOldMain(info); await Promise.resolve(); });
            expect(renderer.result.current.projects.activeSession?.localId).toBe('prior');
            await act(async () => { finishNewMain(info); });
        }
        await waitFor(() => expect(prepareSecond).toHaveBeenCalledOnce());
        if (stage === 'brief preparation') await act(async () => { finishOldPrepare(opened); await Promise.resolve(); });
        // An old finally must not release the second request's latch.
        gate.finish(first.controller);
        expect(gate.pending).toBe(true);
        expect(renderer.result.current.composer.todayPreview).toEqual({ status: 'loading' });
        await act(async () => { await renderer.result.current.composer.sendPrompt(); });
        expect(binding('SendPromptWithTodayContext')).not.toHaveBeenCalled();
        expect(renderer.result.current.composer.prompt).toBe('Keep this question');
        expect(renderer.result.current.composer.images).toEqual(images);
        expect(renderer.result.current.composer.files).toEqual(files);
        await act(async () => { finishNewPrepare(secondOpened); await second.promise; });
        expect(gate.pending).toBe(false);
        await act(async () => { await renderer.result.current.composer.sendPrompt(); });
        const args = binding('SendPromptWithTodayContext').mock.calls[0];
        expect(args[0]).toBe('Keep this question');
        expect(args[5]).toEqual([{ media_type: 'image/png', data_base64: 'image' }]);
        expect(args[6]).toEqual([{ file_name: 'notes.txt', media_type: 'text/plain', data_base64: 'notes', file_size: 5 }]);
        expect(args.at(-1)).toMatchObject({ ...secondOpened.ref, generatedFingerprint: 'brief-facts' });
        if (stage === 'main session') expect(prepareFirst).not.toHaveBeenCalled();
        renderer.unmount();
    });

    it.each(['timeout', 'another chat', 'another page'] as const)('does not select or persist a late GetMainChat after %s', async (reason) => {
        const { renderer, info, project, workspace } = await mount();
        let resolveMain!: (value: typeof info) => void;
        binding('GetMainChat').mockReturnValueOnce(new Promise((resolve) => { resolveMain = resolve; }));
        const prepare = vi.fn(async () => opened);
        act(() => { renderer.result.current.composer.setPrompt('Keep this'); renderer.result.current.composer.setImages(images); renderer.result.current.composer.setFiles(files); });
        let request!: ReturnType<typeof start>;
        act(() => { request = start(renderer, prepare, 20); });
        if (reason === 'another chat') {
            act(() => renderer.result.current.projects.handleSwitchSession(project.id, 'other'));
        } else if (reason === 'another page') {
            request.leave();
        } else {
            await act(async () => { expect(await request.promise).toBe(false); });
        }
        const expected = reason === 'another chat' ? 'other' : 'prior';
        expect(renderer.result.current.projects.activeSession?.localId).toBe(expected);
        binding('SaveActiveProjectID').mockClear();
        binding('UpdateProjectActiveSession').mockClear();
        binding('UpdateWorkspaceActiveSession').mockClear();
        await act(async () => { resolveMain(info); await Promise.resolve(); await Promise.resolve(); });
        if (reason !== 'timeout') await act(async () => { await request.promise; });
        expect(renderer.result.current.projects.activeSession?.localId).toBe(expected);
        expect(renderer.result.current.projects.projects[0].workspaces.find((w) => w.id === workspace.id)?.sessions.map((s) => s.localId)).not.toContain('main');
        expect(binding('SaveActiveProjectID')).not.toHaveBeenCalled();
        expect(binding('UpdateProjectActiveSession')).not.toHaveBeenCalled();
        expect(binding('UpdateWorkspaceActiveSession')).not.toHaveBeenCalled();
        expect(prepare).not.toHaveBeenCalled();
        expect(renderer.result.current.composer.prompt).toBe('Keep this');
        expect(renderer.result.current.composer.images).toEqual(images);
        expect(renderer.result.current.composer.files).toEqual(files);
        renderer.unmount();
    });
});
