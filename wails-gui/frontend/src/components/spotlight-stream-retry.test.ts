import { afterEach, expect, it, vi } from 'vitest';
import { waitFor } from '@testing-library/react';
import { subscribeStream } from '../../../quick-chat-spotlight/frontend/src/api/yansu';

const stream = (body: string) => new Response(new ReadableStream<Uint8Array>({
    start(controller) { controller.enqueue(new TextEncoder().encode(body)); controller.close(); },
}), { status: 200 });

afterEach(() => vi.unstubAllGlobals());

it('reports EOF and resumes after the last server event', async () => {
    vi.stubGlobal('__spotlightConfig', { yansuPort: 4242, token: 'fixture' });
    const fetchMock = vi.fn().mockResolvedValueOnce(stream('id: 1\nevent: agent_event\ndata: {"type":"assistant","content":"one"}\n\n'))
        .mockResolvedValueOnce(stream('id: 2\nevent: agent_event\ndata: {"type":"assistant","content":"two"}\n\n'));
    vi.stubGlobal('fetch', fetchMock);
    const events = vi.fn();
    const disconnected = vi.fn();
    subscribeStream('chat', 0, events, disconnected);
    await waitFor(() => expect(disconnected).toHaveBeenCalledTimes(1));
    expect(events.mock.calls[0][1]).toBe(1);
    subscribeStream('chat', 1, events, disconnected);
    await waitFor(() => expect(disconnected).toHaveBeenCalledTimes(2));
    expect(events.mock.calls[1][1]).toBe(2);
    expect(String(fetchMock.mock.calls[1][0])).toContain('?after=1');
});

it('reports an expired server cursor for snapshot recovery', async () => {
    vi.stubGlobal('__spotlightConfig', { yansuPort: 4242, token: 'fixture' });
    vi.stubGlobal('fetch', vi.fn().mockResolvedValueOnce(new Response('', { status: 409 })));
    const events = vi.fn();
    const disconnected = vi.fn();
    subscribeStream('chat', 1, events, disconnected);
    await waitFor(() => expect(disconnected).toHaveBeenCalledWith(true));
    expect(events).not.toHaveBeenCalled();
});
