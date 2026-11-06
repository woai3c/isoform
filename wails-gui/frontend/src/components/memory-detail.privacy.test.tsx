import { act, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { main } from '../../wailsjs/go/models';
import type { AboutYouBelief, CommunicationDraftExample } from '@/lib/wails-shim-app';
import { MemoryDetail } from './memory-detail';

// Guard tests for I1: a shown Mail draft example withdraws synchronously on
// the app-protection/privacy events and cannot be restored by a response
// that was in flight across the change. The event registry is never cleared:
// lib/privacy-reset.ts subscribes its forwarders once per module lifetime.
const mocks = vi.hoisted(() => ({
    getExamples: vi.fn(),
    events: new Map<string, Set<(value: unknown) => void>>(),
}));
vi.mock('@/lib/wails-shim-app', () => ({
    IdeaMeaningGroups: () => Promise.resolve(null),
    SaveMemoryEntry: vi.fn(),
    GetMemoryEvidenceReadable: async () => [],
    GetEntryUses: async () => ({ total: 0, lastAt: 0, surfaces: [] }),
    ListMemoryVersions: async () => [],
    GetCommunicationDraftExamples: mocks.getExamples,
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, callback: (value: unknown) => void) => {
        const listeners = mocks.events.get(name) ?? new Set<(value: unknown) => void>();
        listeners.add(callback);
        mocks.events.set(name, listeners);
        return () => { listeners.delete(callback); };
    },
}));
vi.mock('@/lib/notify', () => ({ notifyToast: vi.fn() }));

const entry = {
    id: 'communication',
    title: 'Language with Jia',
    content: '# Language with Jia\n- You write to Jia in Chinese.\n',
    apps: ['WeChat', 'Mail'],
    updatedAt: 1000,
} as unknown as main.MemoryEntry;

const belief: AboutYouBelief = {
    id: 'communication',
    title: 'Language with Jia',
    area: 'communication',
    status: 'confirmed',
    confidence: 2,
    firstLearnedAt: 0,
    updatedAt: 1000,
    sessionCount: 2,
    dayCount: 1,
    confirmedAt: 1000,
};

const mailExample: CommunicationDraftExample = {
    id: 'draft-mail-1',
    to: 'jia@example.com',
    subject: 'Friday',
    text: 'Hi Jia, Friday works — confirming here.',
    app: 'Mail',
    recipientName: 'Jia',
    language: 'English',
    sentences: 1,
    greeting: true,
    beliefIds: ['communication'],
    createdAt: Date.now(),
};

const account = { provider: 'claude', kind: 'subscription', name: 'Bo', on: true };

function detail() {
    return <MemoryDetail entry={entry} belief={belief} editRequest={0} onSaved={vi.fn()} onForget={vi.fn()} onReject={vi.fn()} />;
}

async function emit(name: string, payload?: unknown) {
    await act(async () => {
        for (const listener of [...(mocks.events.get(name) ?? [])]) listener(payload);
    });
}

describe('communication draft examples privacy withdrawal', () => {
    beforeEach(async () => {
        vi.resetAllMocks();
        await i18n.changeLanguage('en');
        mocks.getExamples.mockResolvedValue([mailExample]);
    });

    it('withdraws a shown Mail draft example in the event turn on sensitive-apps:updated, then re-reads the backend policy', async () => {
        render(detail());
        expect(await screen.findByText(mailExample.text)).toBeInTheDocument();
        expect(screen.getByText('To jia@example.com · Mail')).toBeInTheDocument();
        expect(screen.getByText('Written the way you write to Jia: English, one sentence, with a greeting.')).toBeInTheDocument();
        expect(mocks.getExamples).toHaveBeenCalledTimes(1);
        // Mail is now protected: the backend policy returns no examples.
        mocks.getExamples.mockResolvedValue([]);
        await emit('sensitive-apps:updated');
        // The exact draft text and recipient are already gone.
        expect(screen.queryByText(mailExample.text)).not.toBeInTheDocument();
        expect(screen.queryByText('To jia@example.com · Mail')).not.toBeInTheDocument();
        // The re-read through the backend policy keeps them gone.
        await waitFor(() => expect(mocks.getExamples).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(screen.queryByText('How a draft shows it')).not.toBeInTheDocument());
        expect(screen.queryByText(mailExample.text)).not.toBeInTheDocument();
    });

    it('never restores an examples response that was held across protection', async () => {
        let resolveFirst!: (rows: CommunicationDraftExample[]) => void;
        mocks.getExamples
            .mockReturnValueOnce(new Promise((resolve) => { resolveFirst = resolve; }))
            .mockResolvedValue([]);
        render(detail());
        await waitFor(() => expect(mocks.getExamples).toHaveBeenCalledTimes(1));
        await emit('sensitive-apps:updated');
        await waitFor(() => expect(mocks.getExamples).toHaveBeenCalledTimes(2));
        await act(async () => resolveFirst([mailExample]));
        expect(screen.queryByText(mailExample.text)).not.toBeInTheDocument();
        expect(screen.queryByText('To jia@example.com · Mail')).not.toBeInTheDocument();
        expect(screen.queryByText('How a draft shows it')).not.toBeInTheDocument();
    });

    it('covers the account sign-out boundary: an unknown baseline fails closed, a quota refresh keeps examples, sign-out withdraws', async () => {
        render(detail());
        expect(await screen.findByText(mailExample.text)).toBeInTheDocument();
        expect(mocks.getExamples).toHaveBeenCalledTimes(1);
        // First account event while the signed-in baseline is unknown fails
        // closed: withdraw and re-read (the backend still returns the draft).
        await emit('model-accounts:updated', { accounts: [account] });
        await waitFor(() => expect(mocks.getExamples).toHaveBeenCalledTimes(2));
        expect(await screen.findByText(mailExample.text)).toBeInTheDocument();
        // An ordinary refresh with the same signed-in account is not a reset.
        await emit('model-accounts:updated', { accounts: [{ ...account }] });
        expect(mocks.getExamples).toHaveBeenCalledTimes(2);
        expect(screen.getByText(mailExample.text)).toBeInTheDocument();
        // Sign-out: the account is gone and the backend returns no examples.
        mocks.getExamples.mockResolvedValue([]);
        await emit('model-accounts:updated', { accounts: [] });
        expect(screen.queryByText(mailExample.text)).not.toBeInTheDocument();
        await waitFor(() => expect(mocks.getExamples).toHaveBeenCalledTimes(3));
        await waitFor(() => expect(screen.queryByText('How a draft shows it')).not.toBeInTheDocument());
    });
});
