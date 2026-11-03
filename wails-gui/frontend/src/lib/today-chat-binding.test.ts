import { afterEach, expect, it, vi } from 'vitest';
import { PrepareTodayChatDraft, SendPromptWithTodayContext } from './wails-shim-app';

const originalGo = (window as any).go;
afterEach(() => { (window as any).go = originalGo; });

it('exports Today draft preparation through the explicit Wails shim', async () => {
    const ref = { sourceKind: 'activity', sourceId: 'release-notes' };
    const draft = {
        prefill: { kind: 'activity', title: 'Release notes', durationMinutes: 18 },
        counts: { memories: 3, activities: 2, people: 1 },
        fingerprint: 'test-fingerprint',
    };
    const binding = vi.fn().mockResolvedValue(draft);
    (window as any).go = { main: { App: { PrepareTodayChatDraft: binding } } };
    await expect(PrepareTodayChatDraft(ref)).resolves.toEqual(draft);
    expect(binding).toHaveBeenCalledWith(ref);
});

it('passes the Today user row to Go and returns its admitted sort order', async () => {
    const binding = vi.fn().mockResolvedValue(7);
    (window as any).go = { main: { App: { SendPromptWithTodayContext: binding } } };
    const item = { id: 'today-item', kind: 'user', content: 'Help with the release.', sortOrder: 0 };
    const ref = { sourceKind: 'activity', sourceId: 'release-notes' };
    await expect(SendPromptWithTodayContext(item.content, '', 'model', '', 'main-session', [], [], false, false, 'en', '', item, ref)).resolves.toBe(7);
    expect(binding.mock.calls[0].slice(-2)).toEqual([item, ref]);
});
