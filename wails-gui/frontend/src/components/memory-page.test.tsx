import { useRef, useState } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import en from '@/i18n/locales/en/memory.json';
import zh from '@/i18n/locales/zh-CN/memory.json';
import type { GoalItemView, GoalsPageView, MemoryForgetPreview } from '@/lib/wails-shim-app';
import { resetPageQueries } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { MemoryPage } from './memory-page';
import { Dialog, DialogTitle } from '@/components/ui/dialog';
import { RetainedDialogContent } from '@/components/retained-dialog-content';

const mocks = vi.hoisted(() => ({
    getEntries: vi.fn(),
    getStatus: vi.fn(),
    getEvidence: vi.fn(),
    listVersions: vi.fn(),
    search: vi.fn(),
    saveEntry: vi.fn(),
    getPreview: vi.fn(),
    forgetEntry: vi.fn(),
    rejectWithPreview: vi.fn(),
    notify: vi.fn(),
    getAbout: vi.fn(),
    getPortrait: vi.fn(),
    confirm: vi.fn(),
    undoConfirm: vi.fn(),
    notQuite: vi.fn(),
    undoNotQuite: vi.fn(),
    undoWording: vi.fn(),
    getUses: vi.fn(),
    getExamples: vi.fn(),
    getVersion: vi.fn(),
    sync: vi.fn(),
    listGoals: vi.fn(),
    getGoal: vi.fn(),
    moveGoal: vi.fn(),
    events: new Map<string, Set<(value: unknown) => void>>(),
}));

vi.mock('@/lib/wails-shim-app', () => ({
    GetMemoryEntries: mocks.getEntries,
    GetMemoryStatus: mocks.getStatus,
    SaveMemoryEntry: mocks.saveEntry,
    GetMemoryForgetPreview: mocks.getPreview,
    ForgetMemoryEntry: mocks.forgetEntry,
    SearchMemory: mocks.search,
    SyncMemoryIndex: mocks.sync,
    ListMemoryVersions: mocks.listVersions,
    GetMemoryEvidenceReadable: mocks.getEvidence,
    GetEntryUses: mocks.getUses,
    GetCommunicationDraftExamples: mocks.getExamples,
    GetAboutYou: mocks.getAbout,
    GetAboutYouPortrait: mocks.getPortrait,
    ConfirmMemoryEntry: mocks.confirm,
    UndoConfirmMemoryEntry: mocks.undoConfirm,
    NotQuiteMemoryEntry: mocks.notQuite,
    UndoNotQuiteMemoryEntry: mocks.undoNotQuite,
    UndoMemoryWording: mocks.undoWording,
    GetMemoryVersion: mocks.getVersion,
    TranslateMemoryEntryToCurrentLanguage: vi.fn(),
    TranslateMemoryVersionToCurrentLanguage: vi.fn(),
    StartTranslateAllCurrentMemoryEntries: vi.fn(),
    ReassignMemoryAttribution: vi.fn(),
    RejectMemoryAttributionWithPreview: mocks.rejectWithPreview,
    // The Goals group reads these; with no answer staged, the list is empty.
    ListGoals: (...args: unknown[]) => mocks.listGoals(...args) ?? Promise.resolve({ tracking: [], done: [], held: [] }),
    GetGoal: (...args: unknown[]) => mocks.getGoal(...args),
    MoveGoal: (...args: unknown[]) => mocks.moveGoal(...args),
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, callback: (value: unknown) => void) => {
        const listeners = mocks.events.get(name) ?? new Set<(value: unknown) => void>();
        listeners.add(callback);
        mocks.events.set(name, listeners);
        return () => { listeners.delete(callback); };
    },
}));

async function emitEvent(name: string, payload?: unknown) {
    await act(async () => {
        for (const handler of [...(mocks.events.get(name) ?? [])]) await handler(payload);
    });
}
vi.mock('@/lib/notify', () => ({ notifyToast: mocks.notify }));
vi.mock('@/components/ai-memory-import-dialog', () => ({ AIMemoryImportDialog: ({ open }: { open: boolean }) => (open ? <div>Import dialog open</div> : null) }));
vi.mock('@/components/memory-dream-dialog', () => ({ MemoryDreamDialog: ({ open }: { open: boolean }) => (open ? <div>Dream dialog open</div> : null) }));
vi.mock('./contacts/principal-picker', () => ({ PrincipalPicker: () => null }));
vi.mock('@/components/entry-content', async (importOriginal) => {
    const actual = await importOriginal<typeof import('@/components/entry-content')>();
    return {
        ...actual,
        EntryDetail: ({ content }: { content: string }) => <pre>{actual.splitHeaderAndBody(content).body}</pre>,
        EntryMetadataList: () => null,
    };
});

// The page reads through the shared query cache now; each test starts cold.
beforeEach(() => {
    resetPageQueries();
});

const entry = {
    id: 'writing-style',
    title: 'Writing style',
    content: '# Writing style\n- **Dimension**: user_insight\n- **Source**: activity\n\nI prefer detailed replies.\n',
    dimension: 'user_insight',
    source: 'activity',
    modTime: Date.now(),
    updatedAt: Date.now(),
    sizeBytes: 120,
    hasHistory: false,
    activitySessionIds: [],
    apps: [],
};

function preview(overrides: Partial<MemoryForgetPreview> = {}): MemoryForgetPreview {
    return {
        memoryId: entry.id,
        title: entry.title,
        items: [
            { kind: 'memory', id: entry.id, title: entry.title },
            { kind: 'memory_archive', id: 'archive-1', title: 'Earlier writing style' },
            { kind: 'self_dream', id: 'self-1', title: 'Detailed communication preference' },
            { kind: 'purpose_map', id: 'purpose-1', title: 'Prepare a detailed weekly report' },
            { kind: 'work_profile', id: 'profile-1', title: 'Writing habits snapshot' },
        ],
        embeddingCount: 3,
        historyCount: 2,
        retentionDays: 30,
        confirmationToken: 'reviewed-scope-v1',
        ...overrides,
    };
}

type Entry = Omit<typeof entry, 'apps' | 'activitySessionIds'> & { apps: string[]; activitySessionIds: string[] };

/** The About you read for these entries; nothing new to check by default. */
function aboutFor(entries: Entry[], overrides: { newThisWeek?: string[]; status?: 'confirmed' | 'learned_today' | 'inferred' | 'not_quite'; portraitCandidate?: boolean; confirmedAt?: number; notQuiteAt?: number; confidence?: number; firstLearnedAt?: number; area?: string; sentMessageCount?: number; evidenceFirstAt?: number; evidenceLastAt?: number } = {}) {
    return {
        beliefs: entries.map((row) => ({
            id: row.id,
            title: row.title,
            area: overrides.area,
            status: overrides.status ?? 'inferred',
            confidence: overrides.confidence ?? 2,
            firstLearnedAt: overrides.firstLearnedAt ?? 0,
            updatedAt: row.updatedAt,
            sessionCount: 2,
            dayCount: 1,
            confirmedAt: overrides.confirmedAt ?? 0,
            notQuiteAt: overrides.notQuiteAt ?? 0,
            portraitCandidate: overrides.portraitCandidate ?? false,
            sentMessageCount: overrides.sentMessageCount,
            evidenceFirstAt: overrides.evidenceFirstAt,
            evidenceLastAt: overrides.evidenceLastAt,
        })),
        newThisWeek: overrides.newThisWeek ?? [],
        totals: { beliefs: entries.length, confirmed: overrides.status === 'confirmed' ? entries.length : 0 },
        storage: { files: entries.length, bytes: 120 * entries.length, chunks: 3, vectorReady: true },
    };
}

function learningPortrait() {
    return { state: 'learning', generatedAt: 0, beliefCount: 0, writing: false, rewriting: false, forgotten: false, assistant: '', progress: { beliefs: 2, beliefsNeeded: 20, days: 1, daysNeeded: 5, groups: 1, groupsNeeded: 3, moreDays: 4, met: false } };
}

function readyPortrait(parts: { text: string; memoryIds?: string[]; confirmed?: boolean }[]) {
    return {
        state: 'ready', name: ['blueprint', 'builder'], nameAreas: [['work'], ['communication']], sides: [0.2, 0.3, 0.2, 0.3],
        sentences: [{ role: 'connect', changed: false, parts: parts.map((part) => ({ memoryIds: [], confirmed: false, ...part })) }],
        workWithMe: [], beliefCount: 2, generatedAt: Date.now(), writing: false, rewriting: false, forgotten: false, assistant: 'claude-code',
    };
}

function resetDefaults(entries: Entry[]) {
    mocks.getEntries.mockResolvedValue(entries);
    mocks.getAbout.mockImplementation(async () => aboutFor(entries));
    mocks.getPortrait.mockResolvedValue(learningPortrait());
    mocks.getUses.mockResolvedValue({ total: 0, lastAt: 0, surfaces: [] });
    mocks.getExamples.mockResolvedValue([]);
    mocks.getEvidence.mockResolvedValue([]);
    mocks.listVersions.mockResolvedValue([]);
    mocks.search.mockResolvedValue([]);
    mocks.getStatus.mockResolvedValue({ files: entries.length, chunks: 3, vectorReady: true });
}

async function openPageMenu() {
    fireEvent.keyDown(await screen.findByRole('button', { name: 'More About you actions' }), { key: 'Enter' });
}

async function openDetailMenu(name = 'More memory actions') {
    fireEvent.keyDown(screen.getByRole('button', { name }), { key: 'Enter' });
}

async function renderSelected() {
    render(<MemoryPage initialSelectedId={entry.id} />);
    await screen.findByRole('heading', { name: entry.title });
}

async function openForget() {
    fireEvent.click(screen.getByRole('button', { name: 'Forget this' }));
    const dialog = screen.getByRole('dialog', { name: 'Forget this memory?' });
    await within(dialog).findByText('Detailed communication preference');
    return dialog;
}

describe('MemoryPage editing and forgetting', () => {
    beforeEach(async () => {
        vi.resetAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage('en');
        resetDefaults([entry]);
        mocks.saveEntry.mockImplementation(async (id: string, content: string) => ({ ...entry, id, content, source: 'user_edit' }));
        mocks.getPreview.mockResolvedValue(preview());
        mocks.forgetEntry.mockResolvedValue(undefined);
        mocks.rejectWithPreview.mockResolvedValue(undefined);
    });

    it.each([
        { records: 1, embeddings: 0, versions: 0, expected: 'Removed 1 record, 0 search embeddings and 0 saved versions.' },
        { records: 1, embeddings: 1, versions: 1, expected: 'Removed 1 record, 1 search embedding and 1 saved version.' },
        { records: 2, embeddings: 2, versions: 2, expected: 'Removed 2 records, 2 search embeddings and 2 saved versions.' },
    ])('pluralizes forgotten counts: $expected', async ({ records, embeddings, versions, expected }) => {
        mocks.getPreview.mockResolvedValue(preview({
            items: Array.from({ length: records }, (_, index) => ({ kind: 'memory' as const, id: `memory-${index}`, title: `Memory ${index}` })),
            embeddingCount: embeddings,
            historyCount: versions,
        }));
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: 'Forget this' }));
        const dialog = screen.getByRole('dialog');
        await within(dialog).findByText('Memory 0');
        fireEvent.click(within(dialog).getByRole('button', { name: 'Forget this' }));
        await waitFor(() => expect(mocks.notify).toHaveBeenCalledWith('Memory forgotten', expect.stringContaining(expected), 'success'));
    });

    it.each([
        ['en', 'User edited'],
        ['zh-CN', '用户编辑'],
    ])('localizes the user-edit source in Technical details, never in the belief list, in %s', async (language, expected) => {
        await i18n.changeLanguage(language);
        mocks.getEntries.mockResolvedValue([{ ...entry, source: 'user_edit' }]);
        render(<MemoryPage initialSelectedId={entry.id} />);
        const detail = await screen.findByRole('complementary', { name: language === 'en' ? 'Belief detail' : '认识详情' });
        expect(screen.queryByText(expected)).not.toBeInTheDocument();
        fireEvent.click(within(detail).getByRole('button', { name: language === 'en' ? 'Technical details' : '技术细节' }));
        expect(within(detail).getByText(expected)).toBeInTheDocument();
        expect(screen.queryByText('User_edit')).not.toBeInTheDocument();
    });

    it('removes obsolete delete-dialog copy from both memory locales', () => {
        for (const locale of [en, zh]) {
            expect(locale.sidebar).not.toHaveProperty('deleteFile');
            expect(locale).not.toHaveProperty('deleteDialog');
        }
    });

    it.each([
        ['en', 'Forget this', 'Analysis'],
        ['zh-CN', '忘记这条', '分析记录'],
    ])('uses a readable localized label for an untitled derived record in %s', async (language, action, label) => {
        await i18n.changeLanguage(language);
        mocks.getPreview.mockResolvedValue(preview({ items: [{ kind: 'self_dream', id: 'opaque-derived-record-id', title: '', recordType: 'analysis' }] }));
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: action }));
        const dialog = screen.getByRole('dialog');
        expect(await within(dialog).findByText(label)).toBeInTheDocument();
        expect(within(dialog).queryByText('opaque-derived-record-id')).not.toBeInTheDocument();
    });

    it.each([
        ['en', 'Mark "Writing style" as wrongly attributed', 'Wrong subject?', 'This is actually about another person, not me.', 'Review removal', 'Correct this attribution?', 'Confirm correction', 'Attribution corrected'],
        ['zh-CN', '标记「Writing style」为主体归属错误', '搞错主体了？', '这条其实是另一个人的事,不是我的。', '查看移除范围', '更正这条记忆的归属？', '确认更正', '已更正记忆归属'],
    ])('uses correction-specific preview and confirmation after rejecting attribution in %s', async (language, rejectAction, rejectTitle, reason, reviewAction, correctionTitle, confirmAction, successTitle) => {
        await i18n.changeLanguage(language);
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: rejectAction }));
        const rejectDialog = screen.getByRole('dialog', { name: rejectTitle });
        fireEvent.click(within(rejectDialog).getByRole('radio', { name: reason }));
        fireEvent.click(within(rejectDialog).getByRole('button', { name: reviewAction }));
        const correctionDialog = await screen.findByRole('dialog', { name: correctionTitle });
        await within(correctionDialog).findByText('Detailed communication preference');
        expect(within(correctionDialog).getByText('Prepare a detailed weekly report')).toBeInTheDocument();
        expect(mocks.rejectWithPreview).not.toHaveBeenCalled();
        expect(mocks.forgetEntry).not.toHaveBeenCalled();
        fireEvent.click(within(correctionDialog).getByRole('button', { name: confirmAction }));
        await waitFor(() => expect(mocks.rejectWithPreview).toHaveBeenCalledWith(entry.id, reason, 'reviewed-scope-v1'));
        expect(mocks.forgetEntry).not.toHaveBeenCalled();
        expect(mocks.notify).toHaveBeenCalledWith(successTitle, expect.any(String), 'success');
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    });

    it.each([
        ['en', 'Forget this', 'Forgetting works offline using local fact hashes. Optional AI matching can also recognize reworded facts when available.'],
        ['zh-CN', '忘记这条', '忘记操作可离线完成，使用本地事实哈希进行匹配。AI 匹配可用时，还能帮助识别换一种说法的事实。'],
    ])('explains offline forgetting and optional semantic matching in %s', async (language, action, explanation) => {
        await i18n.changeLanguage(language);
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: action }));
        const dialog = screen.getByRole('dialog');
        await within(dialog).findByText('Detailed communication preference');
        expect(within(dialog).getByText(explanation)).toBeInTheDocument();
    });

    it.each([
        ['en', 'Forget this', 'Shared learning analyses and their supporting records are removed when listed. Memory files are removed only when listed as memory records.'],
        ['zh-CN', '忘记这条', '列表中的共享学习分析及其支持记录会被移除。记忆文件仅在列为记忆记录时才会被移除。'],
    ])('explains the shared-learning removal scope in %s', async (language, action, explanation) => {
        await i18n.changeLanguage(language);
        mocks.getPreview.mockResolvedValue(preview({ items: [{ kind: 'shadow_learning', id: 'learning-run-1', title: 'Writing preference analysis', recordType: 'analysis' }] }));
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: action }));
        const dialog = screen.getByRole('dialog');
        await within(dialog).findByText('Writing preference analysis');
        expect(within(dialog).getByText(explanation)).toBeInTheDocument();
    });

    it.each([
        ['en', 'Forget this', 'Saved memory version'],
        ['zh-CN', '忘记这条', '已保存的记忆版本'],
    ])('labels an independently removed memory history version in %s', async (language, action, label) => {
        await i18n.changeLanguage(language);
        mocks.getPreview.mockResolvedValue(preview({ items: [{ kind: 'memory_history', id: 'writing-version-1', title: 'Previous writing preference' }] }));
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: action }));
        const dialog = screen.getByRole('dialog');
        await within(dialog).findByText('Previous writing preference');
        expect(within(dialog).getByText(label)).toBeInTheDocument();
    });

    it('edits the full plain text within its fixed area group and saves it', async () => {
        await renderSelected();
        expect(screen.getByRole('button', { name: /^Other\s*1$/ })).toBeInTheDocument();
        expect(screen.getAllByText('A preference').some((node) => node.tagName === 'SPAN')).toBe(true);
        fireEvent.click(screen.getByRole('button', { name: 'Edit wording' }));
        const editor = screen.getByRole('textbox', { name: 'Memory point' });
        expect(editor).toHaveValue('I prefer detailed replies.');
        expect(screen.getByText('Your changes are saved as user edits and take priority over observed memories.')).toBeInTheDocument();
        const edited = entry.content.replace('detailed', 'concise');
        fireEvent.change(editor, { target: { value: 'I prefer concise replies.' } });
        mocks.getEntries.mockResolvedValue([{ ...entry, content: edited, source: 'user_edit' }]);
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(mocks.saveEntry).toHaveBeenCalledWith(entry.id, edited));
        await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Memory point' })).not.toBeInTheDocument());
        expect(screen.getByRole('button', { name: 'I prefer concise replies.' })).toBeInTheDocument();
        expect(mocks.notify).toHaveBeenCalledWith('Memory saved', expect.stringContaining('user edit'), 'success');
        expect(screen.getByRole('button', { name: /^Other\s*1$/ })).toBeInTheDocument();
    });

    it('cancels an edit without saving', async () => {
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: 'Edit wording' }));
        fireEvent.change(screen.getByRole('textbox', { name: 'Memory point' }), { target: { value: 'Discard this draft' } });
        fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
        expect(mocks.saveEntry).not.toHaveBeenCalled();
        expect(screen.queryByRole('textbox', { name: 'Memory point' })).not.toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'I prefer detailed replies.' })).toBeInTheDocument();
    });

    it('clears the active draft when its memory is forgotten and starts the next edit as a new record', async () => {
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: 'Edit wording' }));
        fireEvent.change(screen.getByRole('textbox', { name: 'Memory point' }), { target: { value: 'Draft that must be discarded' } });
        fireEvent.click(screen.getByRole('button', { name: 'Forget "Writing style"' }));
        const dialog = screen.getByRole('dialog');
        await within(dialog).findByText('Detailed communication preference');
        mocks.getEntries.mockResolvedValue([]);
        fireEvent.click(within(dialog).getByRole('button', { name: 'Forget this' }));
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        expect(screen.queryByRole('textbox', { name: 'Memory point' })).not.toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'Save' })).not.toBeInTheDocument();
        expect(mocks.saveEntry).not.toHaveBeenCalled();
        await openPageMenu();
        fireEvent.click(await screen.findByRole('menuitem', { name: 'New memory file' }));
        const newDialog = await screen.findByRole('dialog', { name: 'New Memory' });
        expect(within(newDialog).getByRole('textbox', { name: 'Memory text' })).toHaveValue(en.newTemplate);
        fireEvent.change(within(newDialog).getByRole('textbox', { name: 'Memory text' }), { target: { value: '# New fact\nA different preference.' } });
        fireEvent.click(within(newDialog).getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(mocks.saveEntry).toHaveBeenCalledWith('', '# New fact\nA different preference.'));
    });

    it('keeps an unsaved edit after failure and allows a retry', async () => {
        mocks.saveEntry.mockRejectedValueOnce(new Error('disk full'));
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: 'Edit wording' }));
        const editor = screen.getByRole('textbox', { name: 'Memory point' });
        fireEvent.change(editor, { target: { value: 'Keep replies concise.' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(mocks.notify).toHaveBeenCalledWith('Memory could not be saved', expect.any(String), 'error'));
        expect(editor).toHaveValue('Keep replies concise.');
        expect(screen.getByRole('button', { name: 'Save' })).toBeEnabled();
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(mocks.saveEntry).toHaveBeenCalledTimes(2));
        await waitFor(() => expect(screen.queryByRole('textbox', { name: 'Memory point' })).not.toBeInTheDocument());
    });

    it('previews every removed derivative, stored copies and scope before confirming with its token', async () => {
        await renderSelected();
        const dialog = await openForget();
        expect(mocks.getPreview).toHaveBeenCalledWith(entry.id);
        expect(mocks.forgetEntry).not.toHaveBeenCalled();
        expect(within(dialog).getByText('Writing style')).toBeInTheDocument();
        expect(within(dialog).getByText('Archived memory')).toBeInTheDocument();
        expect(within(dialog).getByText('Earlier writing style')).toBeInTheDocument();
        expect(within(dialog).getByText('Self Dream')).toBeInTheDocument();
        expect(within(dialog).getByText('Purpose Map')).toBeInTheDocument();
        expect(within(dialog).getByText('Work profile snapshot')).toBeInTheDocument();
        expect(within(dialog).getByText('Prepare a detailed weekly report')).toBeInTheDocument();
        expect(within(dialog).getByText('Stored search embeddings: 3. Saved versions: 2.')).toBeInTheDocument();
        expect(within(dialog).getByText('For 30 days, matching re-observations are blocked from memory, Dream and chat context.')).toBeInTheDocument();
        expect(within(dialog).getByText('Source activities and conversations, unrelated logs and external backups are not deleted.')).toBeInTheDocument();
        expect(within(dialog).queryByText('Shared learning analyses and their supporting records are removed when listed. Memory files are removed only when listed as memory records.')).not.toBeInTheDocument();
        mocks.getEntries.mockResolvedValue([]);
        fireEvent.click(within(dialog).getByRole('button', { name: 'Forget this' }));
        await waitFor(() => expect(mocks.forgetEntry).toHaveBeenCalledWith(entry.id, 'reviewed-scope-v1'));
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
        expect(screen.queryByRole('heading', { name: entry.title })).not.toBeInTheDocument();
        expect(mocks.notify).toHaveBeenCalledWith('Memory forgotten', expect.stringContaining('Removed 5 records, 3 search embeddings and 2 saved versions'), 'success');
    });

    it('opens the same preview from a grouped row and cancelling leaves it untouched', async () => {
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: 'Forget "Writing style"' }));
        const dialog = screen.getByRole('dialog');
        await within(dialog).findByText('Detailed communication preference');
        fireEvent.click(within(dialog).getByRole('button', { name: 'Cancel' }));
        expect(mocks.forgetEntry).not.toHaveBeenCalled();
        expect(screen.queryByRole('dialog')).not.toBeInTheDocument();
        expect(screen.getByRole('heading', { name: entry.title })).toBeInTheDocument();
    });

    it('cannot forget before the preview has loaded', async () => {
        let resolvePreview!: (value: MemoryForgetPreview) => void;
        mocks.getPreview.mockReturnValue(new Promise((resolve) => { resolvePreview = resolve; }));
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: 'Forget this' }));
        const dialog = screen.getByRole('dialog');
        const confirm = within(dialog).getByRole('button', { name: 'Forget this' });
        expect(confirm).toBeDisabled();
        expect(within(dialog).getByRole('status')).toHaveTextContent('Checking this memory');
        fireEvent.click(confirm);
        expect(mocks.forgetEntry).not.toHaveBeenCalled();
        await act(async () => resolvePreview(preview()));
        expect(confirm).toBeEnabled();
    });

    it.each([
        ['en', 'Forget this', 'Nothing has been forgotten', 'Make sure your selected AI model is available, then retry the preview.', 'Refresh preview'],
        ['zh-CN', '忘记这条', '尚未忘记任何内容', '请确认所选 AI 模型可用，然后重新加载预览。', '刷新预览'],
    ])('recovers from a storage preview failure without a model-availability hint in %s', async (language, action, error, hint, refresh) => {
        await i18n.changeLanguage(language);
        mocks.getPreview.mockRejectedValueOnce(new Error('storage unavailable'));
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: action }));
        const dialog = screen.getByRole('dialog');
        expect(await within(dialog).findByRole('alert')).toHaveTextContent(error);
        expect(within(dialog).queryByText(hint)).not.toBeInTheDocument();
        expect(within(dialog).queryByRole('button', { name: action })).not.toBeInTheDocument();
        expect(mocks.forgetEntry).not.toHaveBeenCalled();
        fireEvent.click(within(dialog).getByRole('button', { name: refresh }));
        await within(dialog).findByText('Detailed communication preference');
        expect(within(dialog).getByRole('button', { name: action })).toBeEnabled();
    });

    it('requires reviewing a fresh scope after forget fails or its token is stale', async () => {
        mocks.forgetEntry.mockRejectedValueOnce(new Error('scope changed'));
        await renderSelected();
        const dialog = await openForget();
        fireEvent.click(within(dialog).getByRole('button', { name: 'Forget this' }));
        expect(await within(dialog).findByRole('alert')).toHaveTextContent('Refresh the preview');
        expect(within(dialog).queryByText('Detailed communication preference')).not.toBeInTheDocument();
        expect(within(dialog).queryByRole('button', { name: 'Forget this' })).not.toBeInTheDocument();
        mocks.getPreview.mockResolvedValue(preview({ confirmationToken: 'reviewed-scope-v2', historyCount: 4 }));
        fireEvent.click(within(dialog).getByRole('button', { name: 'Refresh preview' }));
        await within(dialog).findByText('Stored search embeddings: 3. Saved versions: 4.');
        expect(mocks.forgetEntry).toHaveBeenCalledTimes(1);
        fireEvent.click(within(dialog).getByRole('button', { name: 'Forget this' }));
        await waitFor(() => expect(mocks.forgetEntry).toHaveBeenLastCalledWith(entry.id, 'reviewed-scope-v2'));
        await waitFor(() => expect(screen.queryByRole('dialog')).not.toBeInTheDocument());
    });

    it('spaces the composed Chinese success counts correctly', async () => {
        await i18n.changeLanguage('zh-CN');
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: '忘记这条' }));
        const dialog = screen.getByRole('dialog');
        await within(dialog).findByText('Detailed communication preference');
        fireEvent.click(within(dialog).getByRole('button', { name: '忘记这条' }));
        await waitFor(() => expect(mocks.notify).toHaveBeenCalledWith('已忘记该记忆', expect.stringContaining('已移除 5 条记录、3 个搜索向量和 2 个历史版本。'), 'success'));
    });

    it('renders the editing and forget controls in Chinese', async () => {
        await i18n.changeLanguage('zh-CN');
        await renderSelected();
        fireEvent.click(screen.getByRole('button', { name: '修改措辞' }));
        expect(screen.getByRole('textbox', { name: '记忆要点' })).toHaveValue('I prefer detailed replies.');
        expect(screen.getByText('你的修改会保存为用户编辑，优先于观察到的记忆。')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: '取消' }));
        fireEvent.click(screen.getByRole('button', { name: '忘记这条' }));
        const dialog = screen.getByRole('dialog', { name: '忘记这条记忆？' });
        await within(dialog).findByText('自我梦境');
        expect(within(dialog).getByText('已归档的记忆')).toBeInTheDocument();
        expect(within(dialog).getByText('目标图谱')).toBeInTheDocument();
        expect(within(dialog).getByText('工作画像快照')).toBeInTheDocument();
        expect(within(dialog).getByText('原始活动和对话、无关日志以及外部备份不会被删除。')).toBeInTheDocument();
        expect(within(dialog).getByText('忘记操作可离线完成，使用本地事实哈希进行匹配。AI 匹配可用时，还能帮助识别换一种说法的事实。')).toBeInTheDocument();
        expect(within(dialog).getByRole('button', { name: '忘记这条' })).toBeEnabled();
    });
});


describe('MemoryPage memory-first browsing', () => {
    const id = 'b4c8e1a9-4f72-4b18-a19d-6f143d66e98b';
    const untitled = { ...entry, id, title: id, content: '---\nuuid: ' + id + '\ntype: memory\ndimension: user_insight\n---\nPrefers concise replies.' };

    beforeEach(async () => {
        vi.resetAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage('en');
        resetDefaults([untitled]);
        mocks.getPreview.mockResolvedValue(preview({ memoryId: id, items: [{ kind: 'memory', id, title: id }] }));
    });

    it.each([
        ['en', 'More memory actions', 'Copy memory ID'],
        ['zh-CN', '更多记忆操作', '复制记忆 ID'],
    ])('keeps the ID entirely out of the DOM and copies it only through the menu in %s', async (language, menu, copy) => {
        await i18n.changeLanguage(language);
        const writeText = vi.fn().mockResolvedValue(undefined);
        Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText } });
        const { container } = render(<MemoryPage initialSelectedId={id} />);
        await screen.findByRole('heading', { name: 'Prefers concise replies.' });
        expect(container.innerHTML).not.toContain(id);
        expect(screen.queryByRole('button', { name: copy })).not.toBeInTheDocument();
        fireEvent.keyDown(screen.getByRole('button', { name: menu }), { key: 'Enter' });
        const copyAction = await screen.findByRole('menuitem', { name: copy });
        expect(document.body.innerHTML).not.toContain(id);
        fireEvent.click(copyAction);
        expect(writeText).toHaveBeenCalledWith(id);
    });

    it.each([
        ['en', 'Search memory text...', 'Filter by kind', 'Edit "Prefers concise replies."', 'Forget "Prefers concise replies."'],
        ['zh-CN', '搜索记忆内容……', '按类型筛选', '编辑「Prefers concise replies.」', '忘记“Prefers concise replies.”'],
    ])('combines text search and kind filtering with direct edit and forget actions in %s', async (language, search, filter, edit, forget) => {
        await i18n.changeLanguage(language);
        const topic = { ...entry, id: 'topic-id', title: 'Another topic', dimension: 'topic_highlight', content: '# Another topic\nUses concise project plans.' };
        resetDefaults([untitled, topic]);
        // The index can be unavailable; local body matches still work.
        mocks.search.mockRejectedValue(new Error('offline'));
        render(<MemoryPage initialSelectedId={id} />);
        await screen.findByRole('heading', { name: 'Prefers concise replies.' });
        fireEvent.change(screen.getByRole('textbox', { name: search }), { target: { value: 'CONCISE' } });
        fireEvent.change(screen.getByRole('combobox', { name: filter }), { target: { value: 'preference' } });
        await waitFor(() => expect(mocks.search).toHaveBeenCalledWith('CONCISE', 20));
        expect(screen.getByRole('button', { name: /Prefers concise replies\./, pressed: true })).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: /Prefers concise project plans\./ })).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: edit }));
        expect(screen.getByRole('textbox', { name: language === 'en' ? 'Memory point' : '记忆要点' })).toHaveValue('Prefers concise replies.');
        fireEvent.click(screen.getByRole('button', { name: forget }));
        await waitFor(() => expect(mocks.getPreview).toHaveBeenCalledWith(id));
        const dialog = screen.getByRole('dialog');
        expect(await within(dialog).findByText('Prefers concise replies.')).toBeInTheDocument();
        expect(dialog).not.toHaveTextContent(id);
    });

    it('filters the belief list by text and kind', async () => {
        const topic = { ...entry, id: 'topic-id', title: 'Project plans', dimension: 'topic_highlight', content: '# Project plans\nUses concise project plans.' };
        resetDefaults([untitled, topic]);
        render(<MemoryPage />);
        expect(await screen.findAllByRole('button', { name: /Project plans.*Uses concise project plans/, pressed: false })).toHaveLength(1);
        fireEvent.change(screen.getByRole('textbox', { name: 'Search memory text...' }), { target: { value: 'CONCISE' } });
        fireEvent.change(screen.getByRole('combobox', { name: 'Filter by kind' }), { target: { value: 'preference' } });
        expect(screen.getAllByRole('button', { name: /Prefers concise replies\./, pressed: false })).toHaveLength(1);
        expect(screen.queryByRole('button', { name: /Project plans.*Uses concise project plans/, pressed: false })).not.toBeInTheDocument();
    });

    it('deduplicates indexed chunks into the same memory rows without exposing paths and ignores stale searches', async () => {
        let resolveFirst!: (results: unknown[]) => void;
        mocks.search.mockReturnValueOnce(new Promise((resolve) => { resolveFirst = resolve; }));
        mocks.search.mockResolvedValueOnce([{ path: id + '.md', startLine: 10, endLine: 12 }, { path: id + '.md', startLine: 20, endLine: 22 }]);
        render(<MemoryPage initialSelectedId={id} />);
        await screen.findByRole('heading', { name: 'Prefers concise replies.' });
        const search = screen.getByRole('textbox', { name: 'Search memory text...' });
        fireEvent.change(search, { target: { value: 'first query' } });
        await waitFor(() => expect(mocks.search).toHaveBeenCalledWith('first query', 20));
        fireEvent.change(search, { target: { value: 'communication style' } });
        await waitFor(() => expect(mocks.search).toHaveBeenCalledWith('communication style', 20));
        expect(await screen.findByRole('button', { name: /Prefers concise replies\./, pressed: true })).toBeInTheDocument();
        await act(async () => resolveFirst([]));
        expect(screen.getAllByRole('button', { name: /Prefers concise replies\./, pressed: true })).toHaveLength(1);
        expect(screen.queryByText(id + '.md')).not.toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'Edit "Prefers concise replies."' })).toBeInTheDocument();
    });
});

describe('MemoryPage human-first detail', () => {
    const detailed = { ...entry, content: entry.content + '- I use examples when helpful. I keep meetings short.\n', apps: ['ChatGPT', 'loginwindow', 'Safari', 'chatgpt', 'Isoform', 'Terminal'], activitySessionIds: ['hidden-session'] };
    const yesterday = Date.now() - 86400000;
    beforeEach(async () => {
        vi.resetAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage('en');
        resetDefaults([detailed]);
        mocks.getStatus.mockResolvedValue({ files: 1, chunks: 0, vectorReady: false });
        mocks.getEvidence.mockResolvedValue([{ at: yesterday, durationMs: 2 * 3600000, appName: 'ChatGPT', text: 'Prepared a weekly update.', activitySessionId: 'hidden-session' }]);
        mocks.saveEntry.mockImplementation(async (id: string, content: string) => ({ ...detailed, id, content, source: 'user_edit' }));
    });

    it.each([
        ['en', 'Memory points', 'Why Isoform believes this', 'Where Isoform uses it', 'Technical details', 'Not right', 'Seen yesterday in ChatGPT · 2 hours'],
        ['zh-CN', '记忆要点', 'Isoform 为什么这样认为', 'Isoform 在哪里用到它', '技术细节', '不准确', '昨天在 ChatGPT 中出现 · 2 小时'],
    ])('orders points, provenance, readable evidence, uses and closed technical details in %s', async (language, pointsLabel, why, uses, technical, reject, seen) => {
        await i18n.changeLanguage(language);
        const { container } = render(<MemoryPage initialSelectedId={entry.id} />);
        await screen.findByRole('heading', { name: entry.title });
        const points = screen.getByRole('region', { name: pointsLabel });
        const provenance = screen.getByTestId('memory-provenance');
        const whyHeading = screen.getByRole('heading', { name: why });
        const usesHeading = screen.getByRole('heading', { name: uses });
        const technicalButton = screen.getAllByRole('button', { name: technical }).at(-1)!;
        for (const [before, after] of [[points, provenance], [provenance, whyHeading], [whyHeading, usesHeading], [usesHeading, technicalButton]]) {
            expect(before.compareDocumentPosition(after) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        }
        expect(technicalButton).toHaveAttribute('aria-expanded', 'false');
        // Evidence is the moment title Isoform wrote, when and in which app.
        expect(await screen.findByText('Prepared a weekly update.')).toBeVisible();
        expect(screen.getByText(seen)).toBeInTheDocument();
        expect(container.innerHTML).not.toContain('hidden-session');
        expect(provenance).toHaveTextContent('ChatGPT');
        expect(provenance).not.toHaveTextContent('loginwindow');
        expect(provenance).not.toHaveTextContent('Terminal');
        expect(within(points).getAllByRole('button', { name: /^I / })).toHaveLength(3);
        fireEvent.click(screen.getByRole('button', { name: reject }));
        expect(await screen.findByRole('dialog')).toBeInTheDocument();
    });

    it('opens the newest moment in Activity without putting its ID on the page', async () => {
        const onOpen = vi.fn();
        const { container } = render(<MemoryPage onOpenActivitySession={onOpen} initialSelectedId={entry.id} />);
        fireEvent.click(await screen.findByRole('button', { name: 'See these moments in Activity' }));
        expect(onOpen).toHaveBeenCalledWith('hidden-session');
        expect(container.innerHTML).not.toContain('hidden-session');
    });

    it('says where Isoform used the belief, in words', async () => {
        mocks.getUses.mockResolvedValue({ total: 4, lastAt: yesterday, surfaces: [
            { surface: 'brief', count: 1, lastAt: Date.now() - 3 * 86400000 },
            { surface: 'chat', count: 3, lastAt: yesterday },
        ] });
        render(<MemoryPage initialSelectedId={entry.id} />);
        const items = await screen.findAllByRole('listitem');
        expect(items.map((item) => item.textContent)).toEqual([
            'Used 3 times in chat answers · last yesterday',
            'Used once in your briefs · last 3 days ago',
        ]);
        expect(mocks.getUses).toHaveBeenCalledWith('memory', entry.id);
    });

    it('says when Isoform has not used a belief, and offers a retry when evidence fails', async () => {
        mocks.getEvidence.mockRejectedValueOnce(new Error('store'));
        render(<MemoryPage initialSelectedId={entry.id} />);
        expect(await screen.findByText('Isoform has not used this yet.')).toBeInTheDocument();
        const alert = await screen.findByRole('alert');
        expect(alert).toHaveTextContent('Could not load why Isoform believes this.');
        fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));
        expect(await screen.findByText('Prepared a weekly update.')).toBeInTheDocument();
    });

    it('keeps older provenance relative and handles evidence without app names', async () => {
        mocks.getEntries.mockResolvedValue([{ ...detailed, updatedAt: Date.now() - 4 * 86400000 }]);
        mocks.getEvidence.mockResolvedValue([{ at: Date.now() - 5 * 86400000, durationMs: 0, appName: 'Notes', text: 'A planning note.', activitySessionId: '' }]);
        render(<MemoryPage initialSelectedId={entry.id} />);
        await waitFor(() => expect(screen.getByTestId('memory-provenance')).toHaveTextContent('Learned from your work since 5 days ago'));
        expect(screen.getByTestId('memory-provenance')).toHaveTextContent('Updated 4 days ago.');
        expect(await screen.findByText('A planning note.')).toBeVisible();
        expect(screen.getByText('Seen 5 days ago in Notes')).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'See these moments in Activity' })).not.toBeInTheDocument();
    });

    it('edits just one sentence, preserves metadata and neighboring points, and retains drafts after a failure', async () => {
        render(<MemoryPage initialSelectedId={entry.id} />);
        const point = await screen.findByRole('button', { name: 'I keep meetings short.' });
        fireEvent.click(point);
        const editor = screen.getByRole('textbox', { name: 'Memory point' });
        fireEvent.change(editor, { target: { value: 'I keep meetings focused.' } });
        mocks.saveEntry.mockRejectedValueOnce(new Error('disk full'));
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        expect(await screen.findByRole('alert')).toBeInTheDocument();
        expect(editor).toHaveValue('I keep meetings focused.');
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(mocks.saveEntry).toHaveBeenLastCalledWith(entry.id, detailed.content.replace('I keep meetings short.', 'I keep meetings focused.')));
        await screen.findByRole('button', { name: 'I keep meetings focused.' });
        expect(screen.queryByRole('textbox', { name: 'Memory point' })).not.toBeInTheDocument();
    });

    it('adds a point, cancels without saving and removes only the chosen point', async () => {
        render(<MemoryPage initialSelectedId={entry.id} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Add a point' }));
        fireEvent.change(screen.getByRole('textbox', { name: 'Memory point' }), { target: { value: 'I work on the computer and review on my phone.' } });
        fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
        expect(mocks.saveEntry).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button', { name: 'Add a point' }));
        fireEvent.change(screen.getByRole('textbox', { name: 'Memory point' }), { target: { value: 'I review on my phone.' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await screen.findByRole('button', { name: 'I review on my phone.' });
        fireEvent.click(screen.getByRole('button', { name: 'Forget point: I use examples when helpful.' }));
        await waitFor(() => expect(mocks.saveEntry).toHaveBeenCalledTimes(2));
        const content = mocks.saveEntry.mock.calls[1][1];
        expect(content).not.toContain('I use examples when helpful.');
        expect(content).toContain('I keep meetings short.');
        expect(content).toContain('- **Dimension**: user_insight');
    });

    it('does not carry an inline draft or open sections into another memory', async () => {
        resetDefaults([detailed, { ...entry, id: 'second', title: 'Planning', content: '# Planning\n- I plan on paper.' }]);
        render(<MemoryPage initialSelectedId={entry.id} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Edit wording' }));
        fireEvent.change(screen.getByRole('textbox', { name: 'Memory point' }), { target: { value: 'Unsaved sentence' } });
        const detail = screen.getByRole('complementary', { name: 'Belief detail' });
        fireEvent.click(within(detail).getByRole('button', { name: 'Technical details' }));
        fireEvent.click(screen.getByRole('button', { name: /Planning.*I plan on paper/, pressed: false }));
        expect(await screen.findByRole('heading', { name: 'Planning' })).toBeInTheDocument();
        expect(screen.queryByRole('textbox', { name: 'Memory point' })).not.toBeInTheDocument();
        expect(within(screen.getByRole('complementary', { name: 'Belief detail' })).getByRole('button', { name: 'Technical details' })).toHaveAttribute('aria-expanded', 'false');
    });

    it('closes the detail on Escape, then the dialog, but a menu or an editor keeps its own Escape', async () => {
        const onOpenChange = vi.fn();
        function Host() {
            const escapeRef = useRef<(() => boolean) | null>(null);
            const [open, setOpen] = useState(true);
            return (
                <Dialog open={open} onOpenChange={(next) => { onOpenChange(next); setOpen(next); }}>
                    <RetainedDialogContent open={open} retain={false} onEscapeKeyDown={(e) => { if (escapeRef.current?.()) e.preventDefault(); }}>
                        <DialogTitle>About you</DialogTitle>
                        <MemoryPage initialSelectedId={entry.id} escapeRef={escapeRef} />
                    </RetainedDialogContent>
                </Dialog>
            );
        }
        render(<Host />);
        await screen.findByRole('heading', { name: entry.title });
        // A menu closes alone.
        await openDetailMenu();
        const copy = await screen.findByRole('menuitem', { name: 'Copy memory ID' });
        fireEvent.keyDown(copy, { key: 'Escape' });
        await waitFor(() => expect(screen.queryByRole('menuitem', { name: 'Copy memory ID' })).not.toBeInTheDocument());
        expect(screen.getByRole('heading', { name: entry.title })).toBeInTheDocument();
        // An open point editor cancels itself and keeps the belief open.
        fireEvent.click(screen.getByRole('button', { name: 'Edit wording' }));
        const editor = screen.getByRole('textbox', { name: 'Memory point' });
        editor.focus();
        fireEvent.keyDown(editor, { key: 'Escape' });
        expect(screen.queryByRole('textbox', { name: 'Memory point' })).not.toBeInTheDocument();
        expect(screen.getByRole('heading', { name: entry.title })).toBeInTheDocument();
        // Then the belief closes, then the dialog.
        fireEvent.keyDown(screen.getByRole('heading', { name: 'What Isoform believes about you' }), { key: 'Escape' });
        expect(screen.queryByRole('heading', { name: entry.title })).not.toBeInTheDocument();
        expect(onOpenChange).not.toHaveBeenCalled();
        fireEvent.keyDown(screen.getByRole('heading', { name: 'What Isoform believes about you' }), { key: 'Escape' });
        expect(onOpenChange).toHaveBeenCalledWith(false);
    });

    it('clears a search on Escape before closing the belief, then the dialog', async () => {
        const onOpenChange = vi.fn();
        function Host() {
            const escapeRef = useRef<(() => boolean) | null>(null);
            const [open, setOpen] = useState(true);
            return (
                <Dialog open={open} onOpenChange={(next) => { onOpenChange(next); setOpen(next); }}>
                    <RetainedDialogContent open={open} retain={false} onEscapeKeyDown={(e) => { if (escapeRef.current?.()) e.preventDefault(); }}>
                        <DialogTitle>About you</DialogTitle>
                        <MemoryPage initialSelectedId={entry.id} escapeRef={escapeRef} />
                    </RetainedDialogContent>
                </Dialog>
            );
        }
        render(<Host />);
        await screen.findByRole('heading', { name: entry.title });
        const search = screen.getByRole('textbox', { name: 'Search memory text...' });
        fireEvent.change(search, { target: { value: 'replies' } });
        search.focus();
        fireEvent.keyDown(search, { key: 'Escape' });
        expect(search).toHaveValue('');
        expect(screen.getByRole('heading', { name: entry.title })).toBeInTheDocument();
        fireEvent.keyDown(search, { key: 'Escape' });
        expect(screen.queryByRole('heading', { name: entry.title })).not.toBeInTheDocument();
        expect(onOpenChange).not.toHaveBeenCalled();
        fireEvent.keyDown(search, { key: 'Escape' });
        expect(onOpenChange).toHaveBeenCalledWith(false);
    });

    it('keeps the Activity link when the evidence read fails', async () => {
        mocks.getEvidence.mockRejectedValue(new Error('store'));
        const onOpen = vi.fn();
        render(<MemoryPage onOpenActivitySession={onOpen} initialSelectedId={entry.id} />);
        expect(await screen.findByText('Could not load why Isoform believes this.')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Open where this was learned in Activity' }));
        expect(onOpen).toHaveBeenCalledWith('hidden-session');
    });

    it('offers the linked session in Activity when no readable moment is left', async () => {
        mocks.getEvidence.mockResolvedValue([]);
        const onOpen = vi.fn();
        render(<MemoryPage onOpenActivitySession={onOpen} initialSelectedId={entry.id} />);
        expect(await screen.findByText('No activity evidence is available for this memory.')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Open where this was learned in Activity' }));
        expect(onOpen).toHaveBeenCalledWith('hidden-session');
    });
});

describe('About you overview', () => {
    const second = { ...entry, id: 'second', title: 'Planning', dimension: 'user_insight', content: '# Planning\n- Plans the week on Sunday evening.\n' };
    beforeEach(async () => {
        vi.resetAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage('en');
        resetDefaults([entry, second]);
    });

    it.each([
        ['en', 'What Isoform believes about you', '2 beliefs from 3 weeks of your work. You haven’t confirmed any yet. Saved on this computer. You can correct or forget any of it.'],
        ['zh-CN', 'Isoform 对你的认识', '2 条认识，来自你 3 周的工作。你还没有确认任何一条。保存在这台电脑上。你可以更正或忘记其中任何一条。'],
    ])('leads with what Isoform believes, over how long, in %s', async (language, title, summary) => {
        await i18n.changeLanguage(language);
        mocks.getAbout.mockResolvedValue(aboutFor([entry, second], { firstLearnedAt: Date.now() - 21 * 86400000 }));
        render(<MemoryPage />);
        expect(await screen.findByRole('heading', { level: 1, name: title })).toBeInTheDocument();
        expect(await screen.findByText(summary)).toBeInTheDocument();
    });

    it('groups beliefs by fixed area with a kind label, confidence bar and status', async () => {
        mocks.getAbout.mockResolvedValue({ ...aboutFor([entry, second]), beliefs: [
            { ...aboutFor([entry]).beliefs[0], area: 'work', status: 'confirmed', confidence: 4 },
            { ...aboutFor([second]).beliefs[0], area: 'people', status: 'learned_today', confidence: 1 },
        ] });
        render(<MemoryPage />);
        const preference = await screen.findByRole('button', { name: /Writing style.*I prefer detailed replies/, pressed: false });
        expect(within(preference).getByRole('img', { name: 'Confidence 4 of 4' })).toBeInTheDocument();
        expect(within(preference).getByText('Confirmed')).toBeInTheDocument();
        const learned = screen.getByRole('button', { name: /Planning.*Plans the week/, pressed: false });
        expect(within(learned).getByRole('img', { name: 'Confidence 1 of 4' })).toBeInTheDocument();
        expect(within(learned).getByText('Learned today')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /^Work\s*1$/ })).toBeInTheDocument();
        expect(screen.getByRole('button', { name: /^People\s*1$/ })).toBeInTheDocument();
        expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument();
        expect(screen.getAllByText('A preference').some((node) => node.tagName === 'SPAN')).toBe(true);
        expect(screen.getAllByText('Something I learned about you').some((node) => node.tagName === 'SPAN')).toBe(true);
    });

    // jsdom has no layout, so this guards the layout classes: area groups
    // stack at full width (never grid cells side by side), each group's rows
    // flow into columns, and New this week cards are wide enough for their
    // three buttons on one row.
    it('stacks belief groups at full width and keeps review cards wide enough for one button row', async () => {
        mocks.getAbout.mockImplementation(async () => aboutFor([entry, second], { newThisWeek: [second.id] }));
        render(<MemoryPage />);
        const groups = await screen.findByTestId('belief-groups');
        expect(groups).toHaveClass('flex', 'flex-col');
        expect(groups.className).not.toMatch(/\bgrid\b|grid-template-columns/);
        expect(groups.children).toHaveLength(1);
        for (const rows of screen.getAllByTestId('belief-group-rows')) {
            expect(rows).toHaveClass('grid');
            expect(rows.className).toContain('repeat(auto-fill,minmax(min(100%,340px),1fr))');
        }
        const cards = screen.getByTestId('review-cards');
        const min = Number(cards.className.match(/minmax\(min\(100%,(\d+)px\)/)?.[1]);
        expect(min).toBeGreaterThanOrEqual(300);
    });

    it('keeps the storage numbers folded under Technical details', async () => {
        render(<MemoryPage />);
        const technical = await screen.findByRole('button', { name: 'Technical details' });
        expect(screen.queryByText(/chunks indexed/)).not.toBeInTheDocument();
        fireEvent.click(technical);
        expect(screen.getByText('2 files · 240 B · 3 chunks indexed · vector search active')).toBeInTheDocument();
    });

    it('shows a skeleton while loading and a retry when the beliefs fail to load', async () => {
        // A published sibling read re-runs the load, so the failure persists
        // until the store recovers and the user retries.
        mocks.getEntries.mockRejectedValue(new Error('disk'));
        const { container } = render(<MemoryPage />);
        expect(container.querySelector('[aria-busy="true"]')).not.toBeNull();
        const alert = await screen.findByRole('alert');
        expect(alert).toHaveTextContent('Could not load what Isoform believes about you.');
        mocks.getEntries.mockResolvedValue([entry, second]);
        fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));
        expect(await screen.findByRole('button', { name: /Planning.*Plans the week/, pressed: false })).toBeInTheDocument();
    });

    it('says when confidence and new beliefs cannot be read, keeps the beliefs, and retries', async () => {
        mocks.getAbout.mockRejectedValue(new Error('store'));
        render(<MemoryPage />);
        const alert = await screen.findByRole('alert');
        expect(alert).toHaveTextContent('Could not load what is new this week or how sure Isoform is.');
        expect(screen.getByRole('button', { name: /Planning.*Plans the week/, pressed: false })).toBeInTheDocument();
        mocks.getAbout.mockResolvedValue({ ...aboutFor([entry, second]), beliefs: aboutFor([entry, second], { confidence: 3 }).beliefs });
        fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));
        expect(await screen.findAllByRole('img', { name: 'Confidence 3 of 4' })).toHaveLength(2);
        expect(screen.queryByRole('alert')).not.toBeInTheDocument();
    });

    it('builds no area-of-life switch and no "Only for work"', async () => {
        render(<MemoryPage initialSelectedId={entry.id} />);
        await screen.findByRole('heading', { name: entry.title });
        expect(screen.queryByText(/Area of life/i)).not.toBeInTheDocument();
        expect(screen.queryByText(/Only for work/i)).not.toBeInTheDocument();
    });
});

describe('About you portrait', () => {
    const second = { ...entry, id: 'second', title: 'Planning', content: '# Planning\n- Plans the week on Sunday evening.\n' };
    beforeEach(async () => {
        vi.resetAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage('en');
        resetDefaults([entry, second]);
    });

    it('opens the beliefs behind a phrase; Show in Beliefs opens the belief', async () => {
        mocks.getPortrait.mockResolvedValue(readyPortrait([
            { text: 'You ' },
            { text: 'want detailed replies', memoryIds: ['gone', entry.id] },
            { text: ' and ' },
            { text: 'plan on Sundays', memoryIds: ['second'], confirmed: true },
            { text: '.' },
        ]));
        render(<MemoryPage />);
        expect(await screen.findByRole('heading', { level: 2, name: 'The Blueprint Builder' })).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: 'You' })).not.toBeInTheDocument();
        expect(screen.getByRole('button', { name: 'plan on Sundays' })).toHaveAttribute('data-underline', 'solid');
        expect(screen.getByRole('button', { name: 'want detailed replies' })).toHaveAttribute('data-underline', 'dotted');
        fireEvent.click(screen.getByRole('button', { name: 'plan on Sundays' }));
        const popover = await screen.findByRole('dialog');
        expect(popover).toHaveTextContent('Why Isoform says this');
        expect(popover).toHaveTextContent('“Plans the week on Sunday evening.”');
        fireEvent.click(within(popover).getByRole('button', { name: 'Show in Beliefs' }));
        expect(await screen.findByRole('heading', { level: 2, name: 'Planning' })).toBeInTheDocument();
    });

    it('says the portrait is on its way while Isoform is still learning', async () => {
        render(<MemoryPage />);
        expect(await screen.findByRole('heading', { level: 2, name: 'On its way' })).toBeInTheDocument();
        expect(screen.getByText('2 of 20 beliefs · 1 of 5 days')).toBeInTheDocument();
        expect(screen.queryByText(/Loading/)).not.toBeInTheDocument();
    });

    it('never shows a load error: an unreadable portrait waits for the rewrite', async () => {
        mocks.getPortrait.mockRejectedValueOnce(new Error('portrait_unavailable'));
        render(<MemoryPage />);
        expect(await screen.findByRole('heading', { level: 1 })).toBeInTheDocument();
        await act(async () => {});
        expect(screen.queryByText(/Could not load your portrait/)).not.toBeInTheDocument();
        mocks.getPortrait.mockResolvedValue(learningPortrait());
        await emitEvent('portrait:updated');
        expect(await screen.findByRole('heading', { level: 2, name: 'On its way' })).toBeInTheDocument();
    });
});

describe('About you new this week', () => {
    const fresh = { ...entry, id: 'fresh', title: 'Short calls', apps: ['Zoom', 'loginwindow'], content: '# Short calls\n- Keeps calls short before 11 AM.\n' };
    beforeEach(async () => {
        vi.resetAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage('en');
        resetDefaults([entry, fresh]);
        mocks.getAbout.mockImplementation(async () => aboutFor([entry, fresh], { newThisWeek: ['fresh'] }));
        mocks.confirm.mockResolvedValue({ ...fresh });
        mocks.getPreview.mockResolvedValue(preview({ memoryId: 'fresh', items: [{ kind: 'memory', id: 'fresh', title: 'Short calls' }, { kind: 'self_dream', id: 'd', title: 'Calls' }] }));
        mocks.forgetEntry.mockResolvedValue(undefined);
    });
    afterEach(() => vi.useRealTimers());

    async function card() {
        const section = await screen.findByRole('region', { name: 'New this week' });
        return within(section).getByRole('article', { name: '“Keeps calls short before 11 AM.”' });
    }

    it('lists each new belief with how often it was seen and where, never a system app', async () => {
        render(<MemoryPage />);
        const article = await card();
        expect(article).toHaveTextContent('Seen 2 times · Zoom');
        expect(article).not.toHaveTextContent('loginwindow');
        expect(article).toHaveTextContent('“Keeps calls short before 11 AM.”');
        expect(screen.getByText('1 to check')).toBeInTheDocument();
    });

    it("confirms in one click with That's me: the chip flips, the line says what it means, Undo takes it back", async () => {
        render(<MemoryPage />);
        const article = await card();
        expect(within(article).getByText('Inferred')).toHaveAttribute('data-status', 'inferred');
        mocks.getAbout.mockImplementation(async () => aboutFor([entry, fresh]));
        fireEvent.click(within(article).getByRole('button', { name: 'That’s me' }));
        await waitFor(() => expect(mocks.confirm).toHaveBeenCalledWith('fresh'));
        const line = await within(article).findByText('Confirmed. Isoform treats this as fact now.');
        expect(line).toHaveAttribute('role', 'status');
        expect(line).toHaveFocus();
        expect(within(article).getByText('Confirmed')).toHaveAttribute('data-status', 'confirmed');
        expect(screen.getByText('All checked')).toBeInTheDocument();
        expect(mocks.forgetEntry).not.toHaveBeenCalled();
        // No toast: the answer is the line on the card.
        expect(mocks.notify).not.toHaveBeenCalled();
        // The list chip changes with it.
        expect(screen.getByRole('button', { name: /Short calls/, pressed: false })).toHaveTextContent('Confirmed');

        fireEvent.click(within(article).getByRole('button', { name: 'Undo' }));
        await waitFor(() => expect(mocks.undoConfirm).toHaveBeenCalledWith('fresh'));
        expect(await within(article).findByRole('button', { name: 'That’s me' })).toBeInTheDocument();
    });

    it('says the portrait can use a confirm-row belief now', async () => {
        mocks.getAbout.mockImplementation(async () => aboutFor([entry, fresh], { newThisWeek: ['fresh'], portraitCandidate: true }));
        render(<MemoryPage />);
        const article = await card();
        fireEvent.click(within(article).getByRole('button', { name: 'That’s me' }));
        expect(await within(article).findByText('Confirmed. Your portrait can use this now.')).toBeInTheDocument();
    });

    it('keeps the card open with a retry message when confirming fails', async () => {
        mocks.confirm.mockRejectedValueOnce(new Error('suppressed'));
        render(<MemoryPage />);
        const article = await card();
        fireEvent.click(within(article).getByRole('button', { name: 'That’s me' }));
        expect(await within(article).findByRole('alert')).toHaveTextContent('Could not confirm this. Try again.');
        expect(within(article).getByRole('button', { name: 'That’s me' })).toBeEnabled();
    });

    it('asks Not quite in place; left blank, Isoform stops using it, and Undo puts it back', async () => {
        mocks.notQuite.mockResolvedValue(0);
        render(<MemoryPage />);
        const article = await card();
        fireEvent.click(within(article).getByRole('button', { name: 'Not quite' }));
        const field = within(article).getByRole('textbox', { name: 'What’s closer? You can leave it blank.' });
        expect(field).toHaveFocus();
        expect(field).toHaveAttribute('placeholder', 'Keeps calls short before 11 AM.');
        // Not in the side pane any more.
        expect(screen.queryByRole('textbox', { name: 'Memory point' })).not.toBeInTheDocument();
        fireEvent.click(within(article).getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(mocks.notQuite).toHaveBeenCalledWith('fresh'));
        expect(await within(article).findByText('Isoform stopped using this.')).toBeInTheDocument();
        expect(within(article).getByText('Not quite', { selector: '[data-status]' })).toHaveAttribute('data-status', 'not_quite');
        expect(mocks.saveEntry).not.toHaveBeenCalled();
        fireEvent.click(within(article).getByRole('button', { name: 'Undo' }));
        await waitFor(() => expect(mocks.undoNotQuite).toHaveBeenCalledWith('fresh', 0));
        expect(await within(article).findByRole('button', { name: 'That’s me' })).toBeInTheDocument();
    });

    it('forgets in one click after 10 seconds with the reviewed preview token, and Undo cancels it', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        render(<MemoryPage />);
        let article = await card();
        fireEvent.click(within(article).getByRole('button', { name: 'Forget' }));
        // Announced once; the visible countdown is silent.
        expect(within(article).getByRole('status')).toHaveTextContent('Forgetting in 10 seconds.');
        expect(within(article).getByRole('button', { name: 'Undo' })).toHaveFocus();
        await act(async () => { vi.advanceTimersByTime(4000); });
        expect(within(article).getByText(/Forgetting in [56] seconds\./)).toHaveAttribute('aria-hidden', 'true');
        expect(within(article).getByRole('status')).toHaveTextContent('Forgetting in 10 seconds.');
        fireEvent.click(within(article).getByRole('button', { name: 'Undo' }));
        await act(async () => { vi.advanceTimersByTime(10_000); });
        expect(mocks.getPreview).not.toHaveBeenCalled();
        expect(mocks.forgetEntry).not.toHaveBeenCalled();
        article = await card();
        expect(within(article).getByRole('button', { name: 'That’s me' })).toBeInTheDocument();

        fireEvent.click(within(article).getByRole('button', { name: 'Forget' }));
        mocks.getEntries.mockResolvedValue([entry]);
        mocks.getAbout.mockImplementation(async () => aboutFor([entry]));
        // The backend emits memory:updated on a forget; the mocked runtime
        // swallows events, so the test performs the invalidation it drives.
        mocks.forgetEntry.mockImplementation(async () => {
            pageQueryCache.invalidate(['GetMemoryEntries', 'GetAboutYou', 'GetMemoryStatus', 'GetAboutYouPortrait']);
        });
        await act(async () => { vi.advanceTimersByTime(10_000); });
        await waitFor(() => expect(mocks.forgetEntry).toHaveBeenCalledWith('fresh', 'reviewed-scope-v1'));
        expect(mocks.getPreview).toHaveBeenCalledWith('fresh');
        expect(await within(article).findByText('Forgotten. Isoform won’t learn it again for 30 days.')).toBeInTheDocument();
        expect(screen.queryByRole('button', { name: /Short calls/, pressed: false })).not.toBeInTheDocument();
    });

    it('says nothing was removed when the one-click forget fails, and retries', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        mocks.forgetEntry.mockRejectedValueOnce(new Error('scope changed'));
        render(<MemoryPage />);
        const article = await card();
        fireEvent.click(within(article).getByRole('button', { name: 'Forget' }));
        await act(async () => { vi.advanceTimersByTime(10_000); });
        expect(await within(article).findByText('Could not forget this. Nothing was removed.')).toBeInTheDocument();
        fireEvent.click(within(article).getByRole('button', { name: 'Try again' }));
        await waitFor(() => expect(mocks.forgetEntry).toHaveBeenCalledTimes(2));
        expect(mocks.getPreview).toHaveBeenCalledTimes(2);
    });

    it('cancels a pending forget when the page closes, so nothing is forgotten', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        const { unmount } = render(<MemoryPage />);
        const article = await card();
        fireEvent.click(within(article).getByRole('button', { name: 'Forget' }));
        expect(within(article).getByRole('button', { name: 'Undo' })).toHaveFocus();
        unmount();
        await act(async () => { vi.advanceTimersByTime(15_000); });
        expect(mocks.getPreview).not.toHaveBeenCalled();
        expect(mocks.forgetEntry).not.toHaveBeenCalled();
    });

    it('cancels a pending one-click forget when the reviewed Forget this runs first', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        render(<MemoryPage initialSelectedId="fresh" />);
        const article = await card();
        fireEvent.click(within(article).getByRole('button', { name: 'Forget' }));
        const detail = screen.getByRole('complementary', { name: 'Belief detail' });
        fireEvent.click(within(detail).getByRole('button', { name: 'Forget this' }));
        const dialog = await screen.findByRole('dialog', { name: 'Forget this memory?' });
        await within(dialog).findByText('Calls');
        mocks.getEntries.mockResolvedValue([entry]);
        mocks.getAbout.mockImplementation(async () => aboutFor([entry]));
        fireEvent.click(within(dialog).getByRole('button', { name: 'Forget this' }));
        await waitFor(() => expect(mocks.forgetEntry).toHaveBeenCalledTimes(1));
        await act(async () => { vi.advanceTimersByTime(15_000); });
        expect(mocks.forgetEntry).toHaveBeenCalledTimes(1);
        expect(screen.queryByText('Could not forget this. Nothing was removed.')).not.toBeInTheDocument();
    });

    it('returns Not quite to the open card on Cancel or Esc, and with words saves the wording with Undo', async () => {
        mocks.saveEntry.mockImplementation(async (id: string, content: string) => ({ ...fresh, id, content, source: 'user_edit' }));
        mocks.undoWording.mockResolvedValue({ ...fresh });
        render(<MemoryPage />);
        let article = await card();
        fireEvent.click(within(article).getByRole('button', { name: 'Not quite' }));
        fireEvent.click(within(article).getByRole('button', { name: 'Cancel' }));
        article = await card();
        expect(within(article).getByRole('button', { name: 'That’s me' })).toBeInTheDocument();
        fireEvent.click(within(article).getByRole('button', { name: 'Not quite' }));
        fireEvent.keyDown(within(article).getByRole('textbox'), { key: 'Escape' });
        expect(within(article).getByRole('button', { name: 'That’s me' })).toBeInTheDocument();
        fireEvent.click(within(article).getByRole('button', { name: 'Not quite' }));
        const field = within(article).getByRole('textbox');
        fireEvent.change(field, { target: { value: 'Keeps calls short before noon.' } });
        // Return saves.
        fireEvent.keyDown(field, { key: 'Enter' });
        await waitFor(() => expect(mocks.saveEntry).toHaveBeenCalledWith('fresh', fresh.content.replace('Keeps calls short before 11 AM.', 'Keeps calls short before noon.')));
        expect(await within(article).findByText('Corrected. Isoform will use your wording.')).toBeInTheDocument();
        expect(within(article).getByText('Confirmed', { selector: '[data-status]' })).toBeInTheDocument();
        fireEvent.click(within(article).getByRole('button', { name: 'Undo' }));
        await waitFor(() => expect(mocks.undoWording).toHaveBeenCalledWith('fresh', 'Keeps calls short before noon.'));
    });

    it('keeps the confirmation when the belief is edited after That’s me', async () => {
        const confirmedContent = '---\nconfirmed_at: 2026-10-22T09:00:00Z\n---\n' + fresh.content;
        mocks.confirm.mockResolvedValue({ ...fresh, content: confirmedContent });
        mocks.saveEntry.mockImplementation(async (id: string, content: string) => ({ ...fresh, id, content }));
        render(<MemoryPage initialSelectedId="fresh" />);
        const detail = await screen.findByRole('complementary', { name: 'Belief detail' });
        fireEvent.click(await within(detail).findByRole('button', { name: 'That’s me' }));
        await waitFor(() => expect(mocks.confirm).toHaveBeenCalledWith('fresh'));
        await waitFor(() => expect(within(detail).getByRole('button', { name: 'Edit wording' })).toBeEnabled());
        fireEvent.click(within(detail).getByRole('button', { name: 'Edit wording' }));
        fireEvent.change(screen.getByRole('textbox', { name: 'Memory point' }), { target: { value: 'Keeps calls short before noon.' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save' }));
        await waitFor(() => expect(mocks.saveEntry).toHaveBeenCalledWith('fresh', confirmedContent.replace('Keeps calls short before 11 AM.', 'Keeps calls short before noon.')));
    });

    it('says so in the detail when confirming fails', async () => {
        mocks.confirm.mockRejectedValueOnce(new Error('suppressed'));
        render(<MemoryPage initialSelectedId="fresh" />);
        const detail = await screen.findByRole('complementary', { name: 'Belief detail' });
        fireEvent.click(await within(detail).findByRole('button', { name: 'That’s me' }));
        expect(await within(detail).findByRole('alert')).toHaveTextContent('Could not confirm this. Try again.');
        expect(within(detail).getByRole('button', { name: 'That’s me' })).toBeEnabled();
    });

    it('cancels a pending forget when the page jumps to Activity and stays mounted behind it', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        mocks.getEvidence.mockResolvedValue([{ at: Date.now(), durationMs: 0, appName: 'Zoom', text: 'Planned the week.', activitySessionId: 's-9' }]);
        const onOpen = vi.fn();
        function Host() {
            const [open, setOpen] = useState(true);
            return (
                <Dialog open={open} onOpenChange={setOpen}>
                    <RetainedDialogContent open={open} retain>
                        <DialogTitle>About you</DialogTitle>
                        <MemoryPage
                            initialSelectedId="fresh"
                            active={open}
                            onOpenActivitySession={(id) => { onOpen(id); setOpen(false); }}
                        />
                    </RetainedDialogContent>
                </Dialog>
            );
        }
        render(<Host />);
        const article = await card();
        fireEvent.click(within(article).getByRole('button', { name: 'Forget' }));
        fireEvent.click(await screen.findByRole('button', { name: 'See these moments in Activity' }));
        expect(onOpen).toHaveBeenCalledWith('s-9');
        await act(async () => { vi.advanceTimersByTime(15_000); });
        expect(mocks.getPreview).not.toHaveBeenCalled();
        expect(mocks.forgetEntry).not.toHaveBeenCalled();
    });

    it('cancels a pending forget when the page is closed but kept mounted', async () => {
        vi.useFakeTimers({ shouldAdvanceTime: true });
        const { rerender } = render(<MemoryPage active />);
        const article = await card();
        fireEvent.click(within(article).getByRole('button', { name: 'Forget' }));
        rerender(<MemoryPage active={false} />);
        await act(async () => { vi.advanceTimersByTime(15_000); });
        expect(mocks.forgetEntry).not.toHaveBeenCalled();
    });

    it("answers in the detail the same way, keeps Edit wording and Forget, and says when later", async () => {
        render(<MemoryPage initialSelectedId="fresh" />);
        const detail = await screen.findByRole('complementary', { name: 'Belief detail' });
        expect(within(detail).getByRole('button', { name: 'Not quite' })).toBeInTheDocument();
        fireEvent.click(await within(detail).findByRole('button', { name: 'That’s me' }));
        expect(await within(detail).findByText('Confirmed. Isoform treats this as fact now.')).toBeInTheDocument();
        expect(within(detail).getByRole('button', { name: 'Undo' })).toBeInTheDocument();
        expect(within(detail).getByRole('button', { name: 'Edit wording' })).toBeInTheDocument();
        expect(within(detail).getByRole('button', { name: 'Forget this' })).toBeInTheDocument();
        expect(within(detail).queryByRole('button', { name: 'Not quite' })).not.toBeInTheDocument();
        cleanup();
        pageQueryCache.clear();

        // Later: a confirmed belief says when, and offers Not quite.
        mocks.getAbout.mockImplementation(async () => aboutFor([entry, fresh], { status: 'confirmed', confirmedAt: Date.now() }));
        render(<MemoryPage initialSelectedId="fresh" />);
        let later = await screen.findByRole('complementary', { name: 'Belief detail' });
        expect(await within(later).findByText('You confirmed this today.')).toBeInTheDocument();
        expect(within(later).getByRole('button', { name: 'Not quite' })).toBeInTheDocument();
        expect(within(later).queryByRole('button', { name: 'That’s me' })).not.toBeInTheDocument();
        cleanup();
        pageQueryCache.clear();

        // A Not quite belief says Isoform isn't using it, and offers That's me.
        mocks.getAbout.mockImplementation(async () => aboutFor([entry, fresh], { status: 'not_quite', notQuiteAt: Date.now() - 86_400_000 }));
        render(<MemoryPage initialSelectedId="fresh" />);
        later = await screen.findByRole('complementary', { name: 'Belief detail' });
        expect(await within(later).findByText('You said not quite yesterday. Isoform isn’t using it.')).toBeInTheDocument();
        fireEvent.click(within(later).getByRole('button', { name: 'That’s me' }));
        await waitFor(() => expect(mocks.confirm).toHaveBeenCalledWith('fresh'));
        // Undo puts Not quite back.
        mocks.notQuite.mockResolvedValue(0);
        fireEvent.click(await within(later).findByRole('button', { name: 'Undo' }));
        await waitFor(() => expect(mocks.notQuite).toHaveBeenCalledWith('fresh'));
        expect(mocks.undoConfirm).toHaveBeenCalledWith('fresh');
    });

    it("keeps That's me in the detail when the About you read fails, and hides it for a confirmed file", async () => {
        mocks.getAbout.mockRejectedValue(new Error('store'));
        const confirmedFile = { ...entry, content: '---\nconfirmed_at: 2026-10-22T09:00:00Z\n---\n' + entry.content };
        resetDefaults([confirmedFile, fresh]);
        mocks.getAbout.mockRejectedValue(new Error('store'));
        const { unmount } = render(<MemoryPage initialSelectedId="fresh" />);
        let detail = await screen.findByRole('complementary', { name: 'Belief detail' });
        fireEvent.click(within(detail).getByRole('button', { name: 'That’s me' }));
        await waitFor(() => expect(mocks.confirm).toHaveBeenCalledWith('fresh'));
        unmount();
        render(<MemoryPage initialSelectedId={entry.id} />);
        detail = await screen.findByRole('complementary', { name: 'Belief detail' });
        await within(detail).findByRole('button', { name: 'Edit wording' });
        expect(within(detail).queryByRole('button', { name: 'That’s me' })).not.toBeInTheDocument();
    });

    it("confirms from the detail with That's me too", async () => {
        render(<MemoryPage initialSelectedId="fresh" />);
        const detail = await screen.findByRole('complementary', { name: 'Belief detail' });
        fireEvent.click(await within(detail).findByRole('button', { name: 'That’s me' }));
        await waitFor(() => expect(mocks.confirm).toHaveBeenCalledWith('fresh'));
    });
});

describe('About you communication', () => {
    const communicationEntry = {
        ...entry,
        id: 'communication',
        title: 'Language with Jia',
        apps: ['WeChat', 'Mail'],
        activitySessionIds: ['private-session'],
        content: '# Language with Jia\n- Writes to Jia in Chinese.\n',
    };
    const example = {
        id: 'draft-1',
        to: '加一',
        subject: '',
        text: '加一，周五前给你确认。',
        app: 'WeChat',
        recipientName: '加一',
        language: 'Chinese',
        sentences: 2,
        greeting: false,
        beliefIds: ['another-belief', 'communication'],
        createdAt: Date.now(),
    };

    beforeEach(async () => {
        vi.resetAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage('en');
        resetDefaults([communicationEntry]);
        mocks.getAbout.mockImplementation(async () => aboutFor([communicationEntry], {
            area: 'communication',
            sentMessageCount: 14,
            evidenceFirstAt: new Date(2026, 8, 30).getTime(),
            evidenceLastAt: Date.now(),
        }));
        mocks.getEvidence.mockResolvedValue([{ at: Date.now(), durationMs: 0, appName: 'WeChat', text: 'Sent a reply.', activitySessionId: 'private-session' }]);
        mocks.confirm.mockResolvedValue(communicationEntry);
    });

    it('orders every fixed area, hides empty groups, keeps kind labels and puts unknown or missing areas in Other', async () => {
        const areas = ['communication', 'work', 'people', 'tools', 'routines', 'interests', 'other'];
        const entries = areas.map((_, index) => ({
            ...entry,
            id: `area-${index}`,
            title: `Belief ${index}`,
            content: `# Belief ${index}\n- Fact ${index}.\n`,
        }));
        entries.push({ ...entry, id: 'area-unknown', title: 'Unknown area', content: '# Unknown area\n- Another fact.\n' });
        const assigned = ['interests', 'other', 'communication', 'unknown-area', 'tools', 'work', 'people', 'routines'];
        resetDefaults(entries);
        mocks.getAbout.mockResolvedValue({
            ...aboutFor(entries),
            beliefs: aboutFor(entries).beliefs.map((belief, index) => ({ ...belief, area: assigned[index] })),
        });
        render(<MemoryPage />);
        const groups = await screen.findByTestId('belief-groups');
        const headings = within(groups).getAllByRole('heading', { level: 2 });
        expect(headings.map((heading) => heading.textContent)).toEqual([
            'How you communicate1', 'Work1', 'People1', 'Tools and apps1', 'Routines1', 'Interests1', 'Other2',
        ]);
        expect(within(groups).getByText('Learned only from messages you sent. Drafts follow these.')).toBeInTheDocument();
        expect(within(groups).getAllByText('Something I learned about you').length).toBe(8);
        expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument();
    });

    it('shows sent-message evidence on New this week and confirms that drafts will follow it', async () => {
        mocks.getAbout.mockImplementation(async () => aboutFor([communicationEntry], {
            newThisWeek: ['communication'],
            area: 'communication',
            sentMessageCount: 14,
            evidenceFirstAt: new Date(2026, 8, 30).getTime(),
            evidenceLastAt: Date.now(),
        }));
        render(<MemoryPage />);
        const article = await screen.findByRole('article', { name: '“Writes to Jia in Chinese.”' });
        expect(article).toHaveTextContent('How you communicate');
        expect(article).toHaveTextContent('14 messages you sent');
        expect(article).toHaveTextContent('Sep 30, 2026 to today');
        expect(article).toHaveTextContent('WeChat and Mail');
        fireEvent.click(within(article).getByRole('button', { name: 'That’s me' }));
        expect(await within(article).findByText('Confirmed. Drafts will follow this.')).toBeInTheDocument();
        expect(mocks.confirm).toHaveBeenCalledWith('communication');
    });

    it('shows sent evidence and draft examples, hides the Activity jump and keeps Change on the inspected belief when another ID comes first', async () => {
        mocks.getExamples.mockResolvedValue([example]);
        render(<MemoryPage initialSelectedId="communication" onOpenActivitySession={vi.fn()} />);
        const detail = await screen.findByRole('complementary', { name: 'Belief detail' });
        expect(within(detail).getByText('How you communicate')).toBeInTheDocument();
        expect(within(detail).getByText(/14 messages you sent/)).toHaveTextContent('WeChat and Mail');
        expect(within(detail).getByText(/never guesses your tone/)).toBeInTheDocument();
        expect(within(detail).queryByRole('button', { name: 'See these moments in Activity' })).not.toBeInTheDocument();
        expect(within(detail).queryByRole('button', { name: 'Open where this was learned in Activity' })).not.toBeInTheDocument();
        expect(await within(detail).findByText('How a draft shows it')).toBeInTheDocument();
        expect(within(detail).getByText('To 加一 · WeChat')).toBeInTheDocument();
        expect(within(detail).getByText(example.text)).toBeInTheDocument();
        expect(within(detail).getByText('Written the way you write to 加一: Chinese, 2 sentences, no greeting.')).toBeInTheDocument();
        fireEvent.click(within(detail).getByRole('button', { name: 'Change' }));
        expect(screen.getByRole('button', { name: /Language with Jia.*Writes to Jia/, pressed: true })).toBeInTheDocument();
        expect(mocks.getExamples).toHaveBeenCalledWith('communication');
    });

    it('keeps draft examples loading, offers retry after failure and then renders the example', async () => {
        let reject!: (reason: unknown) => void;
        mocks.getExamples.mockReturnValueOnce(new Promise((_, fail) => { reject = fail; }));
        render(<MemoryPage initialSelectedId="communication" />);
        const detail = await screen.findByRole('complementary', { name: 'Belief detail' });
        expect(within(detail).getByText('How a draft shows it')).toBeInTheDocument();
        expect(within(detail).getByText('How a draft shows it').parentElement!.querySelector('[aria-busy="true"]')).not.toBeNull();
        await act(async () => reject(new Error('unavailable')));
        const alert = await within(detail).findByRole('alert');
        expect(alert).toHaveTextContent('Could not load drafts that used this belief.');
        mocks.getExamples.mockResolvedValue([example]);
        fireEvent.click(within(alert).getByRole('button', { name: 'Try again' }));
        expect(await within(detail).findByText(example.text)).toBeInTheDocument();
        expect(mocks.getExamples).toHaveBeenCalledTimes(2);
    });

    it.each(['memory:updated', 'closed-eyes:changed'])('reloads draft examples after %s and hides a withdrawn example', async (eventName) => {
        mocks.getExamples.mockResolvedValue([example]);
        render(<MemoryPage initialSelectedId="communication" />);
        expect(await screen.findByText(example.text)).toBeInTheDocument();
        expect(mocks.getExamples).toHaveBeenCalledTimes(1);
        mocks.getExamples.mockResolvedValue([]);
        await act(async () => {
            for (const listener of [...(mocks.events.get(eventName) ?? [])]) listener(undefined);
        });
        expect(screen.queryByText(example.text)).not.toBeInTheDocument();
        const readsBeforeReopen = mocks.getExamples.mock.calls.length;
        fireEvent.click(await screen.findByRole('button', { name: /Language with Jia.*Writes to Jia/ }));
        await waitFor(() => expect(mocks.getExamples.mock.calls.length).toBeGreaterThan(readsBeforeReopen));
        await waitFor(() => expect(screen.queryByText('How a draft shows it')).not.toBeInTheDocument());
        expect(screen.queryByText(example.text)).not.toBeInTheDocument();
    });

    it('does not restore a stale example response after a newer withdrawal read', async () => {
        let resolveFirst!: (rows: Array<typeof example>) => void;
        mocks.getExamples.mockReturnValueOnce(new Promise((resolve) => { resolveFirst = resolve; })).mockResolvedValue([]);
        render(<MemoryPage initialSelectedId="communication" />);
        await waitFor(() => expect(mocks.getExamples).toHaveBeenCalledTimes(1));
        await act(async () => {
            for (const listener of [...(mocks.events.get('memory:updated') ?? [])]) listener(undefined);
        });
        const readsBeforeReopen = mocks.getExamples.mock.calls.length;
        fireEvent.click(await screen.findByRole('button', { name: /Language with Jia.*Writes to Jia/ }));
        await waitFor(() => expect(mocks.getExamples.mock.calls.length).toBeGreaterThan(readsBeforeReopen));
        await waitFor(() => expect(screen.queryByText('How a draft shows it')).not.toBeInTheDocument());
        await act(async () => resolveFirst([example]));
        expect(screen.queryByText(example.text)).not.toBeInTheDocument();
        expect(screen.queryByText('How a draft shows it')).not.toBeInTheDocument();
    });

    it('hides How a draft shows it when there are no examples and never reads examples for other areas', async () => {
        const { unmount } = render(<MemoryPage initialSelectedId="communication" />);
        const detail = await screen.findByRole('complementary', { name: 'Belief detail' });
        await waitFor(() => expect(mocks.getExamples).toHaveBeenCalledWith('communication'));
        expect(within(detail).queryByText('How a draft shows it')).not.toBeInTheDocument();
        unmount();

        resetPageQueries();
        resetDefaults([entry]);
        render(<MemoryPage initialSelectedId={entry.id} />);
        await screen.findByRole('complementary', { name: 'Belief detail' });
        expect(mocks.getExamples).toHaveBeenCalledTimes(1);
        expect(screen.queryByText('How a draft shows it')).not.toBeInTheDocument();
    });

    it('keeps communication locale keys and the exact English concept note in parity', () => {
        expect(Object.keys(zh.aboutYou.areas)).toEqual(Object.keys(en.aboutYou.areas));
        expect(Object.keys(zh.aboutYou.communication)).toEqual(Object.keys(en.aboutYou.communication));
        expect(Object.keys(zh.aboutYou.communication.languages)).toEqual(Object.keys(en.aboutYou.communication.languages));
        expect(en.aboutYou.communication.note).toBe('Learned only from messages you sent. Drafts follow these.');
        expect(en.aboutYou.review.communicationConfirmed).toBe('Confirmed. Drafts will follow this.');
        expect(zh.aboutYou.review).toHaveProperty('communicationWhere');
        expect(zh.aboutYou.review).toHaveProperty('communicationConfirmed');
    });
});

describe('Decision 11: everything the Memory page had stays reachable', () => {
    const versioned = { ...entry, hasHistory: true, source: 'user_edit', activitySessionIds: ['s-1'] };
    beforeEach(async () => {
        vi.resetAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage('en');
        resetDefaults([versioned]);
        mocks.getEvidence.mockResolvedValue([{ at: Date.now(), durationMs: 0, appName: 'Mail', text: 'Wrote a detailed reply.', activitySessionId: 's-1' }]);
        mocks.getPreview.mockResolvedValue(preview());
        mocks.listVersions.mockResolvedValue([{ timestampMs: Date.now() - 86400000, sizeBytes: 100 }]);
        mocks.getVersion.mockResolvedValue('# Writing style\n- **Dimension**: user_insight\n\nI prefer short replies.\n');
    });

    it('keeps search, the kind filter and the source in Technical details after fixed area grouping replaces the switch', async () => {
        render(<MemoryPage initialSelectedId={entry.id} />);
        expect(await screen.findByRole('textbox', { name: 'Search memory text...' })).toBeInTheDocument();
        expect(screen.getByRole('combobox', { name: 'Filter by kind' })).toBeInTheDocument();
        expect(screen.queryByRole('radiogroup')).not.toBeInTheDocument();
        const detail = screen.getByRole('complementary', { name: 'Belief detail' });
        fireEvent.click(within(detail).getByRole('button', { name: 'Technical details' }));
        expect(within(detail).getByText('User edited')).toBeInTheDocument();
    });

    it.each([
        ['New memory file', 'New Memory'],
        ['Import from AI tools', 'Import dialog open'],
        ['Memory Dream', 'Dream dialog open'],
        ['Translate memories', 'Translate memories'],
    ])('opens %s from the page More menu', async (item, opened) => {
        render(<MemoryPage />);
        await openPageMenu();
        fireEvent.click(await screen.findByRole('menuitem', { name: item }));
        if (opened === 'New Memory' || opened === 'Translate memories') {
            expect(await screen.findByRole('dialog', { name: opened })).toBeInTheDocument();
        } else {
            expect(await screen.findByText(opened)).toBeInTheDocument();
        }
    });

    it('re-indexes from the page More menu', async () => {
        render(<MemoryPage />);
        await openPageMenu();
        mocks.sync.mockResolvedValue(undefined);
        fireEvent.click(await screen.findByRole('menuitem', { name: 'Re-index memory files' }));
        await waitFor(() => expect(mocks.sync).toHaveBeenCalledOnce());
    });

    it('keeps copy ID, version history with Compare, translate and Wrong subject in the detail More menu', async () => {
        render(<MemoryPage initialSelectedId={entry.id} />);
        await screen.findByRole('heading', { level: 2, name: entry.title });
        await openDetailMenu();
        expect(await screen.findByRole('menuitem', { name: 'Copy memory ID' })).toBeInTheDocument();
        expect(screen.getByRole('menuitem', { name: 'Translate' })).toBeInTheDocument();
        expect(screen.getByRole('menuitem', { name: 'Wrong subject' })).toBeInTheDocument();
        fireEvent.click(screen.getByRole('menuitem', { name: 'Version history' }));
        const history = await screen.findByRole('dialog', { name: 'Version history' });
        await waitFor(() => expect(within(history).getByRole('button', { name: 'Compare' })).toBeEnabled());
        fireEvent.click(within(history).getByRole('button', { name: 'Compare' }));
        expect(await within(history).findByText('Diff')).toBeInTheDocument();
    });

    it('keeps Wrong subject from the menu, per-session Not right, sentence edits and the Forget preview counts', async () => {
        render(<MemoryPage initialSelectedId={entry.id} />);
        await screen.findByRole('heading', { level: 2, name: entry.title });
        await openDetailMenu();
        fireEvent.click(await screen.findByRole('menuitem', { name: 'Wrong subject' }));
        expect(await screen.findByRole('dialog', { name: 'Wrong subject?' })).toBeInTheDocument();
        fireEvent.click(within(screen.getByRole('dialog', { name: 'Wrong subject?' })).getByRole('button', { name: 'Cancel' }));
        fireEvent.click(await screen.findByRole('button', { name: 'Not right' }));
        expect(await screen.findByRole('dialog', { name: 'Wrong subject?' })).toBeInTheDocument();
        fireEvent.click(within(screen.getByRole('dialog', { name: 'Wrong subject?' })).getByRole('button', { name: 'Cancel' }));
        fireEvent.click(screen.getByRole('button', { name: 'I prefer detailed replies.' }));
        expect(screen.getByRole('textbox', { name: 'Memory point' })).toHaveValue('I prefer detailed replies.');
        fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
        fireEvent.click(screen.getByRole('button', { name: 'Forget this' }));
        const dialog = await screen.findByRole('dialog', { name: 'Forget this memory?' });
        expect(await within(dialog).findByText('Stored search embeddings: 3. Saved versions: 2.')).toBeInTheDocument();
        expect(mocks.forgetEntry).not.toHaveBeenCalled();
    });
});


describe('MemoryPage with the page query cache', () => {
    beforeEach(async () => {
        vi.resetAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage('en');
        resetDefaults([entry]);
    });

    it('paints the cached beliefs and portrait at once on a warm reopen, without the skeleton', async () => {
        pageQueryCache.set('GetMemoryEntries', [], [entry]);
        pageQueryCache.set('GetAboutYou', [], aboutFor([entry]));
        pageQueryCache.set('GetMemoryStatus', [], { files: 1, chunks: 3, vectorReady: true });
        pageQueryCache.set('GetAboutYouPortrait', [], readyPortrait([{ text: 'Wants detailed replies', memoryIds: [entry.id] }]));
        // A warm reopen has the goal lists cached too; the Goals group paints them at once.
        pageQueryCache.set('ListGoals', [], { tracking: [], done: [], held: [] });
        const { container } = render(<MemoryPage />);
        expect(screen.getByRole('button', { name: /Writing style.*I prefer detailed replies/, pressed: false })).toBeInTheDocument();
        expect(screen.getByText('Wants detailed replies')).toBeInTheDocument();
        expect(container.querySelector('[aria-busy="true"]')).toBeNull();
        await act(async () => {});
    });

    it('shows the skeleton on a true first load', () => {
        mocks.getEntries.mockReturnValue(new Promise(() => undefined));
        const { container } = render(<MemoryPage />);
        expect(container.querySelector('[aria-busy="true"]')).not.toBeNull();
        expect(screen.queryByRole('button', { name: /I prefer detailed replies/ })).toBeNull();
    });

    it('drops every cached About you read on the privacy invalidation', () => {
        pageQueryCache.set('GetMemoryEntries', [], [entry]);
        pageQueryCache.set('GetAboutYou', [], aboutFor([entry]));
        pageQueryCache.set('GetMemoryStatus', [], { files: 1, chunks: 3, vectorReady: true });
        pageQueryCache.set('GetAboutYouPortrait', [], learningPortrait());
        pageQueryCache.invalidate(['GetMemoryEntries', 'GetAboutYou', 'GetMemoryStatus', 'GetAboutYouPortrait']);
        for (const name of ['GetMemoryEntries', 'GetAboutYou', 'GetMemoryStatus', 'GetAboutYouPortrait']) {
            expect(pageQueryCache.peek(name)).toBeUndefined();
        }
    });

    it('a privacy sweep withdraws the hidden retained page — beliefs, portrait, search text — before the re-read lands, then a permitted read recovers', async () => {
        mocks.getPortrait.mockResolvedValue(readyPortrait([{ text: 'Wants detailed replies', memoryIds: [entry.id] }]));
        const view = render(<MemoryPage active={false} />);
        expect(await screen.findByRole('button', { name: /I prefer detailed replies/ })).toBeInTheDocument();
        expect(await screen.findByText('Wants detailed replies')).toBeInTheDocument();
        // A remembered search narrows the list; it holds text too.
        fireEvent.change(screen.getByRole('textbox', { name: 'Search memory text...' }), { target: { value: 'detailed' } });
        expect(screen.getByRole('textbox', { name: 'Search memory text...' })).toHaveValue('detailed');

        // The sweep's re-read hangs: nothing may paint pre-transition text.
        mocks.getEntries.mockImplementation(() => new Promise(() => {}));
        mocks.getAbout.mockImplementation(() => new Promise(() => {}));
        mocks.getStatus.mockImplementation(() => new Promise(() => {}));
        mocks.getPortrait.mockImplementation(() => new Promise(() => {}));
        await emitEvent('memory:updated');
        expect(screen.queryByRole('button', { name: /I prefer detailed replies/ })).not.toBeInTheDocument();
        expect(screen.queryByText('Wants detailed replies')).not.toBeInTheDocument();
        expect(screen.queryByRole('textbox', { name: 'Search memory text...' })).not.toBeInTheDocument();

        // Reopening while the reads are still held shows the skeleton, never
        // the withdrawn beliefs.
        view.rerender(<MemoryPage active={true} />);
        expect(screen.queryByRole('button', { name: /I prefer detailed replies/ })).not.toBeInTheDocument();

        // A later ordinary change re-reads: what is still permitted returns.
        resetDefaults([entry]);
        await emitEvent('memory:updated');
        expect(await screen.findByRole('button', { name: /I prefer detailed replies/ })).toBeInTheDocument();
    });

    it('a privacy sweep with failing reads shows the retry, never the withdrawn beliefs', async () => {
        render(<MemoryPage />);
        expect(await screen.findByRole('button', { name: /I prefer detailed replies/ })).toBeInTheDocument();
        mocks.getEntries.mockRejectedValue(new Error('gone'));
        mocks.getAbout.mockRejectedValue(new Error('gone'));
        mocks.getStatus.mockRejectedValue(new Error('gone'));
        await emitEvent('closed-eyes:changed');
        expect(screen.queryByRole('button', { name: /I prefer detailed replies/ })).not.toBeInTheDocument();
        expect(await screen.findByRole('alert')).toHaveTextContent('Could not load what Isoform believes about you.');
    });

    it('the portrait follows every privacy sweep, not just Forget', async () => {
        mocks.getPortrait.mockResolvedValue(readyPortrait([{ text: 'Wants detailed replies', memoryIds: [entry.id] }]));
        render(<MemoryPage />);
        expect(await screen.findByText('Wants detailed replies')).toBeInTheDocument();
        mocks.getPortrait.mockImplementation(() => new Promise(() => {}));
        mocks.getEntries.mockImplementation(() => new Promise(() => {}));
        mocks.getAbout.mockImplementation(() => new Promise(() => {}));
        mocks.getStatus.mockImplementation(() => new Promise(() => {}));
        await emitEvent('permissions:changed');
        expect(screen.queryByText('Wants detailed replies')).not.toBeInTheDocument();
    });
});

describe('MemoryPage goals group', () => {
    const at = (day: number) => new Date(2026, 9, day, 9).toISOString();
    function goal(overrides: Partial<GoalItemView>): GoalItemView {
        return {
            id: 'g', eventId: 'e1', kind: 'goal', title: 'Goal', state: 'active', group: 'tracking', slipping: false,
            addedAt: at(20), updatedAt: at(20), lastEvent: 'discovered', origin: 'chat', ...overrides,
        };
    }
    const GOAL_PAGE: GoalsPageView = {
        tracking: [
            goal({ id: 'passport', eventId: 'e1', title: 'Renew passport', state: 'waiting', waitingOn: 'Ana', lastEvent: 'waiting' }),
            goal({ id: 'follow', eventId: 'e2', title: 'Send Dana the notes', origin: 'activity' }),
        ],
        done: [goal({ id: 'dentist', eventId: 'e3', title: 'Book the dentist', state: 'resolved', group: 'done', lastEvent: 'resolved' })],
        held: [goal({ id: 'kept', eventId: 'e4', title: 'Answer Dana about the renewal', group: 'held', origin: 'noticed' })],
    };

    beforeEach(async () => {
        vi.resetAllMocks();
        window.localStorage.clear();
        await i18n.changeLanguage('en');
        resetDefaults([entry]);
        mocks.getAbout.mockImplementation(async () => aboutFor([entry], { newThisWeek: [entry.id] }));
    });

    it('keeps a marked goal in place with Undo', async () => {
        mocks.listGoals.mockResolvedValue(GOAL_PAGE);
        mocks.moveGoal.mockResolvedValueOnce({ id: 'follow', eventId: 'e5', state: 'resolved' });
        render(<MemoryPage />);
        const group = await screen.findByRole('region', { name: 'Goals' });
        const mark = await within(group).findByRole('button', { name: 'Mark done, Send Dana the notes' });
        await act(async () => { fireEvent.click(mark); });
        const row = within(group).getByText('Send Dana the notes').closest('li')!;
        expect(within(row).getByText('Marked done')).toBeInTheDocument();
        mocks.moveGoal.mockResolvedValueOnce({ id: 'follow', eventId: 'e6', state: 'active' });
        await act(async () => { fireEvent.click(within(row).getByRole('button', { name: 'Undo' })); });
        expect(mocks.moveGoal).toHaveBeenLastCalledWith(expect.objectContaining({ goalId: 'follow', action: 'restore', expectedEventId: 'e5' }));
        expect(within(group).getByRole('button', { name: 'Mark done, Send Dana the notes' })).toBeInTheDocument();
    });

    it('shows the Goals group above New this week and the belief groups', async () => {
        mocks.listGoals.mockResolvedValue(GOAL_PAGE);
        render(<MemoryPage onCreateGoal={vi.fn()} />);
        const goalsGroup = await screen.findByRole('region', { name: 'Goals' });
        const review = await screen.findByRole('region', { name: 'New this week' });
        const groups = await screen.findByTestId('belief-groups');
        expect(goalsGroup.compareDocumentPosition(review) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        expect(review.compareDocumentPosition(groups) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy();
        expect(within(goalsGroup).getByText('Renew passport')).toBeInTheDocument();
        expect(within(goalsGroup).getByText('Waiting on someone')).toHaveClass('text-copper');
        expect(within(goalsGroup).getAllByText('Tracking')).toHaveLength(2);
        expect(within(goalsGroup).getByText('Only on this computer (1)')).toBeInTheDocument();
    });

    it.each([
        ['en', 'Goals', 'Add a goal', "No goals yet. Tell Isoform what you're working toward and it keeps track here.", 'I want to set a new goal: '],
        ['zh-CN', '目标', '添加目标', '还没有目标。告诉 Isoform 你在推进什么，它会在这里跟进。', '我想设定一个新目标：'],
    ])('reads one empty line and offers Add a goal in %s', async (language, title, add, empty, prompt) => {
        await i18n.changeLanguage(language);
        mocks.listGoals.mockResolvedValue({ tracking: [], done: [], held: [] });
        const onCreateGoal = vi.fn();
        render(<MemoryPage onCreateGoal={onCreateGoal} />);
        const group = await screen.findByRole('region', { name: title });
        expect(await within(group).findByTestId('goals-empty')).toHaveTextContent(empty);
        fireEvent.click(within(group).getByRole('button', { name: add }));
        expect(onCreateGoal).toHaveBeenCalledWith(prompt);
    });

    it('does not offer Add a goal when the page is not wired to Chat', async () => {
        mocks.listGoals.mockResolvedValue(GOAL_PAGE);
        render(<MemoryPage />);
        const group = await screen.findByRole('region', { name: 'Goals' });
        await within(group).findByText('Renew passport');
        expect(within(group).queryByRole('button', { name: 'Add a goal' })).not.toBeInTheDocument();
    });

    it('folds done goals under Done (n) until they are opened', async () => {
        mocks.listGoals.mockResolvedValue(GOAL_PAGE);
        render(<MemoryPage onCreateGoal={vi.fn()} />);
        const group = await screen.findByRole('region', { name: 'Goals' });
        const fold = await within(group).findByRole('button', { name: 'Done (1)' });
        expect(fold).toHaveAttribute('aria-expanded', 'false');
        expect(within(group).queryByText('Book the dentist')).not.toBeInTheDocument();
        fireEvent.click(fold);
        expect(fold).toHaveAttribute('aria-expanded', 'true');
        expect(within(group).getByText('Book the dentist')).toBeInTheDocument();
    });

    it('opens the goal Search or a notice asked for, and hands the request back', async () => {
        mocks.listGoals.mockResolvedValue(GOAL_PAGE);
        mocks.getGoal.mockResolvedValue({ goal: GOAL_PAGE.tracking[0], history: [], sources: [] });
        const onOpenGoalHandled = vi.fn();
        const onTalkAboutGoal = vi.fn();
        render(<MemoryPage initialSelectedId={entry.id} openGoalId="passport" openGoalSignal={1}
            onOpenGoalHandled={onOpenGoalHandled} onTalkAboutGoal={onTalkAboutGoal} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Talk about this goal' }));
        expect(mocks.getGoal).toHaveBeenCalledWith('passport');
        expect(onOpenGoalHandled).toHaveBeenCalledTimes(1);
        expect(onTalkAboutGoal).toHaveBeenCalledWith(GOAL_PAGE.tracking[0], "Let's talk about my goal: Renew passport");
        expect(screen.queryByRole('heading', { name: entry.title })).not.toBeInTheDocument();
    });
});
