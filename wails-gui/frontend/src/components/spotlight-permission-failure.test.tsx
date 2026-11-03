import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, expect, it, vi } from 'vitest';
import { ConversationPanel } from '../../../quick-chat-spotlight/frontend/src/components/ConversationPanel';
import type { StreamEvent } from '../../../quick-chat-spotlight/frontend/src/api/types';

const mocks = vi.hoisted(() => ({ subscribe: vi.fn(), respond: vi.fn(), submit: vi.fn(), snapshot: vi.fn() }));
vi.mock('../../../quick-chat-spotlight/frontend/src/api/yansu', () => ({
    subscribeStream: mocks.subscribe, fetchSpotlightSnapshot: mocks.snapshot, respondToPermission: mocks.respond,
    submitPrompt: mocks.submit, newSpotlightRequestId: () => 'stable-edit-request', dismiss: vi.fn(), cancel: vi.fn(), expand: vi.fn(),
}));
vi.mock('@/components/grouped-stream-list', () => ({ GroupedStreamList: ({ items, onEditMessage }: { items: { id: string; content?: string; sortOrder?: number; kind: string }[]; onEditMessage: (id: string, order: number, content: string) => void }) => <div>{items.map((item) => <p key={item.id}>{item.content}</p>)}{items[0]?.sortOrder !== undefined && <button onClick={() => onEditMessage(items[0].id, items[0].sortOrder!, 'Edited request')}>Edit fixture</button>}</div> }));
vi.mock('@/components/permission-question-card', async () => {
    const { useState } = await import('react');
    return { PermissionQuestionCard: ({ onSelect }: { onSelect: (id: string) => unknown }) => {
        const [failed, setFailed] = useState(false);
        return <><button onClick={async () => setFailed((await onSelect('allow')) === false)}>Allow</button>{failed && <p role="alert">failed</p>}</>;
    } };
});
vi.mock('@/components/typing-indicator', () => ({ TypingIndicator: () => null }));
vi.mock('@/components/voice-recording-indicator', () => ({ VoiceRecordingIndicator: () => null }));
vi.mock('@/hooks/useVoiceInput', () => ({ useVoiceInput: () => ({ state: 'idle' }) }));
vi.mock('../../../quick-chat-spotlight/frontend/src/lib/wails-app-stub', () => ({ HideWindow: vi.fn() }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

it('keeps a failed permission decision visible and retries it', async () => {
    Element.prototype.scrollIntoView = vi.fn();
    let emit!: (event: StreamEvent, sequence: number) => void;
    mocks.subscribe.mockImplementation((_id, _after, callback) => { emit = callback; return () => {}; });
    mocks.respond.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce(undefined);
    render(<ConversationPanel detection={null} userPrompt="" sessionLocalId="spotlight" onClose={() => {}} />);
    act(() => emit({ kind: 'agent_event', data: { type: 'permission_request', session_id: 'agent', permission_options: [{ id: 'allow', name: 'Allow', kind: 'allow_once' }] } }, 1));
    fireEvent.click(screen.getByText('Allow'));
    await waitFor(() => expect(screen.getByRole('alert')).toBeInTheDocument());
    expect(screen.getByText('Allow')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Allow'));
    await waitFor(() => expect(screen.queryByText('Allow')).not.toBeInTheDocument());
    expect(mocks.respond).toHaveBeenCalledTimes(2);
});

it('shows a dropped stream and retries from the last received event', async () => {
    let emit!: (event: StreamEvent, sequence: number) => void;
    let disconnected!: (historyUnavailable: boolean) => void;
    mocks.subscribe.mockImplementation((_id, _after, callback, onDisconnect) => { emit = callback; disconnected = onDisconnect; return () => {}; });
    render(<ConversationPanel detection={null} userPrompt="Keep this" sessionLocalId="spotlight" onClose={() => {}} />);
    act(() => emit({ kind: 'agent_event', data: { type: 'run_created', run_id: 'run' } }, 1));
    act(() => disconnected(false));
    expect(screen.getByRole('alert')).toHaveTextContent('The answer stopped.');
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(mocks.subscribe).toHaveBeenCalledTimes(2));
    expect(mocks.subscribe.mock.calls[1][1]).toBe(1);
    expect(screen.queryByRole('alert')).not.toBeInTheDocument();
});

it('keeps history on failed edit and reconciles buffered output on retry', async () => {
    let emit!: (event: StreamEvent, sequence: number) => void;
    mocks.submit.mockReset().mockRejectedValueOnce(new Error('response lost')).mockResolvedValueOnce({ sessionLocalId: 'spotlight' });
    mocks.subscribe.mockImplementation((_id, _after, callback) => { emit = callback; return () => {}; });
    render(<ConversationPanel detection={null} userPrompt="Original" firstRequestId="local-user" firstUserAck={{ clientId: 'local-user', itemId: 'server-user', sortOrder: 0 }} sessionLocalId="spotlight" onClose={() => {}} />);
    act(() => {
        emit({ kind: 'agent_event', data: { type: 'assistant', content: 'Original answer' } }, 1);
        emit({ kind: 'agent_event', data: { type: 'done' } }, 2);
    });
    expect(screen.getByText('Original answer')).toBeInTheDocument();
    fireEvent.click(screen.getByText('Edit fixture'));
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent("Couldn't confirm it sent"));
    expect(screen.getByText('Original')).toBeInTheDocument();
    expect(screen.getByText('Original answer')).toBeInTheDocument();
    act(() => {
        emit({ kind: 'assistant_started', data: {} }, 3);
        emit({ kind: 'agent_event', data: { type: 'assistant', content: 'New answer' } }, 4);
        emit({ kind: 'agent_event', data: { type: 'done' } }, 5);
    });
    expect(screen.queryByText('New answer')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(screen.getByText('Edited request')).toBeInTheDocument());
    expect(screen.getByText('New answer')).toBeInTheDocument();
    expect(screen.queryByText('Original answer')).not.toBeInTheDocument();
    expect(mocks.submit.mock.calls.map((call) => call[4])).toEqual(['stable-edit-request', 'stable-edit-request']);
    expect(mocks.submit.mock.calls[0][3]).toEqual({ itemId: 'server-user', sortOrder: 0 });
});

it('retains a failed follow-up and a newer draft while retrying the same request', async () => {
    mocks.submit.mockReset().mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ sessionLocalId: 'spotlight', userItemId: 'server-followup', sortOrder: 0 });
    mocks.subscribe.mockImplementation(() => () => {});
    render(<ConversationPanel detection={null} userPrompt="" sessionLocalId="spotlight" onClose={() => {}} />);
    const input = screen.getByPlaceholderText('Refine or confirm…');
    fireEvent.change(input, { target: { value: 'Next request' } });
    fireEvent.keyDown(input, { key: 'Enter' });
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent("Couldn't confirm it sent"));
    expect(screen.getByText('Next request')).toBeInTheDocument();
    fireEvent.change(input, { target: { value: 'Another draft' } });
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(input).toHaveValue('Another draft');
    expect(screen.getAllByText('Next request')).toHaveLength(1);
    expect(mocks.submit.mock.calls.map((call) => call[4])).toEqual(['stable-edit-request', 'stable-edit-request']);
});

it('recovers an expired stream cursor from the server snapshot and keeps the draft', async () => {
    let disconnected!: (historyUnavailable: boolean) => void;
    mocks.subscribe.mockImplementation((_id, _after, _callback, onDisconnect) => { disconnected = onDisconnect; return () => {}; });
    mocks.snapshot.mockReset().mockResolvedValueOnce({
        items: [{ id: 'server-user', kind: 'user', content: 'Stored request', sortOrder: 0 }, { id: 'server-answer', kind: 'assistant', content: 'Recovered response', sortOrder: 1 }],
        sequence: 92, streaming: true,
        permission: { type: 'permission_request', event_id: 'permission-1', session_id: 'agent', permission_options: [{ id: 'allow', name: 'Allow', kind: 'allow_once' }] },
    });
    render(<ConversationPanel detection={null} userPrompt="" sessionLocalId="spotlight" onClose={() => {}} />);
    const input = screen.getByPlaceholderText('Refine or confirm…');
    fireEvent.change(input, { target: { value: 'My unsent draft' } });
    act(() => disconnected(true));
    expect(screen.getByRole('alert')).toHaveTextContent("Couldn't load this chat.");
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(screen.getByText('Recovered response')).toBeInTheDocument());
    expect(input).toHaveValue('My unsent draft');
    expect(screen.getByText('Allow')).toBeInTheDocument();
    await waitFor(() => expect(mocks.subscribe).toHaveBeenCalledTimes(2));
    expect(mocks.subscribe.mock.calls[1][1]).toBe(92);
});

it('does not clear a newer permission when an earlier response succeeds', async () => {
    let emit!: (event: StreamEvent, sequence: number) => void;
    let finish!: () => void;
    mocks.subscribe.mockImplementation((_id, _after, callback) => { emit = callback; return () => {}; });
    mocks.respond.mockReset().mockReturnValue(new Promise<void>((resolve) => { finish = resolve; }));
    render(<ConversationPanel detection={null} userPrompt="" sessionLocalId="spotlight" onClose={() => {}} />);
    act(() => emit({ kind: 'agent_event', data: { type: 'permission_request', event_id: 'first', session_id: 'agent', permission_options: [] } }, 1));
    fireEvent.click(screen.getByText('Allow'));
    act(() => emit({ kind: 'agent_event', data: { type: 'permission_request', event_id: 'second', session_id: 'agent', permission_options: [] } }, 2));
    await act(async () => finish());
    expect(screen.getByText('Allow')).toBeInTheDocument();
});

it('clears only the permission whose tool result arrived', () => {
    let emit!: (event: StreamEvent, sequence: number) => void;
    mocks.subscribe.mockImplementation((_id, _after, callback) => { emit = callback; return () => {}; });
    render(<ConversationPanel detection={null} userPrompt="" sessionLocalId="spotlight" onClose={() => {}} />);
    act(() => emit({ kind: 'agent_event', data: { type: 'permission_request', event_id: 'request', session_id: 'agent', tool_use_id: 'tool-1', permission_options: [] } }, 1));
    act(() => emit({ kind: 'agent_event', data: { type: 'tool_result', tool_use_id: 'another' } }, 2));
    expect(screen.getByText('Allow')).toBeInTheDocument();
    act(() => emit({ kind: 'agent_event', data: { type: 'tool_result', tool_use_id: 'tool-1' } }, 3));
    expect(screen.queryByText('Allow')).not.toBeInTheDocument();
});
