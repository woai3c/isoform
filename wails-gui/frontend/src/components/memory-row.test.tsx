import { fireEvent, render, screen, within } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { learnedWhen, MemoryRow } from './memory-row';

const id = '7f9343c6-30cf-4833-a386-962a62877328';
const entry = {
    id,
    title: id,
    content: `---\nuuid: ${id}\ntype: memory\ndimension: user_insight\ntimestamp: 2026-10-16T09:30:00Z\n---\n# Writing style\n- Prefers **concise** replies.\n- Uses examples when helpful.`,
    dimension: 'user_insight',
    updatedAt: new Date(2026, 9, 16, 9, 30).getTime(),
    modTime: new Date(2026, 9, 17, 12).getTime(),
    sizeBytes: 120,
    source: 'activity',
    hasHistory: false,
    activitySessionIds: [],
    apps: [],
};

describe('MemoryRow', () => {
    beforeEach(() => {
        vi.useFakeTimers();
        vi.setSystemTime(new Date(2026, 9, 17, 10));
    });
    afterEach(() => vi.useRealTimers());

    it.each([
        ['en', 'Updated yesterday', 'Edit', 'Forget', 'Confidence 3 of 4', 'Inferred'],
        ['zh-CN', '昨天更新', '编辑', '忘记', '可信度 3/4', '推断'],
    ])('leads with the learned text, then confidence, status and calendar date, with visible actions in %s', async (language, when, edit, forget, confidence, status) => {
        await i18n.changeLanguage(language);
        const onSelect = vi.fn();
        const onEdit = vi.fn();
        const onForget = vi.fn();
        const belief = { id, title: 'Writing style', status: 'inferred' as const, confidence: 3, firstLearnedAt: entry.updatedAt, updatedAt: entry.updatedAt, sessionCount: 4, dayCount: 2, confirmedAt: 0 };
        const { container } = render(<MemoryRow entry={entry} belief={belief} onSelect={onSelect} onEdit={onEdit} onForget={onForget} onReject={vi.fn()} />);
        const select = screen.getByRole('button', { name: /Writing style.*Prefers concise replies\./, pressed: false });
        expect(select.querySelector('span')).toHaveTextContent('Writing style');
        expect(within(select).getByRole('img', { name: confidence })).toBeInTheDocument();
        expect(within(select).getByText(status)).toBeInTheDocument();
        expect(screen.getByText(when)).toBeInTheDocument();
        // Fixed area groups keep the kind as a small label on every row.
        expect(screen.getByText(language === 'en' ? 'A preference' : '一项偏好')).toBeInTheDocument();
        expect(container).not.toHaveTextContent(id);
        expect(container.innerHTML).not.toContain(id);
        expect(container).not.toHaveTextContent('09:30');
        expect(container).not.toHaveTextContent('user_insight');
        expect(container).not.toHaveTextContent('120');
        const editButton = screen.getByRole('button', { name: new RegExp('^' + edit) });
        const forgetButton = screen.getByRole('button', { name: new RegExp('^' + forget) });
        expect(editButton).toBeVisible();
        expect(forgetButton).toBeVisible();
        fireEvent.click(editButton);
        fireEvent.click(forgetButton);
        expect(onEdit).toHaveBeenCalledOnce();
        expect(onForget).toHaveBeenCalledOnce();
        expect(onSelect).not.toHaveBeenCalled();
        fireEvent.click(select);
        expect(onSelect).toHaveBeenCalledOnce();
    });

    it('shows the kind label explicitly', async () => {
        await i18n.changeLanguage('en');
        render(<MemoryRow entry={entry} showKind onSelect={vi.fn()} onEdit={vi.fn()} onForget={vi.fn()} onReject={vi.fn()} />);
        expect(screen.getByText('A preference')).toBeInTheDocument();
    });

    it('skips legacy metadata and nested session IDs while keeping body facts', async () => {
        await i18n.changeLanguage('en');
        render(<MemoryRow entry={{ ...entry, content: `# Writing style\n- **Dimension**: user_insight\n- **Activity Session**:\n  - ${id}\n\n- **Preference**: concise replies.` }} onSelect={vi.fn()} onEdit={vi.fn()} onForget={vi.fn()} onReject={vi.fn()} />);
        expect(screen.getByRole('button', { name: /Preference: concise replies\./, pressed: false })).toBeInTheDocument();
        expect(screen.queryByText(id)).not.toBeInTheDocument();
    });

    it('uses a readable empty state instead of the filename when no point has been written', async () => {
        await i18n.changeLanguage('en');
        const { container } = render(<MemoryRow entry={{ ...entry, content: `---\nuuid: ${id}\n---\n` }} onSelect={vi.fn()} onEdit={vi.fn()} onForget={vi.fn()} onReject={vi.fn()} />);
        expect(screen.getByRole('button', { name: 'No memory text yet', pressed: false })).toBeInTheDocument();
        expect(container.innerHTML).not.toContain(id);
    });

    it.each([
        [new Date(2026, 9, 17, 9, 59), 'Learned today'],
        [new Date(2026, 9, 16, 23, 59), 'Learned yesterday'],
        [new Date(2026, 9, 14), 'Learned 3 days ago'],
        [new Date(2026, 9, 9), 'Learned last week'],
        [new Date(2026, 9, 2), 'Learned 2 weeks ago'],
        [new Date(2026, 8, 1), 'Learned 1 month ago'],
        [new Date(2024, 9, 17), 'Learned 2 years ago'],
    ])('uses plain words for %s', async (date, expected) => {
        await i18n.changeLanguage('en');
        expect(learnedWhen(i18n.getFixedT('en', 'memory'), date.getTime())).toBe(expected);
    });

    it('falls back to mtime and handles an unknown learning date', async () => {
        await i18n.changeLanguage('en');
        render(<MemoryRow entry={{ ...entry, updatedAt: 0 }} onSelect={vi.fn()} onEdit={vi.fn()} onForget={vi.fn()} onReject={vi.fn()} />);
        expect(screen.getByText('Updated today')).toBeInTheDocument();
        expect(learnedWhen(i18n.getFixedT('en', 'memory'), 0)).toBe('Learning date unavailable');
    });
});
