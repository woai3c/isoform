import { cleanup, fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { Sidebar, UserPopoverMenu } from './sidebar';
import { STANDALONE_CHAT_PROJECT_ID, createSession } from '@/lib/stream-helpers';
import { resetPageQueries } from '@/lib/page-queries';
import type { Project, Session } from '@/types';

// Hover/keyboard-focus prefetch: a pointer or keyboard pass over a navigation
// row starts that page's primary reads so the click paints from cache.
const fixture = vi.hoisted(() => ({ bindings: new Map<string, ReturnType<typeof vi.fn>>(), calls: [] as string[] }));
vi.mock('@/lib/wails-shim-app', () => new Proxy({}, {
    has: () => true,
    get: (_target, name: string) => {
        if (name === 'then') return undefined;
        if (!fixture.bindings.has(name)) fixture.bindings.set(name, vi.fn(async () => {
            fixture.calls.push(name);
            if (name === 'GetMainChat') return { session: { localId: 'main' } };
            if (name === 'GetAboutInfo') return { update: { status: 'idle', version: '0.0.0' }, later: false };
            return [];
        }));
        return fixture.bindings.get(name);
    },
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: () => () => {} }));
vi.mock('@/hooks/useUserProfile', () => ({ useUserProfile: () => ({ profile: null, loading: false }) }));
vi.mock('@/components/observing-status', () => ({ ObservingStatus: () => null }));

function buildProjects(sessions: Session[]): Project[] {
    return [{
        id: STANDALONE_CHAT_PROJECT_ID,
        name: 'General Chat',
        path: '',
        activeSessionId: null,
        activeWorkspaceId: 'ws-1',
        sortOrder: 0,
        workspaces: [{ id: 'ws-1', label: 'General Chat', isDefault: true, activeSessionId: null, sessions, createdAt: 1, updatedAt: 1 }],
    } as unknown as Project];
}

function renderSidebar(sessions: Session[] = []) {
    const noop = () => {};
    return render(
        <Sidebar
            projects={buildProjects(sessions)}
            activeProjectId={STANDALONE_CHAT_PROJECT_ID}
            collapsed={false}
            width={260}
            onToggleCollapse={noop}
            onOpenSearch={noop}
            onOpenSettings={noop}
            onOpenActivity={noop}
            onOpenKnowledge={noop}
            onOpenMemory={noop}
            onOpenPeople={noop}
            onOpenToday={noop}
            onOpenTasks={noop}
            onOpenMainChat={noop}
            onSwitchSession={noop}
            onCloseSession={noop}
            onTogglePin={noop}
            onRenameSession={noop}
            onMarkSessionRead={noop}
            onMarkSessionUnread={noop}
            onForkSession={noop}
            appMode="today"
        />,
    );
}

beforeEach(async () => {
    await i18n.changeLanguage('en');
    resetPageQueries();
    fixture.calls = [];
});
afterEach(() => {
    cleanup();
    resetPageQueries();
});

describe('sidebar row prefetch', () => {
    it.each([
        ['Activity', ['EnsureDailySummary', 'GetActivitiesInRange', 'GetDayThreads']],
        ['Search', ['GetTodayTopics', 'ListHandoffs']],
        ['Today', ['GetTodayTopics', 'GetLatestBrief']],
        ['Chat', ['GetMainChat']],
    ] as const)('pointerenter on the %s row starts its primary reads', async (label, expected) => {
        renderSidebar();
        fireEvent.pointerEnter(screen.getByRole('button', { name: label }));
        await vi.waitFor(() => {
            for (const name of expected) expect(fixture.calls).toContain(name);
        });
    });

    it('keyboard focus on the Activity row starts its reads', async () => {
        renderSidebar();
        fireEvent.focus(screen.getByRole('button', { name: 'Activity' }));
        await vi.waitFor(() => expect(fixture.calls).toContain('EnsureDailySummary'));
    });

    it('pointerenter on a pinned session row warms its stream pages', async () => {
        const pinned = { ...createSession('claude-sonnet-4-5', 'ws-1', 'Fixture chat'), pinned: true, updatedAt: 10 };
        renderSidebar([pinned]);
        fireEvent.pointerEnter(document.querySelector(`[data-session-row="${pinned.localId}"]`)!);
        await vi.waitFor(() => {
            const stream = fixture.bindings.get('LoadStreamItemsPage');
            expect(stream?.mock.calls).toEqual(expect.arrayContaining([[pinned.localId, -1, 50]]));
        });
    });
});

describe('You row prefetch', () => {
    it.each([
        ['About you', ['GetAboutYou', 'GetMemoryEntries', 'GetMemoryStatus', 'GetAboutYouPortrait']],
        ['How you do things', ['ListKnowHow', 'GetKnowledgeEntries', 'GetKnowHowMatch']],
        ['Who you know', ['ListPeopleOverview', 'ListPrincipalsWithStats', 'GetContactsAccessStatus']],
    ] as const)('pointerenter on the %s sidebar row starts its page reads', async (label, expected) => {
        renderSidebar();
        const you = document.querySelector('[data-sidebar-section="you"]') as HTMLElement;
        fireEvent.pointerEnter(within(you).getByRole('button', { name: label }));
        await vi.waitFor(() => {
            for (const name of expected) expect(fixture.calls).toContain(name);
        });
    });
});

describe('account menu prefetch', () => {
    it('pointerenter on Settings starts its page reads', async () => {
        const noop = () => {};
        render(<UserPopoverMenu displayName="You" onOpenSettings={noop} />);
        fireEvent.pointerEnter(screen.getByRole('button', { name: /Settings/ }));
        await vi.waitFor(() => expect(fixture.calls).toContain('GetSettingsOverview'));
    });
});
