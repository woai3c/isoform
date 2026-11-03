import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { CallTranscript } from './call-transcript';
import type { MeetingCallTurnsView } from '@/lib/wails-shim-app';

const mocks = vi.hoisted(() => ({ get: vi.fn(), rename: vi.fn(), handlers: new Map<string, () => void>() }));
vi.mock('@/lib/wails-shim-app', () => ({ GetMeetingCallTurns: mocks.get, RenameMeetingSpeaker: mocks.rename }));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: (name: string, handler: () => void) => { mocks.handlers.set(name, handler); return () => mocks.handlers.delete(name); } }));
const start = Date.UTC(2026, 9, 19, 10);
const fixture = (): MeetingCallTurnsView => ({ status: 'ready', turns: [
    { speakerId: 'one:speaker-2', name: '', number: 2, source: 'system_audio', startTime: start, endTime: start + 4000, text: 'Please send the report.' },
    { speakerId: 'you', name: '', number: 0, source: 'microphone', startTime: start + 1000, endTime: start + 2000, text: 'I will.' },
    { speakerId: 'one:speaker-3', name: 'Sam', number: 3, source: 'system_audio', startTime: start + 3000, endTime: start + 5000, text: 'Thank you.' },
    { speakerId: 'one:speaker-2', name: '', number: 2, source: 'system_audio', startTime: start + 6000, endTime: start + 9000, text: 'That works for me.' },
] });

describe('Call conversation', () => {
    beforeEach(async () => { vi.clearAllMocks(); mocks.handlers.clear(); mocks.get.mockResolvedValue(fixture()); mocks.rename.mockResolvedValue(undefined); await i18n.changeLanguage('en'); });

    it('keeps chronological overlapping turns with You, numbered and named speakers', async () => {
        render(<CallTranscript meetingKey="call" sessionId="one" />);
        const list = await screen.findByRole('list', { name: 'Call conversation' });
        const rows = list.querySelectorAll('li');
        expect(rows).toHaveLength(4);
        expect(rows[0]).toHaveTextContent('Speaker 2');
        expect(rows[1]).toHaveTextContent('You');
        expect(rows[2]).toHaveTextContent('Sam');
        expect(rows[3]).toHaveTextContent('Speaker 2');
        expect(screen.queryByRole('button', { name: 'Rename You' })).not.toBeInTheDocument();
    });

    it('renames every turn of one speaker and explains local voice memory', async () => {
        render(<CallTranscript meetingKey="call" sessionId="one" />);
        fireEvent.click((await screen.findAllByRole('button', { name: 'Rename Speaker 2' }))[0]);
        expect(screen.getByText(/Voice signatures never go to a model, your phone or sync/)).toBeInTheDocument();
        fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Lin Chen' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save name' }));
        await waitFor(() => expect(mocks.rename).toHaveBeenCalledWith('call', 'one:speaker-2', 'Lin Chen'));
        expect(await screen.findAllByRole('button', { name: 'Rename Lin Chen' })).toHaveLength(2);
        expect(screen.getByRole('button', { name: 'Rename Sam' })).toBeInTheDocument();
    });

    it('keeps the dialog and old labels when rename fails', async () => {
        mocks.rename.mockRejectedValue(new Error('private'));
        render(<CallTranscript meetingKey="call" sessionId="one" />);
        fireEvent.click((await screen.findAllByRole('button', { name: 'Rename Speaker 2' }))[0]);
        fireEvent.change(screen.getByLabelText('Name'), { target: { value: 'Lin' } });
        fireEvent.click(screen.getByRole('button', { name: 'Save name' }));
        expect(await screen.findByRole('alert')).toHaveTextContent('The name could not be saved.');
        fireEvent.click(screen.getByRole('button', { name: 'Cancel' }));
        expect(screen.getAllByRole('button', { name: 'Rename Speaker 2' })).toHaveLength(2);
    });

    it('clears old turns immediately when privacy changes and ignores an older response', async () => {
        let complete!: (value: MeetingCallTurnsView) => void;
        mocks.get.mockReturnValueOnce(new Promise((resolve) => { complete = resolve; }));
        render(<CallTranscript meetingKey="call" sessionId="one" />);
        mocks.get.mockRejectedValue(new Error('private'));
        act(() => mocks.handlers.get('closed-eyes:changed')?.());
        expect(await screen.findByText("This transcript can't be shown.")).toBeInTheDocument();
        await act(async () => complete(fixture()));
        expect(screen.queryByText('Please send the report.')).not.toBeInTheDocument();
    });

    it('localizes numbered speakers and rename controls in Chinese', async () => {
        await i18n.changeLanguage('zh-CN');
        mocks.get.mockResolvedValue({ ...fixture(), status: 'unavailable' });
        render(<CallTranscript meetingKey="call" sessionId="one" />);
        expect(await screen.findAllByRole('button', { name: '重命名说话人 2' })).toHaveLength(2);
        expect(screen.getByText('你')).toBeInTheDocument();
        fireEvent.click(screen.getAllByRole('button', { name: '重命名说话人 2' })[0]);
        expect(screen.getByRole('button', { name: '保存姓名' })).toBeInTheDocument();
    });
});
