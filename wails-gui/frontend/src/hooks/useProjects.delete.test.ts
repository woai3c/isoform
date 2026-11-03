import { act, renderHook } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { toast } from 'sonner';
import type { Project } from '@/types';
import { createSession } from '@/lib/stream-helpers';
import i18n from '@/i18n';
import enApp from '@/i18n/locales/en/app.json';
import zhApp from '@/i18n/locales/zh-CN/app.json';
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

async function mountProjects(activeLocalId = 'chat-2') {
    const first = { ...createSession('model', 'workspace'), localId: 'chat-1', label: 'First' };
    const second = { ...createSession('model', 'workspace'), localId: 'chat-2', label: 'Second' };
    const third = { ...createSession('model', 'workspace'), localId: 'chat-3', label: 'Third' };
    const project = {
        id: 'project', name: 'Project', path: '', activeWorkspaceId: 'workspace', activeSessionId: activeLocalId,
        workspaces: [{ id: 'workspace', isDefault: true, activeSessionId: activeLocalId, sessions: [first, second, third] }],
    } as Project;
    const renderer = renderHook(() => useProjects({ model: 'model', setModel: vi.fn(), setError: mocks.setError }));
    await act(async () => {
        renderer.result.current.setProjects([project]);
        renderer.result.current.setActiveProjectId(project.id);
    });
    return renderer;
}

async function closeSession(renderer: Awaited<ReturnType<typeof mountProjects>>, sessionId: string) {
    await act(async () => {
        renderer.result.current.handleCloseSession('project', sessionId);
        await Promise.resolve();
    });
}

describe('failed chat delete', () => {
    beforeEach(async () => {
        await i18n.changeLanguage('en');
        for (const mock of mocks.bindings.values()) mock.mockReset().mockResolvedValue(undefined);
        binding('DetectInstalledApps').mockResolvedValue([]);
        mocks.setError.mockReset();
        vi.mocked(toast.error).mockReset();
    });

    it('puts the chat back at its place and shows the error toast when the backend delete fails', async () => {
        const renderer = await mountProjects();
        binding('DeleteSessionData').mockRejectedValue(new Error('database is locked'));
        await closeSession(renderer, 'chat-2');
        const workspace = renderer.result.current.projects[0].workspaces[0];
        expect(workspace.sessions.map((s) => s.localId)).toEqual(['chat-1', 'chat-2', 'chat-3']);
        expect(workspace.activeSessionId).toBe('chat-2');
        expect(workspace.sessions[1]).toMatchObject({
            localId: 'chat-2', label: 'Second', streaming: false, currentRunId: null,
        });
        expect(toast.error).toHaveBeenCalledWith(enApp.sessionItem.deleteFailed);
        renderer.unmount();
    });

    it('restores the chat without stealing the selection when another chat was active', async () => {
        const renderer = await mountProjects('chat-1');
        binding('DeleteSessionData').mockRejectedValue(new Error('database is locked'));
        await closeSession(renderer, 'chat-2');
        const workspace = renderer.result.current.projects[0].workspaces[0];
        expect(workspace.sessions.map((s) => s.localId)).toEqual(['chat-1', 'chat-2', 'chat-3']);
        expect(workspace.activeSessionId).toBe('chat-1');
        renderer.unmount();
    });

    it('localizes the failure toast', async () => {
        await i18n.changeLanguage('zh-CN');
        const renderer = await mountProjects();
        binding('DeleteSessionData').mockRejectedValue(new Error('database is locked'));
        await closeSession(renderer, 'chat-2');
        expect(toast.error).toHaveBeenCalledWith(zhApp.sessionItem.deleteFailed);
        renderer.unmount();
    });

    it('keeps the chat removed when the backend delete succeeds', async () => {
        const renderer = await mountProjects();
        await closeSession(renderer, 'chat-2');
        const workspace = renderer.result.current.projects[0].workspaces[0];
        expect(workspace.sessions.map((s) => s.localId)).toEqual(['chat-1', 'chat-3']);
        expect(workspace.activeSessionId).toBe('chat-1');
        expect(toast.error).not.toHaveBeenCalled();
        renderer.unmount();
    });
});
