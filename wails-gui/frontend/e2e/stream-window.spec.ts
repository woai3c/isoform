import { test, expect } from '@playwright/test';

// Real-library windowing guard: react-virtuoso measures a real WebKit
// viewport here (jsdom cannot), so scroll anchoring is exercised end to end.

test.beforeEach(({ page }) => {
    page.on('pageerror', (error) => { console.error('Fixture error:', error.message); });
});

function viewportMetrics(page: import('@playwright/test').Page) {
    return page.locator('[data-radix-scroll-area-viewport]').evaluate((el) => ({
        scrollTop: el.scrollTop,
        scrollHeight: el.scrollHeight,
        clientHeight: el.clientHeight,
    }));
}

test('windowed stream anchors prepends and never moves on appends', async ({ page }) => {
    await page.goto('/e2e/stream-window.html');
    // First screen: the latest rows, windowed (never the whole history).
    await expect(page.getByText('Answer 1199 with markdown')).toBeVisible();
    await expect
        .poll(async () => page.locator('[data-stream-turn]').count(), { timeout: 10_000 })
        .toBeGreaterThanOrEqual(150);
    const rendered = await page.locator('[data-stream-turn]').count();
    expect(rendered).toBeLessThan(200);

    // Opens at the bottom, like the plain list did.
    const initial = await viewportMetrics(page);
    expect(initial.scrollTop + initial.clientHeight).toBeGreaterThan(initial.scrollHeight - 40);

    // Prepend 50 older rows: the marker row must not move on screen. The
    // id wrapper is display:contents, so measure its card child.
    const marker = page.locator('[data-stream-item-id="u-1199"] > *').first();
    const before = (await marker.boundingBox())!;
    await page.getByTestId('prepend').click();
    await page.waitForTimeout(600);
    const afterPrepend = (await marker.boundingBox())!;
    expect(Math.abs(afterPrepend.y - before.y)).toBeLessThanOrEqual(24);

    // Appends while reading history must not move the reading position. The
    // reader scrolls with a real wheel gesture; only that input marks them as
    // away from the bottom, so a programmatic scroll alone would not hold.
    await page.mouse.move(600, 300);
    // Scroll up to the region the original guard measured (scrollTop near 500).
    for (let i = 0; i < 40; i++) {
        if ((await viewportMetrics(page)).scrollTop < 800) break;
        await page.mouse.wheel(0, -1500);
        await page.waitForTimeout(120);
    }
    // Wheel momentum keeps scrolling after the gesture: wait for it to settle.
    let settled = await viewportMetrics(page);
    for (let i = 0; i < 20; i++) {
        await page.waitForTimeout(150);
        const next = await viewportMetrics(page);
        if (next.scrollTop === settled.scrollTop) break;
        settled = next;
    }
    const reading = settled;
    expect(reading.scrollHeight - (reading.scrollTop + reading.clientHeight)).toBeGreaterThanOrEqual(80);
    await page.getByTestId('append').click();
    await page.waitForTimeout(600);
    const afterAppend = await viewportMetrics(page);
    expect(afterAppend.scrollTop).toBe(reading.scrollTop);

    // Native find covers the recent section: after the append, all 150
    // recent rows (turns 1100-1249) are mounted.
    await expect(page.getByText('Answer 1100 with markdown')).toBeAttached();
    await expect(page.getByText('Answer 1249 with markdown')).toBeAttached();
});
