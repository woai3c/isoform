import { act, fireEvent, render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { AIMemoryImportDialog } from './ai-memory-import-dialog';

const mocks = vi.hoisted(() => ({ scan: vi.fn(), importFiles: vi.fn(), importText: vi.fn() }));
vi.mock('../../wailsjs/go/main/App', () => ({
    ScanAIMemories: mocks.scan,
    ImportAIMemories: mocks.importFiles,
    ImportAIMemoryText: mocks.importText,
}));

// About you v1.0.0 Y10: Import from AI tools copy, a wordless scan and a copper failure.
describe('AIMemoryImportDialog', () => {
    beforeEach(async () => {
        vi.resetAllMocks();
        await i18n.changeLanguage('en');
    });

    it.each([
        ['en', 'Import from AI tools', "Couldn't look for files from other AI tools.", 'No files from other AI tools on this computer.'],
        ['zh-CN', '从 AI 工具导入', '没能查找其他 AI 工具的文件。', '这台电脑上没有其他 AI 工具的文件。'],
    ])('says a failed look in copper with Try again, then none found, in %s', async (language, title, failed, none) => {
        await i18n.changeLanguage(language);
        mocks.scan.mockRejectedValueOnce(new Error('denied'));
        render(<AIMemoryImportDialog open onOpenChange={vi.fn()} onImported={vi.fn()} />);
        expect(await screen.findByText(title)).toBeInTheDocument();
        expect(screen.queryByText(/Scanning|正在扫描/)).not.toBeInTheDocument();
        expect((await screen.findByText(failed)).closest('[role="alert"]')).toHaveClass('text-copper');
        mocks.scan.mockResolvedValueOnce([]);
        await act(async () => { fireEvent.click(screen.getByRole('button', { name: i18n.t('settings:aiMemoryImportDialog.retry') })); });
        expect(mocks.scan).toHaveBeenCalledTimes(2);
        expect(await screen.findByText(none)).toBeInTheDocument();
        expect(screen.queryByText(failed)).not.toBeInTheDocument();
    });
});
