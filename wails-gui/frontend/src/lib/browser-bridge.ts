// browser-bridge.ts
// Detects Wails vs browser environment and provides appropriate implementations.
// In browser mode, App method calls go to HTTP REST endpoints on localhost:34116
// and events stream via WebSocket. Both carry the bridge token, which the
// desktop app generates on each launch of the bridge.

const BRIDGE_PORT = 34116;
const BRIDGE_URL = `http://localhost:${BRIDGE_PORT}`;
const WS_URL = `ws://localhost:${BRIDGE_PORT}/ws`;
// Header names match browser_bridge_guard.go.
const TOKEN_HEADER = 'X-Yansu-Bridge-Token';
const TOKEN_REQUEST_HEADER = 'X-Yansu-Bridge-Token-Request';

/**
 * Returns true when running inside a Wails WebView (native desktop),
 * false when running in a regular browser.
 */
export function isWailsEnvironment(): boolean {
  if ((window as any).__yansuBrowserProxy === true) return false;
  return (
    typeof (window as any).__wails__ !== 'undefined' ||
    typeof (window as any).go !== 'undefined'
  );
}

// ---------------------------------------------------------------------------
// Bridge token
// ---------------------------------------------------------------------------

let tokenPromise: Promise<string> | null = null;

/**
 * Reads the bridge token from GET /api/bridge-token and caches it. The
 * custom request header makes the browser send a CORS preflight, which the
 * bridge answers only for its own origin and the Vite dev and preview servers.
 */
function getBridgeToken(): Promise<string> {
  if (!tokenPromise) {
    const pending = fetch(`${BRIDGE_URL}/api/bridge-token`, {
      headers: { [TOKEN_REQUEST_HEADER]: '1' },
      cache: 'no-store',
    }).then(async (res) => {
      const json = await res.json().catch(() => null);
      if (!res.ok || !json?.ok || typeof json.data !== 'string') {
        throw new Error(json?.error || `bridge token request failed (${res.status})`);
      }
      return json.data as string;
    });
    tokenPromise = pending;
    pending.catch(() => {
      if (tokenPromise === pending) tokenPromise = null;
    });
  }
  return tokenPromise;
}

// ---------------------------------------------------------------------------
// HTTP bridge for App method calls
// ---------------------------------------------------------------------------

async function postCall(method: string, args: any[]): Promise<Response> {
  try {
    const token = await getBridgeToken();
    return await fetch(`${BRIDGE_URL}/api/call`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', [TOKEN_HEADER]: token },
      body: JSON.stringify({ method, args }),
    });
  } catch (err) {
    // Most common causes when in a plain browser: the bridge is disabled in
    // the desktop app's settings, so nothing is listening on port 34116, or
    // this page's origin is not one the bridge answers.
    throw new Error(
      `Browser access is unavailable on ${BRIDGE_URL} (method=${method}). ` +
        `Enable it in the Isoform desktop app under Settings -> Developer tools -> ` +
        `Browser access, or set YANSU_BROWSER_BRIDGE=1 before launch. ` +
        `Pages served from a port other than 5173, 4173 or ${BRIDGE_PORT} need ` +
        `their origin in YANSU_BROWSER_BRIDGE_ORIGINS. ` +
        `Underlying error: ${(err as Error)?.message ?? String(err)}`,
    );
  }
}

/**
 * Call a Go App method via the browser bridge HTTP server.
 * POST /api/call with JSON body { method, args } and the bridge token.
 */
export async function callAppMethod(
  method: string,
  ...args: any[]
): Promise<any> {
  let res = await postCall(method, args);
  if (res.status === 401) {
    // The desktop app restarted the bridge and with it the token. The call
    // was refused before it ran, so it is safe to send again.
    tokenPromise = null;
    res = await postCall(method, args);
  }
  const json = await res.json().catch(() => null);
  if (!json) {
    throw new Error(`bridge call ${method} failed (${res.status}): no response body`);
  }
  // Go server returns {ok, data, error} envelope (browserAPIResponse).
  if (!json.ok || json.error) {
    throw new Error(json.error || `bridge call ${method} failed`);
  }
  return json.data;
}

// ---------------------------------------------------------------------------
// WebSocket event bus
// ---------------------------------------------------------------------------

type EventCallback = (...args: any[]) => void;

const listeners = new Map<string, Set<EventCallback>>();

let ws: WebSocket | null = null;
let reconnectAttempts = 0;
const MAX_RECONNECT_ATTEMPTS = 5;
const BASE_RECONNECT_MS = 1000;

function getReconnectDelay(): number {
  // Exponential backoff: 1s, 2s, 4s, 8s, 16s (capped)
  const delay = BASE_RECONNECT_MS * Math.pow(2, reconnectAttempts);
  return Math.min(delay, 16000);
}

let wsTokenPending = false;

function scheduleReconnect(): void {
  if (reconnectAttempts < MAX_RECONNECT_ATTEMPTS) {
    const delay = getReconnectDelay();
    reconnectAttempts++;
    setTimeout(connectWebSocket, delay);
  }
}

function connectWebSocket(): void {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }
  if (wsTokenPending) return;
  wsTokenPending = true;
  getBridgeToken()
    .then((token) => {
      wsTokenPending = false;
      openWebSocket(token);
    })
    .catch(() => {
      wsTokenPending = false;
      scheduleReconnect();
    });
}

function openWebSocket(token: string): void {
  if (ws && (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING)) {
    return;
  }

  try {
    // A browser cannot set headers on a WebSocket, so the token goes in the
    // query; the bridge accepts it there for /ws only.
    ws = new WebSocket(`${WS_URL}?token=${encodeURIComponent(token)}`);
  } catch {
    return;
  }

  ws.onopen = () => {
    reconnectAttempts = 0;
  };

  ws.onmessage = (event) => {
    try {
      const data = JSON.parse(event.data);
      const eventName: string = data.event;
      const eventArgs: any[] = data.args ?? [];
      const cbs = listeners.get(eventName);
      if (cbs) {
        for (const cb of cbs) {
          try {
            cb(...eventArgs);
          } catch (err) {
            console.error(`[browser-bridge] event handler error for "${eventName}":`, err);
          }
        }
      }
    } catch {
      // ignore non-JSON messages
    }
  };

  ws.onclose = () => {
    ws = null;
    // The close may come from a bridge restart, which also replaced the
    // token, so read it again before reconnecting.
    tokenPromise = null;
    scheduleReconnect();
  };

  ws.onerror = () => {
    // onclose will fire after onerror, reconnect handled there
  };
}

/**
 * Subscribe to a named event. Returns an unsubscribe function.
 */
export function onEvent(
  eventName: string,
  callback: EventCallback,
): () => void {
  if (!listeners.has(eventName)) {
    listeners.set(eventName, new Set());
  }
  listeners.get(eventName)!.add(callback);

  // Ensure WS is connected when first listener is added
  if (!isWailsEnvironment()) {
    connectWebSocket();
  }

  return () => {
    const cbs = listeners.get(eventName);
    if (cbs) {
      cbs.delete(callback);
      if (cbs.size === 0) {
        listeners.delete(eventName);
      }
    }
  };
}

/**
 * Remove all listeners for a given event name.
 */
export function offEvent(eventName: string): void {
  listeners.delete(eventName);
}

/**
 * Emit an event to the server via the WebSocket connection.
 */
export function emitEvent(eventName: string, ...args: any[]): void {
  if (!isWailsEnvironment()) {
    connectWebSocket();
  }
  if (ws && ws.readyState === WebSocket.OPEN) {
    ws.send(JSON.stringify({ event: eventName, args }));
  }
}
