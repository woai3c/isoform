import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import type { Project } from '@/types';
import { createSession } from '@/lib/stream-helpers';
import i18n from '@/i18n';
import en from '@/i18n/locales/en/chat.json';
import zh from '@/i18n/locales/zh-CN/chat.json';
import { useProjects } from './useProjects';

const mocks = vi.hoisted(() => ({ bindings: new Map<string, ReturnType<typeof vi.fn>>(), setError: vi.fn() }));
vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        if (!mocks.bindings.has(name)) mocks.bindings.set(name, vi.fn().mockResolvedValue(undefined));
        return mocks.bindings.get(name);
    },
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {} }));
vi.mock('@/hooks/useComposerInput', () => ({ sendFirstMessage: vi.fn(), sendSessionPrompt: vi.fn() }));
vi.mock('@/lib/notify', () => ({ notifyImportant: vi.fn() }));
vi.mock('sonner', () => ({ toast: { error: vi.fn() } }));

function binding(name: string) {
    if (!mocks.bindings.has(name)) mocks.bindings.set(name, vi.fn());
    return mocks.bindings.get(name)!;
}

async function mountProjects(requestId: string | undefined = 'work-permission:first') {
    const session = {
        ...createSession('model', 'workspace'), localId: 'work-chat', sessionId: 'agent-session',
        permissionRequestId: requestId, awaitingPlanApproval: true,
        permissionOptions: [{ id: 'acceptEdits', name: 'Approve', kind: 'allow_once' as const }],
        permissionToolName: 'Write',
    };
    const project = {
        id: 'project', name: 'Project', path: '', activeWorkspaceId: 'workspace', activeSessionId: session.localId,
        workspaces: [{ id: 'workspace', isDefault: true, activeSessionId: session.localId, sessions: [session] }],
    } as Project;
    const renderer = renderHook(() => useProjects({ model: 'model', setModel: vi.fn(), setError: mocks.setError }));
    await act(async () => {
        renderer.result.current.setProjects([project]);
        renderer.result.current.setActiveProjectId(project.id);
    });
    return renderer;
}

describe('original work permission responses', () => {
    beforeEach(async () => {
        await i18n.changeLanguage('en');
        for (const mock of mocks.bindings.values()) mock.mockReset().mockResolvedValue(undefined);
        binding('DetectInstalledApps').mockResolvedValue([]);
        mocks.setError.mockReset();
    });

    it.each([
        ['handleApprovePlan', 'acceptEdits'],
        ['handleDismissPlanReview', 'plan'],
        ['handleRejectPlan', 'plan'],
        ['handleRespondToPermission', 'acceptEdits'],
    ] as const)('%s sends the exact request identity', async (handler, option) => {
        const renderer = await mountProjects();
        await act(async () => { await renderer.result.current[handler]('acceptEdits'); });
        expect(binding('RespondToPermission')).toHaveBeenCalledWith('work-permission:first', option);
        renderer.unmount();
    });

    it('keeps the agent session target for ordinary chat permissions', async () => {
        const renderer = await mountProjects('');
        await act(async () => { await renderer.result.current.handleRespondToPermission('acceptEdits'); });
        expect(binding('RespondToPermission')).toHaveBeenCalledWith('agent-session', 'acceptEdits');
        renderer.unmount();
    });

    it.each(['handleRespondToPermission', 'handleRejectPlan'] as const)('stale %s cannot target or clear the newer request or save a false confirmation', async (handler) => {
        const renderer = await mountProjects();
        const staleResponse = renderer.result.current[handler];
        await act(async () => {
            renderer.result.current.updateProject('project', (project) => ({
                ...project, workspaces: project.workspaces.map((workspace) => ({
                    ...workspace, sessions: workspace.sessions.map((session) => ({
                        ...session, permissionRequestId: 'work-permission:second', permissionToolName: 'Bash',
                    })),
                })),
            }));
        });
        binding('RespondToPermission').mockRejectedValue(new Error('request is no longer pending'));
        await act(async () => { await staleResponse('acceptEdits'); });
        expect(binding('RespondToPermission')).toHaveBeenCalledWith('work-permission:first', handler === 'handleRejectPlan' ? 'plan' : 'acceptEdits');
        expect(binding('PersistStreamItem')).not.toHaveBeenCalled();
        expect(renderer.result.current.activeSession).toMatchObject({
            permissionRequestId: 'work-permission:second', permissionToolName: 'Bash',
            permissionOptions: [{ id: 'acceptEdits' }], items: [],
        });
        renderer.unmount();
    });

    it.each(['handleRespondToPermission', 'handleRejectPlan'] as const)('%s saves work confirmation only after the response succeeds', async (handler) => {
        const renderer = await mountProjects();
        let accept!: () => void;
        binding('RespondToPermission').mockReturnValue(new Promise<void>((resolve) => { accept = resolve; }));
        let response!: Promise<unknown>;
        act(() => { response = renderer.result.current[handler]('acceptEdits'); });
        expect(binding('PersistStreamItem')).not.toHaveBeenCalled();
        expect(renderer.result.current.activeSession?.items).toEqual([]);
        await act(async () => { accept(); await response; });
        expect(binding('PersistStreamItem')).toHaveBeenCalledTimes(1);
        expect(renderer.result.current.activeSession?.items).toHaveLength(1);
        renderer.unmount();
    });

    it.each(['handleApprovePlan', 'handleDismissPlanReview', 'handleRejectPlan', 'handleRespondToPermission'] as const)(
        '%s retains the decision on failure and permits retry', async (handler) => {
            const renderer = await mountProjects();
            binding('RespondToPermission').mockRejectedValueOnce(new Error('offline'));
            await act(async () => { await renderer.result.current[handler]('acceptEdits'); });
            expect(renderer.result.current.activeSession).toMatchObject({
                awaitingPlanApproval: true, permissionToolName: 'Write',
                permissionOptions: [{ id: 'acceptEdits' }], items: [],
            });
            expect(binding('PersistStreamItem')).not.toHaveBeenCalled();
            await act(async () => { await renderer.result.current[handler]('acceptEdits'); });
            expect(binding('RespondToPermission')).toHaveBeenCalledTimes(2);
            expect(renderer.result.current.activeSession?.permissionOptions).toBeUndefined();
            renderer.unmount();
        },
    );

    it.each(['en', 'zh-CN'])('localizes accepted permission confirmations in %s', async (language) => {
        await i18n.changeLanguage(language);
        const renderer = await mountProjects();
        const copy = language === 'zh-CN' ? zh.permissionQuestion : en.permissionQuestion;
        await act(async () => { await renderer.result.current.handleRespondToPermission('acceptEdits'); });
        expect(renderer.result.current.activeSession?.items[0].content).toBe(copy.answered.replace('{{answer}}', 'Approve'));
        renderer.unmount();

        const planRenderer = await mountProjects();
        await act(async () => { await planRenderer.result.current.handleRejectPlan(); });
        expect(planRenderer.result.current.activeSession?.items[0].content).toBe(copy.planRejected);
        planRenderer.unmount();
    });
});
