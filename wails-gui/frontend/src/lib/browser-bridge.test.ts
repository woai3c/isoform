import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

type BridgeModule = typeof import('./browser-bridge');

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  });
}

class FakeWebSocket {
  static CONNECTING = 0;
  static OPEN = 1;
  static CLOSING = 2;
  static CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  readyState = FakeWebSocket.CONNECTING;
  onopen: (() => void) | null = null;
  onmessage: ((event: { data: string }) => void) | null = null;
  onclose: (() => void) | null = null;
  onerror: (() => void) | null = null;

  constructor(public url: string) {
    FakeWebSocket.instances.push(this);
  }

  send(): void {}
}

describe('browser bridge transport', () => {
  let bridge: BridgeModule;
  let fetchMock: ReturnType<typeof vi.fn>;
  let tokens: string[];

  beforeEach(async () => {
    vi.resetModules();
    FakeWebSocket.instances = [];
    vi.stubGlobal('WebSocket', FakeWebSocket);
    tokens = ['token-1', 'token-2'];
    fetchMock = vi.fn();
    vi.stubGlobal('fetch', fetchMock);
    bridge = await import('./browser-bridge');
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  function tokenCalls() {
    return fetchMock.mock.calls.filter(([url]) => String(url).endsWith('/api/bridge-token'));
  }

  function rpcCalls() {
    return fetchMock.mock.calls.filter(([url]) => String(url).endsWith('/api/call'));
  }

  it('reads the token once and sends it with JSON on every call', async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.endsWith('/api/bridge-token')) return jsonResponse(200, { ok: true, data: tokens[0] });
      return jsonResponse(200, { ok: true, data: 'v1' });
    });

    await expect(bridge.callAppMethod('GetVersion')).resolves.toBe('v1');
    await expect(bridge.callAppMethod('GetVersion')).resolves.toBe('v1');

    expect(tokenCalls()).toHaveLength(1);
    const [, tokenInit] = tokenCalls()[0];
    expect(tokenInit.headers).toEqual({ 'X-Yansu-Bridge-Token-Request': '1' });
    expect(rpcCalls()).toHaveLength(2);
    for (const [, init] of rpcCalls()) {
      expect(init.method).toBe('POST');
      expect(init.headers).toEqual({
        'Content-Type': 'application/json',
        'X-Yansu-Bridge-Token': 'token-1',
      });
      expect(JSON.parse(init.body)).toEqual({ method: 'GetVersion', args: [] });
    }
  });

  it('reads a new token and retries once when the bridge refuses the old one', async () => {
    let tokenIndex = 0;
    fetchMock.mockImplementation(async (url: string, init: RequestInit) => {
      if (url.endsWith('/api/bridge-token')) {
        return jsonResponse(200, { ok: true, data: tokens[tokenIndex++] });
      }
      const headers = init.headers as Record<string, string>;
      if (headers['X-Yansu-Bridge-Token'] !== 'token-2') {
        return jsonResponse(401, { ok: false, error: 'bridge token missing or wrong' });
      }
      return jsonResponse(200, { ok: true, data: 'after restart' });
    });

    await expect(bridge.callAppMethod('GetVersion')).resolves.toBe('after restart');
    expect(tokenCalls()).toHaveLength(2);
    expect(rpcCalls()).toHaveLength(2);
  });

  it('gives up after one retry', async () => {
    fetchMock.mockImplementation(async (url: string) => {
      if (url.endsWith('/api/bridge-token')) return jsonResponse(200, { ok: true, data: 'stale' });
      return jsonResponse(401, { ok: false, error: 'bridge token missing or wrong' });
    });

    await expect(bridge.callAppMethod('GetVersion')).rejects.toThrow('bridge token missing or wrong');
    expect(rpcCalls()).toHaveLength(2);
  });

  it('explains how to reach the bridge when the token cannot be read', async () => {
    fetchMock.mockRejectedValue(new TypeError('Failed to fetch'));

    await expect(bridge.callAppMethod('GetVersion')).rejects.toThrow(
      /Browser access is unavailable.*YANSU_BROWSER_BRIDGE_ORIGINS.*Failed to fetch/,
    );
    expect(rpcCalls()).toHaveLength(0);

    // The failed read is not cached.
    fetchMock.mockImplementation(async (url: string) => {
      if (url.endsWith('/api/bridge-token')) return jsonResponse(200, { ok: true, data: 'token-1' });
      return jsonResponse(200, { ok: true, data: 'ok' });
    });
    await expect(bridge.callAppMethod('GetVersion')).resolves.toBe('ok');
  });

  it('opens the event socket with the token in the query', async () => {
    fetchMock.mockImplementation(async () => jsonResponse(200, { ok: true, data: 'a b&c' }));

    const received: unknown[] = [];
    bridge.onEvent('agent:status', (...args) => received.push(args));
    await vi.waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));

    const socket = FakeWebSocket.instances[0];
    expect(socket.url).toBe('ws://localhost:34116/ws?token=a%20b%26c');
    socket.readyState = FakeWebSocket.OPEN;
    socket.onopen?.();
    socket.onmessage?.({ data: JSON.stringify({ event: 'agent:status', args: ['ready'] }) });
    expect(received).toEqual([['ready']]);
  });
});
