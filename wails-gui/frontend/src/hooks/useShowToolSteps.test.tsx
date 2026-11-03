import { act, fireEvent, render, renderHook, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { ToolGroupItem } from '@/components/tool-group-item';
import { useShowToolSteps } from './useShowToolSteps';

describe('useShowToolSteps', () => {
    beforeEach(async () => {
        localStorage.clear();
        await i18n.changeLanguage('en');
    });

    it.each(['never configured', 'previously turned off'])('defaults an ambiguous missing preference to off when %s', (previous) => {
        if (previous === 'previously turned off') {
            localStorage.setItem('yansu.show-tool-steps', '1');
            // The previous build removed the key when the user turned it off.
            localStorage.removeItem('yansu.show-tool-steps');
        }
        localStorage.setItem('yansu.debug-mode-tools', '1');
        const { result, unmount } = renderHook(() => useShowToolSteps());
        expect(result.current[0]).toBe(false);
        expect(localStorage.getItem('yansu.show-tool-steps')).toBeNull();
        expect(localStorage.getItem('yansu.show-tool-steps-migrated')).toBe('1');

        expect(localStorage.getItem('yansu.debug-mode-tools')).toBe('1');
        unmount();
        const reopened = renderHook(() => useShowToolSteps());
        expect(reopened.result.current[0]).toBe(false);
    });

    it.each([
        ['0', '1', false],
        ['1', '0', true],
    ] as const)('preserves an explicit new value of %s during migration', (stored, legacy, expected) => {
        localStorage.setItem('yansu.show-tool-steps', stored);
        localStorage.setItem('yansu.debug-mode-tools', legacy);
        const { result } = renderHook(() => useShowToolSteps());
        expect(result.current[0]).toBe(expected);
        expect(localStorage.getItem('yansu.show-tool-steps')).toBe(stored);
        expect(localStorage.getItem('yansu.debug-mode-tools')).toBe(legacy);
        expect(localStorage.getItem('yansu.show-tool-steps-migrated')).toBe('1');
    });

    it('keeps a readable explicit choice when the migration marker cannot be saved', () => {
        localStorage.setItem('yansu.show-tool-steps', '1');
        const write = vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
            throw new Error('storage quota exceeded');
        });
        try {
            const { result } = renderHook(() => useShowToolSteps());
            expect(result.current[0]).toBe(true);
        } finally {
            write.mockRestore();
        }
    });

    it('does not inherit later debug changes after the initial migration', () => {
        const { result, unmount } = renderHook(() => useShowToolSteps());
        expect(result.current[0]).toBe(false);
        act(() => {
            localStorage.setItem('yansu.debug-mode-tools', '1');
            window.dispatchEvent(new StorageEvent('storage', { key: 'yansu.debug-mode-tools', newValue: '1' }));
        });
        expect(result.current[0]).toBe(false);
        unmount();
        const reopened = renderHook(() => useShowToolSteps());
        expect(reopened.result.current[0]).toBe(false);
    });

    it('restores the persisted value and synchronizes mounted consumers', () => {
        localStorage.setItem('yansu.show-tool-steps', '1');
        const first = renderHook(() => useShowToolSteps());
        const second = renderHook(() => useShowToolSteps());
        expect(first.result.current[0]).toBe(true);
        expect(second.result.current[0]).toBe(true);

        act(() => first.result.current[1](false));
        expect(second.result.current[0]).toBe(false);
        first.unmount();
        const reopened = renderHook(() => useShowToolSteps());
        expect(reopened.result.current[0]).toBe(false);
    });

    it('tracks preference changes from another window', () => {
        const { result } = renderHook(() => useShowToolSteps());
        act(() => {
            localStorage.setItem('yansu.show-tool-steps', '1');
            window.dispatchEvent(new StorageEvent('storage', { key: 'yansu.show-tool-steps', newValue: '1' }));
        });
        expect(result.current[0]).toBe(true);
    });

    it('controls the existing tool input/result display without enabling debug mode', () => {
        const { result } = renderHook(() => useShowToolSteps());
        render(
            <ToolGroupItem
                item={{ id: 'tool-1', kind: 'tool', toolName: 'Bash', toolInput: { command: 'pwd' }, toolUseId: 'call-1' }}
                index={0}
                result={{ id: 'result-1', kind: 'result', toolUseId: 'call-1', content: '/tmp/project' }}
            />,
        );
        expect(screen.queryByRole('button', { name: 'Show raw tool input and output' })).not.toBeInTheDocument();
        act(() => result.current[1](true));
        fireEvent.click(screen.getByRole('button', { name: 'Show raw tool input and output' }));
        expect(screen.getByText('/tmp/project')).toBeInTheDocument();
        expect(localStorage.getItem('yansu.debug-mode-tools')).toBeNull();
    });
});
