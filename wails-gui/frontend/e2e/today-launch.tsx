// The production App at launch while Isoform is busy: ?slow=ms delays every
// binding but the startup gate's, ?hang=A,B never answers the named reads,
// ?fail=A,B rejects them. Fictional user, empty main chat.
import { installFixture, reads } from './launch-demo/fixture';

const params = new URLSearchParams(location.search);
const slow = Number(params.get('slow') ?? 0);
const names = (key: string) => new Set((params.get(key) ?? '').split(',').filter(Boolean));
const hang = names('hang');
const fail = names('fail');
// ?busy=ms blocks the main thread that long out of every 100 ms.
const busy = Number(params.get('busy') ?? 0);
if (busy > 0) setInterval(() => { const end = performance.now() + busy; while (performance.now() < end) { /* busy */ } }, 100);
const exempt = new Set(['GetStorageStartupStatus', 'LoadUIState', 'GetThemePreference']);
localStorage.clear();
localStorage.setItem('yansu.theme', 'paper');
document.documentElement.dataset.theme = 'paper';
reads.GetUserProfile = { name: 'Alex' };
reads.LoadStreamItemsPage = { items: [], hasMore: false, nextCursor: 0 };
installFixture();
const app = (window as any).go.main.App;
(window as any).go.main.App = new Proxy({}, { get: (_, method: string) => async (...args: unknown[]) => {
    if (hang.has(method)) return new Promise(() => {});
    if (fail.has(method)) throw new Error(`${method} failed`);
    if (slow > 0 && !exempt.has(method)) await new Promise((r) => setTimeout(r, slow));
    return app[method](...args);
} });
const { mount } = await import('./launch-demo/mount');
mount();
