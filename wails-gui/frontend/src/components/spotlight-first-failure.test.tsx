import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, expect, it, vi } from 'vitest';
import i18n from '@/i18n';
import { App } from '../../../quick-chat-spotlight/frontend/src/App';

const mocks = vi.hoisted(() => ({ submit: vi.fn() }));
vi.mock('../../../quick-chat-spotlight/frontend/src/api/yansu', () => ({
    fetchAppearance: vi.fn(async () => null), fetchSuggestions: vi.fn(async () => []),
    newSpotlightRequestId: () => 'same-request', submitPrompt: mocks.submit,
    subscribeStream: () => () => {}, respondToPermission: vi.fn(), dismiss: vi.fn(), cancel: vi.fn(), expand: vi.fn(),
}));
vi.mock('../../../quick-chat-spotlight/frontend/src/lib/spotlight-runtime', () => ({ EventsOn: () => () => {} }));
vi.mock('../../../quick-chat-spotlight/frontend/src/lib/wails-app-stub', () => ({
    ResizeMode: vi.fn(async () => {}), SetAutoHideOnBlur: vi.fn(async () => {}),
    SetDarkMode: vi.fn(async () => {}), SetSurfaceColor: vi.fn(async () => {}), HideWindow: vi.fn(async () => {}),
}));
vi.mock('../../../quick-chat-spotlight/frontend/src/components/PillInput', () => ({
    PillInput: ({ onSubmit }: { onSubmit: (prompt: string) => void }) => <button onClick={() => onSubmit('Keep my prompt')}>Send prompt</button>,
}));
vi.mock('@/components/grouped-stream-list', () => ({ GroupedStreamList: ({ items }: { items: { id: string; content?: string }[] }) => <div>{items.map((item) => <p data-item-id={item.id} key={item.id}>{item.content}</p>)}</div> }));
vi.mock('@/components/permission-question-card', () => ({ PermissionQuestionCard: () => null }));
vi.mock('@/components/typing-indicator', () => ({ TypingIndicator: () => <span>Waiting</span> }));
vi.mock('@/components/voice-recording-indicator', () => ({ VoiceRecordingIndicator: () => null }));
vi.mock('@/hooks/useVoiceInput', () => ({ useVoiceInput: () => ({ state: 'idle' }) }));

beforeEach(async () => {
    await i18n.changeLanguage('en');
    Element.prototype.scrollIntoView = vi.fn();
    mocks.submit.mockReset();
});
afterEach(() => cleanup());

it('keeps the first prompt and retries the same request after a lost response', async () => {
    mocks.submit.mockRejectedValueOnce(new Error('connection lost')).mockResolvedValueOnce({ sessionLocalId: 'existing-session', userItemId: 'server-user', sortOrder: 0 });
    render(<App />);
    fireEvent.click(screen.getByText('Send prompt'));
    await waitFor(() => expect(screen.getByRole('alert')).toHaveTextContent("Couldn't confirm it sent"));
    expect(screen.getByText('Keep my prompt')).toBeInTheDocument();
    expect(screen.queryByText('Waiting')).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Try again' }));
    await waitFor(() => expect(mocks.submit).toHaveBeenCalledTimes(2));
    expect(mocks.submit.mock.calls.map((call) => call[4])).toEqual(['same-request', 'same-request']);
    await waitFor(() => expect(screen.queryByRole('alert')).not.toBeInTheDocument());
    expect(screen.getAllByText('Keep my prompt')).toHaveLength(1);
    expect(screen.getByText('Keep my prompt')).toHaveAttribute('data-item-id', 'server-user');
});
