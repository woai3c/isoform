import { act, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import type { MeetingNotesView } from '@/lib/wails-shim-app';
import { setHostBridgeMediaToken } from '@/lib/host-bridge-media';
import { ActivityMeetingNotes, transcriptBody } from './activity-meeting-notes';

const mocks = vi.hoisted(() => ({
    getNotes: vi.fn(),
    getTranscript: vi.fn(),
    reveal: vi.fn(),
    deleteNotes: vi.fn(),
    handlers: new Map<string, Set<() => void>>(),
}));

vi.mock('@/lib/wails-shim-app', () => ({
    GetMeetingNotesForSessions: mocks.getNotes,
    GetMeetingCallTurns: mocks.getTranscript,
    RenameMeetingSpeaker: vi.fn(),
    RevealMeetingRecording: mocks.reveal,
    DeleteMeetingNotes: mocks.deleteNotes,
}));

vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (name: string, handler: () => void) => {
        const handlers = mocks.handlers.get(name) ?? new Set();
        handlers.add(handler);
        mocks.handlers.set(name, handlers);
        return () => handlers.delete(handler);
    },
}));

vi.mock('@/components/markdown-content', () => ({
    MarkdownContent: ({ content }: { content: string }) => <div data-testid="markdown">{content}</div>,
}));

vi.mock('sonner', () => ({ toast: { error: vi.fn(), success: vi.fn() } }));

const start = new Date(2026, 9, 20, 14, 30).getTime();

function meeting(overrides: Partial<MeetingNotesView> = {}): MeetingNotesView {
    return {
        meetingKey: 'meeting-1',
        meetingApp: 'Halcyon Meet',
        title: 'Halcyon studio review',
        startTime: start,
        endTime: start + 45 * 60_000,
        summary: 'We reviewed the studio floor plan and kept the welcome tour.',
        decisions: ['Keep the welcome tour'],
        followups: [
            { text: 'Send the revised floor plan', owner: 'me', due: 'Tuesday' },
            { text: 'Check the welcome copy', owner: 'other', person: 'Maya Chen' },
            { text: 'Book the next review', owner: 'unknown' },
        ],
        userNotes: 'Ask about opening hours.',
        parts: [{ sessionId: 'session-1', startTime: start, endTime: start + 45 * 60_000, wordCount: 1200, hasTranscript: true, hasRecording: true }],
        ...overrides,
    };
}

describe('Meeting notes on Activity', () => {
    beforeEach(async () => {
        vi.clearAllMocks();
        mocks.handlers.clear();
        mocks.getNotes.mockResolvedValue([meeting()]);
        mocks.getTranscript.mockResolvedValue({ status: 'ready', turns: [{ speakerId: 'you', name: '', number: 0, source: 'microphone', startTime: start + 1000, endTime: start + 2000, text: 'Let us start.' }] });
        mocks.reveal.mockResolvedValue(undefined);
        mocks.deleteNotes.mockResolvedValue(undefined);
        setHostBridgeMediaToken('media-token');
        await i18n.changeLanguage('en');
    });

    it('shows the summary, decisions, follow-ups with their owners, and the user\'s own notes', async () => {
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        expect(await screen.findByText('Meeting notes')).toBeInTheDocument();
        expect(mocks.getNotes).toHaveBeenCalledWith(['session-1']);
        expect(screen.getByRole('heading', { name: 'Halcyon studio review' })).toBeInTheDocument();
        expect(screen.getByText(/45 min · 1200 words/)).toBeInTheDocument();
        expect(screen.getByText('We reviewed the studio floor plan and kept the welcome tour.')).toBeInTheDocument();
        expect(screen.getByText('Keep the welcome tour')).toBeInTheDocument();
        expect(screen.getByText('You')).toBeInTheDocument();
        expect(screen.getByText('Send the revised floor plan')).toBeInTheDocument();
        expect(screen.getByText('· due Tuesday')).toBeInTheDocument();
        expect(screen.getByText('Maya Chen')).toBeInTheDocument();
        expect(screen.getByText('Someone')).toBeInTheDocument();
        expect(screen.getByText('Your notes')).toBeInTheDocument();
        expect(screen.getByTestId('markdown')).toHaveTextContent('Ask about opening hours.');
        expect(screen.getByText('Isoform learns from these notes. They stay with the call, not in Knowledge.')).toBeInTheDocument();
    });

    it('names an untitled call by its app and says when there is no summary yet', async () => {
        mocks.getNotes.mockResolvedValue([meeting({ title: '', summary: '', decisions: [], followups: [], userNotes: '' })]);
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        expect(await screen.findByRole('heading', { name: 'Halcyon Meet call' })).toBeInTheDocument();
        expect(screen.getByText('No summary for this call yet.')).toBeInTheDocument();
        expect(screen.queryByText('Your notes')).not.toBeInTheDocument();
    });

    it('collapses and expands the notes', async () => {
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        const toggle = await screen.findByRole('button', { name: 'Hide these notes' });
        expect(toggle).toHaveAttribute('aria-expanded', 'true');
        fireEvent.click(toggle);
        expect(screen.queryByText('Keep the welcome tour')).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Show these notes' }));
        expect(screen.getByText('Keep the welcome tour')).toBeInTheDocument();
    });

    it('opens the full transcript on demand, without its header', async () => {
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Show transcript' }));
        expect(mocks.getTranscript).toHaveBeenCalledWith('session-1');
        expect(await screen.findByText('Let us start.')).toBeInTheDocument();
        expect(screen.queryByText(/Session/)).not.toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Hide transcript' }));
        expect(screen.queryByText('Let us start.')).not.toBeInTheDocument();
    });

    it('says so when the transcript cannot be read', async () => {
        mocks.getTranscript.mockRejectedValue(new Error('transcript source is not readable'));
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Show transcript' }));
        expect(await screen.findByText("This transcript can't be shown.")).toBeInTheDocument();
    });

    it('plays the recording through the media route and offers the file manager when it cannot play', async () => {
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        const player = await screen.findByLabelText('Call recording');
        expect(player).toHaveAttribute('src', 'http://127.0.0.1:4242/api/activity/audio?session=session-1&token=media-token');
        fireEvent.error(player);
        expect(screen.getByText("This recording can't play here.")).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: /^Show in / }));
        await waitFor(() => expect(mocks.reveal).toHaveBeenCalledWith('session-1'));
    });

    it('labels each part of a rejoined call', async () => {
        mocks.getNotes.mockResolvedValue([meeting({
            parts: [
                { sessionId: 'session-1', startTime: start, endTime: start + 10 * 60_000, wordCount: 400, hasTranscript: true, hasRecording: false },
                { sessionId: 'session-2', startTime: start + 15 * 60_000, endTime: start + 30 * 60_000, wordCount: 800, hasTranscript: true, hasRecording: false },
            ],
        })]);
        render(<ActivityMeetingNotes sessionIds={['session-2']} bridgePort={4242} />);
        expect(await screen.findByText(/^Part 1, /)).toBeInTheDocument();
        expect(screen.getByText(/^Part 2, /)).toBeInTheDocument();
        expect(screen.getAllByRole('button', { name: 'Show transcript' })).toHaveLength(2);
        expect(screen.queryByLabelText('Call recording')).not.toBeInTheDocument();
    });

    it('leaves out parts a recording only spans', async () => {
        mocks.getNotes.mockResolvedValue([meeting({
            parts: [
                { sessionId: 'session-0', startTime: start, endTime: start + 5 * 60_000, wordCount: 0, hasTranscript: false, hasRecording: false },
                { sessionId: 'session-1', startTime: start + 5 * 60_000, endTime: start + 30 * 60_000, wordCount: 1200, hasTranscript: true, hasRecording: true },
            ],
        })]);
        render(<ActivityMeetingNotes sessionIds={['session-0']} bridgePort={4242} />);
        expect(await screen.findByRole('button', { name: 'Show transcript' })).toBeInTheDocument();
        expect(screen.queryByText(/^Part /)).not.toBeInTheDocument();
        expect(screen.getAllByLabelText('Call recording')).toHaveLength(1);
    });

    it('renders nothing when no meeting has readable notes, and asks nothing without sessions', async () => {
        mocks.getNotes.mockResolvedValue([]);
        const { container, rerender } = render(<ActivityMeetingNotes sessionIds={['session-9']} bridgePort={4242} />);
        await waitFor(() => expect(mocks.getNotes).toHaveBeenCalledWith(['session-9']));
        expect(container).toBeEmptyDOMElement();
        mocks.getNotes.mockClear();
        rerender(<ActivityMeetingNotes sessionIds={[]} bridgePort={4242} />);
        expect(mocks.getNotes).not.toHaveBeenCalled();
        expect(container).toBeEmptyDOMElement();
    });

    it('keeps the meeting section and retries a failed load', async () => {
        mocks.getNotes.mockRejectedValueOnce(new Error('offline')).mockResolvedValue([meeting()]);
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        expect(await screen.findByText("Couldn't load meeting notes.")).toBeInTheDocument();
        expect(screen.getByText('Meeting notes')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
        expect(await screen.findByText('Halcyon studio review')).toBeInTheDocument();
    });

    it('keeps notes and retries a failed delete in place', async () => {
        mocks.deleteNotes.mockRejectedValueOnce(new Error('offline')).mockResolvedValue(undefined);
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Delete notes and recording' }));
        fireEvent.click(await screen.findByRole('button', { name: 'Delete' }));
        expect(await screen.findByText("Couldn't delete these notes.")).toBeInTheDocument();
        expect(screen.getByText('Halcyon studio review')).toBeInTheDocument();
        fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
        await waitFor(() => expect(screen.queryByText('Meeting notes')).not.toBeInTheDocument());
    });

    it('deletes the notes and the recording after the user confirms', async () => {
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Delete notes and recording' }));
        expect(await screen.findByText("Delete this call's notes?")).toBeInTheDocument();
        expect(mocks.deleteNotes).not.toHaveBeenCalled();
        fireEvent.click(screen.getByRole('button', { name: 'Delete' }));
        await waitFor(() => expect(mocks.deleteNotes).toHaveBeenCalledWith('meeting-1'));
        await waitFor(() => expect(screen.queryByText('Meeting notes')).not.toBeInTheDocument());
    });

    it('keeps the notes when the user cancels the delete', async () => {
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        fireEvent.click(await screen.findByRole('button', { name: 'Delete notes and recording' }));
        fireEvent.click(await screen.findByRole('button', { name: 'Cancel' }));
        expect(mocks.deleteNotes).not.toHaveBeenCalled();
        expect(screen.getByText('Meeting notes')).toBeInTheDocument();
    });

    it('hides the notes when pausing observation makes them unreadable', async () => {
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        expect(await screen.findByText('Meeting notes')).toBeInTheDocument();
        mocks.getNotes.mockResolvedValue([]);
        act(() => {
            mocks.handlers.get('closed-eyes:changed')?.forEach((handler) => handler());
        });
        await waitFor(() => expect(screen.queryByText('Meeting notes')).not.toBeInTheDocument());
        mocks.getNotes.mockResolvedValue([meeting()]);
        act(() => {
            window.dispatchEvent(new Event('focus'));
        });
        expect(await screen.findByText('Meeting notes')).toBeInTheDocument();
    });

    it('reloads when notes are saved for a call', async () => {
        mocks.getNotes.mockResolvedValueOnce([]);
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        await waitFor(() => expect(mocks.getNotes).toHaveBeenCalledTimes(1));
        expect(screen.queryByText('Meeting notes')).not.toBeInTheDocument();
        act(() => {
            mocks.handlers.get('meeting-notes:saved')?.forEach((handler) => handler());
        });
        expect(await screen.findByText('Meeting notes')).toBeInTheDocument();
    });

    it('shows the notes in Chinese', async () => {
        await i18n.changeLanguage('zh-CN');
        render(<ActivityMeetingNotes sessionIds={['session-1']} bridgePort={4242} />);
        expect(await screen.findByText('会议纪要')).toBeInTheDocument();
        expect(screen.getByText('摘要')).toBeInTheDocument();
        expect(screen.getByText('后续事项')).toBeInTheDocument();
        expect(screen.getByText('你')).toBeInTheDocument();
        expect(screen.getByRole('button', { name: '显示转写' })).toBeInTheDocument();
    });

    it('strips the transcript header', () => {
        expect(transcriptBody('# T\n\n- **Date**: x\n\n## Transcript\n\n[10:00:00] Hi.\n')).toBe('[10:00:00] Hi.');
        expect(transcriptBody('[10:00:00] Hi.')).toBe('[10:00:00] Hi.');
    });
});
