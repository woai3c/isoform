import { test, expect, type Page } from '@playwright/test';
import { writeFileSync } from 'node:fs';

// Preload harness: switch through every sidebar page and Settings, twice, and
// record navigation-click -> first-real-data milliseconds per page, plus the
// app's own '[perf] page=<name> first_data_ms=<n> source=<cache|network>'
// lines. Run with playwright.preload.config.ts (WebKit). The self-review
// pastes the printed markdown table.

interface Stop { page: string; pick: (page: Page) => ReturnType<Page['locator']> }

const stops: Stop[] = [
    { page: 'today', pick: (page) => page.getByText('The task list is ready for review.') },
    { page: 'chat', pick: (page) => page.getByTestId('preload-chat').filter({ hasText: 'Fixture chat answer' }) },
    { page: 'activity', pick: (page) => page.getByText('Fixture activity').first() },
    { page: 'goals', pick: (page) => page.getByTestId('goals-empty') },
    { page: 'about-you', pick: (page) => page.getByText('Fixture belief').first() },
    { page: 'know-how', pick: (page) => page.getByText('Fixture know-how').first() },
    { page: 'people', pick: (page) => page.getByText('Fixture person').first() },
    { page: 'settings', pick: (page) => page.getByText('Thinks with') },
    { page: 'search', pick: (page) => page.locator('[role="dialog"]').getByText('Fixture topic 0') },
];

interface Row { page: string; pass: string; ms: number; completeMs?: number; perf?: string }

/** The account-menu dialogs the addendum measures to completion. */
const DIALOG_STOPS = new Set(['about-you', 'know-how', 'people', 'settings']);

async function visit(page: Page, stop: Stop, rows: Row[], pass: string, perfLines: string[]) {
    if (DIALOG_STOPS.has(stop.page)) {
        // Start from a quiet bridge so the dialog's own reads alone count.
        await page.waitForFunction(() => (window as any).__readsPending === 0, undefined, { timeout: 20_000 });
    }
    const started = await page.evaluate(() => performance.now());
    await page.locator(`[data-nav="${stop.page}"]`).click();
    await stop.pick(page).waitFor({ state: 'visible', timeout: 20_000 });
    const ms = await page.evaluate((t0) => Math.round(performance.now() - t0), started);
    let completeMs: number | undefined;
    if (DIALOG_STOPS.has(stop.page)) {
        await page.waitForFunction(() => (window as any).__readsPending === 0, undefined, { timeout: 20_000 });
        completeMs = await page.evaluate((t0) => Math.round(performance.now() - t0), started);
    }
    if (OVERLAY_PAGES.has(stop.page)) {
        // Overlay pages are modal dialogs: Escape returns to the page
        // underneath so the next navigation click is reachable.
        await page.keyboard.press('Escape');
        await page.locator('[role="dialog"]').waitFor({ state: 'detached', timeout: 5_000 }).catch(() => undefined);
    }
    const perf = perfLines.findLast((line) => line.includes(`page=${stop.page} `));
    rows.push({ page: stop.page, pass, ms, completeMs, perf });
}

const OVERLAY_PAGES = new Set(['about-you', 'know-how', 'people', 'search']);

test('preload: every sidebar page and Settings, cold and warm', async ({ page }, testInfo) => {
    const perfLines: string[] = [];
    page.on('console', (message) => {
        const text = message.text();
        if (text.startsWith('[perf]')) perfLines.push(text);
    });
    page.on('pageerror', (error) => console.error('Fixture error:', error.message));

    const rows: Row[] = [];
    await page.setViewportSize({ width: 1400, height: 900 });

    // Pass 1: Today paints on load, the rest on navigation.
    const t0 = Date.now();
    await page.goto('/e2e/preload-pages.html');
    await stops[0].pick(page).waitFor({ state: 'visible', timeout: 20_000 });
    rows.push({ page: 'today', pass: 'cold', ms: Date.now() - t0, perf: perfLines.findLast((line) => line.includes('page=today ')) });
    for (const stop of stops.slice(1)) await visit(page, stop, rows, 'cold', perfLines);
    // Pass 2: every page has been visited once; the cache and retention decide.
    for (const stop of stops) await visit(page, stop, rows, 'second', perfLines);

    writeFileSync(testInfo.outputPath('preload-pages.json'), JSON.stringify(rows, null, 2));
    console.log('\n[preload table: cold vs second visit]');
    for (const stop of stops) {
        const cold = rows.find((row) => row.page === stop.page && row.pass === 'cold');
        const warm = rows.find((row) => row.page === stop.page && row.pass === 'second');
        console.log(`| ${stop.page} | ${cold?.ms ?? '-'} | ${warm?.ms ?? '-'} | ${warm?.perf ?? cold?.perf ?? ''} |`);
    }
    // The addendum's dialogs: first data vs fully complete, both visits.
    console.log('\n[dialog table: first-data / complete, cold vs second visit]');
    for (const stop of stops.filter((stop) => DIALOG_STOPS.has(stop.page))) {
        const cold = rows.find((row) => row.page === stop.page && row.pass === 'cold');
        const warm = rows.find((row) => row.page === stop.page && row.pass === 'second');
        console.log(`| ${stop.page} | ${cold?.ms ?? '-'} | ${cold?.completeMs ?? '-'} | ${warm?.ms ?? '-'} | ${warm?.completeMs ?? '-'} |`);
    }
    console.log('[perf lines]', JSON.stringify(perfLines, null, 2));
});

test('preload: idle prefetch warms every page before the first visit', async ({ page }, testInfo) => {
    const perfLines: string[] = [];
    page.on('console', (message) => {
        const text = message.text();
        if (text.startsWith('[perf]')) perfLines.push(text);
    });
    page.on('pageerror', (error) => console.error('Fixture error:', error.message));

    await page.setViewportSize({ width: 1400, height: 900 });
    await page.goto('/e2e/preload-pages.html?prefetch=1');
    await stops[0].pick(page).waitFor({ state: 'visible', timeout: 20_000 });
    // The walk ends with the search recents; only then are all pages warm.
    await page.waitForFunction(() => (window as any).__idlePrefetchDone === true, undefined, { timeout: 60_000 });

    // Today is already showing; measure it last so every stop is a real switch.
    const order = [...stops.slice(1), stops[0]];
    const rows: Row[] = [];
    for (const stop of order) await visit(page, stop, rows, 'after-prefetch', perfLines);

    writeFileSync(testInfo.outputPath('preload-pages-prefetch.json'), JSON.stringify(rows, null, 2));
    console.log('\n[preload table: first visit after idle prefetch]');
    for (const row of rows) console.log(`| ${row.page} | ${row.ms} | ${row.completeMs ?? '-'} | ${row.perf ?? ''} |`);
});

test('retention: a reopened dialog page keeps its scroll offset and state', async ({ page }) => {
    await page.setViewportSize({ width: 1400, height: 900 });
    await page.goto('/e2e/preload-pages.html');
    await page.getByText('The task list is ready for review.').waitFor({ state: 'visible', timeout: 20_000 });

    // Open About you, scroll its list, close with Escape.
    await page.locator('[data-nav="about-you"]').click();
    const dialog = page.locator('[role="dialog"]');
    await dialog.getByText('Fixture belief 0').first().waitFor({ state: 'visible', timeout: 20_000 });
    const viewport = dialog.locator('[data-radix-scroll-area-viewport]').first();
    await viewport.evaluate((element) => { element.scrollTop = 150; });
    // The scroll event is asynchronous; let the page's offset memory see it.
    await page.waitForTimeout(200);
    await page.keyboard.press('Escape');
    await dialog.waitFor({ state: 'detached', timeout: 5_000 }).catch(() => undefined);

    // Wander, then reopen: the retained page must restore the offset (the
    // restore lands a frame after attach).
    await page.locator('[data-nav="chat"]').click();
    await page.getByTestId('preload-chat').filter({ hasText: 'Fixture chat answer' }).waitFor({ state: 'visible' });
    await page.locator('[data-nav="about-you"]').click();
    await dialog.getByText('Fixture belief 0').first().waitFor({ state: 'visible', timeout: 20_000 });
    await expect
        .poll(async () => viewport.evaluate((element) => element.scrollTop), { timeout: 5_000 })
        .toBe(150);

    // State proof: type a filter, close, reopen — same component instance.
    await dialog.getByRole('textbox').first().fill('belief 2');
    await page.keyboard.press('Escape');
    await dialog.waitFor({ state: 'detached', timeout: 5_000 }).catch(() => undefined);
    await page.locator('[data-nav="about-you"]').click();
    await dialog.getByText('Fixture belief 29').first().waitFor({ state: 'visible', timeout: 20_000 });
    expect(await dialog.getByRole('textbox').first().inputValue()).toBe('belief 2');
});
