// Dev-only preview surface for ChatErrorCard. Renders the seven variants
// side-by-side using representative raw error strings so we can eyeball the
// classification + styling without having to provoke real upstream failures.
//
// Reachable via the `#dev=chat-errors` URL fragment in dev builds; the
// fragment guard lives in App.tsx. Safe to delete once we've stabilised the
// chat-error UX and don't need the preview any more.

import { ChatErrorCard } from './chat-error-card';
import { parseChatError } from '@/lib/chat-error';

const FIXTURES: Array<{ label: string; raw: string }> = [
    {
        label: 'quota_period — weekly (real anthropic payload)',
        raw:
            'anthropic: HTTP 429: {"code":"USAGE_LIMIT_EXCEEDED","message":"error: code=429 reason=\\"WEEKLY_LIMIT_EXCEEDED\\" message=\\"weekly usage limit exceeded\\" metadata=map[]"}',
    },
    {
        label: 'quota_period — daily',
        raw: 'daily quota exhausted for project abc',
    },
    {
        label: 'quota_generic — 429 without period',
        raw: 'rate limit reached: too many requests (429)',
    },
    {
        label: 'subscription required',
        raw: 'subscription required: please upgrade to continue',
    },
    {
        label: 'auth — sign-in needed',
        raw: 'Authentication failed. Please run \'claude login\' in your terminal to refresh your credentials, then restart the app.',
    },
    {
        label: 'server — 503 upstream',
        raw: 'anthropic: HTTP 503: service unavailable',
    },
    {
        label: 'network — timeout',
        raw: 'context deadline exceeded: dial tcp 1.2.3.4:443: i/o timeout',
    },
    {
        label: 'unknown — plain Go error',
        raw: 'runtime error: index out of range [0] with length 0',
    },
    {
        label: 'unknown — JSON-RPC envelope',
        raw: '{"code":-32603,"message":"Internal error: tool \'web_fetch\' is not registered in the active MCP session"}',
    },
];

export function ChatErrorPreview() {
    return (
        <div className="fixed inset-0 bg-canvas p-8 overflow-y-auto">
            <header className="max-w-4xl mx-auto mb-8">
                <h1 className="text-xl font-semibold text-ink m-0">Chat error preview</h1>
                <p className="text-sm text-muted mt-1 m-0">
                    Dev-only gallery. Each card is the output of <code>parseChatError</code> applied to a representative raw error.
                    Remove the <code>#dev=chat-errors</code> fragment to return to the app.
                </p>
            </header>
            <div className="max-w-4xl mx-auto grid gap-6 sm:grid-cols-2">
                {FIXTURES.map((f) => {
                    const parsed = parseChatError(f.raw);
                    return (
                        <section key={f.label} className="space-y-2">
                            <div className="text-[11px] uppercase tracking-wider text-muted font-medium">{f.label}</div>
                            <div className="text-[11px] text-muted/70 font-mono break-words bg-panel border border-border/40 rounded-md p-2">
                                kind={parsed.kind}
                                {parsed.period ? ` · period=${parsed.period}` : ''}
                            </div>
                            <ChatErrorCard error={parsed} />
                            <details className="text-[11px] text-muted/70">
                                <summary className="cursor-pointer select-none">raw input</summary>
                                <pre className="mt-1 whitespace-pre-wrap break-words font-mono">{f.raw}</pre>
                            </details>
                        </section>
                    );
                })}
            </div>
        </div>
    );
}
