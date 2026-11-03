import { ReportPageFirstPaint } from './wails-shim-app';

// Page-switch timing contains page names, durations and where the painted
// value came from only, never arguments, returned content, session IDs, or
// user text. The first-paint line ships in every build (snappy 1.0, "Done
// when": every page's first paint within 100 ms); the per-binding detail is
// a development diagnostic.
let sequence = 0;
let current: { id: number; page: string; started: number; bindings: { name: string; ms?: number }[]; painted: boolean } | undefined;

export type PageDataSource = 'cache' | 'network';

// The read whose value first paints each page. Its last source names the
// page's source in the [perf] line; a page with no recorded read is 'network'.
const pagePrimaryBinding: Record<string, string> = {
    today: 'GetLatestBrief',
    chat: 'LoadStreamItemsPage',
    activity: 'EnsureDailySummary',
    goals: 'ListGoals',
    tasks: 'ListHandoffs',
    automations: 'GetUpcoming',
    'about-you': 'GetAboutYou',
    'know-how': 'ListKnowHow',
    people: 'ListPeopleOverview',
    settings: 'GetSettingsOverview',
    search: 'GetTodayTopics',
};
const bindingSources = new Map<string, PageDataSource>();

/** Records where a read's first painted value came from. Called by the page
 * query cache on every read; direct (uncached) binding calls never record, so
 * their pages keep the 'network' default. Only the first read after a page
 * switch counts: a background revalidation must not flip a cold paint's
 * 'network' to 'cache' before the mark fires. */
export function noteBindingSource(name: string, source: PageDataSource) {
    if (!bindingSources.has(name)) bindingSources.set(name, source);
}

export function pageDataSource(page: string): PageDataSource {
    return bindingSources.get(pagePrimaryBinding[page] ?? '') ?? 'network';
}

export function beginPageSwitch(page: string) {
    bindingSources.clear();
    current = { id: ++sequence, page, started: performance.now(), bindings: [], painted: false };
}
const contentPage: Record<string, string> = { GetAboutYou: 'about-you', ListKnowHow: 'know-how', ListPeopleOverview: 'people' };
export function timePageBinding<T>(name: string, load: () => Promise<T>): Promise<T> {
    const started = performance.now();
    const measurement = current;
    const record = { name, ms: undefined as number | undefined };
    if (import.meta.env.DEV && measurement && !measurement.painted && measurement.bindings.length < 128) measurement.bindings.push(record);
    return Promise.resolve().then(load).finally(() => {
        if (import.meta.env.DEV) {
            record.ms = performance.now() - started;
            if (measurement) console.debug('[page-binding]', { switch: measurement.id, page: measurement.page, ...record });
        }
        if (contentPage[name]) markPageContent(contentPage[name]);
    });
}

/** Sends one page's first paint to the Go log as
 *  "[perf] first_paint page=<page> ms=<n> source=<cache|network>". */
function reportFirstPaint(page: string, ms: number, source: PageDataSource) {
    try { void ReportPageFirstPaint(page, ms, source).catch(() => {}); } catch { /* Older browser bridge. */ }
}

export function markPageContent(page: string, source?: PageDataSource) {
    const measurement = current;
    if (!measurement || measurement.page !== page || measurement.painted) return;
    // The second frame follows the browser's opportunity to paint the commit.
    requestAnimationFrame(() => requestAnimationFrame(() => {
        if (current !== measurement || measurement.painted) return;
        measurement.painted = true;
        const ms = Math.round(performance.now() - measurement.started);
        const from = source ?? pageDataSource(page);
        if (import.meta.env.DEV) {
            console.debug('[page-switch]', { switch: measurement.id, page, contentPaintMs: ms, bindings: measurement.bindings });
            console.info(`[perf] page=${page} first_data_ms=${ms} source=${from}`);
        }
        reportFirstPaint(page, ms, from);
    }));
}
