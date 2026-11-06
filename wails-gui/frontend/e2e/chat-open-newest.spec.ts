import { test, expect, type Page } from '@playwright/test';

// R36: the chat opens on its newest item in every path: a short snapshot that
// grows, a remount after the page was left, and a session switch.

function viewport(page: Page) {
    return page.locator('[data-radix-scroll-area-viewport]').evaluate((el) => ({
        scrollTop: el.scrollTop,
        scrollHeight: el.scrollHeight,
        clientHeight: el.clientHeight,
    }));
}

async function expectOpenAtNewest(page: Page) {
    await expect(page.getByText('Answer 1199 with markdown')).toBeAttached({ timeout: 10_000 });
    // Let the staged list and the bottom anchor settle.
    await page.waitForTimeout(800);
    const m = await viewport(page);
    const distanceFromBottom = m.scrollHeight - (m.scrollTop + m.clientHeight);
    expect(distanceFromBottom, JSON.stringify(m)).toBeLessThan(40);
    await expect(page.getByText('Answer 1199 with markdown')).toBeInViewport();
}

test.beforeEach(({ page }) => {
    page.on('pageerror', (error) => { console.error('Fixture error:', error.message); });
});

test('a short snapshot that grows opens on the newest item', async ({ page }) => {
    await page.goto('/e2e/chat-open-newest.html');
    await expect(page.getByText('Answer 1199 with markdown')).toBeVisible();
    await page.getByTestId('grow').click();
    await expect(page.getByTestId('count')).toHaveText('400');
    await expectOpenAtNewest(page);
});

test('a remount after the page was left opens on the newest item', async ({ page }) => {
    await page.goto('/e2e/chat-open-newest.html');
    await page.getByTestId('grow').click();
    await expectOpenAtNewest(page);
    await page.locator('[data-radix-scroll-area-viewport]').evaluate((el) => { el.scrollTop = 300; el.dispatchEvent(new Event('scroll')); });
    await page.waitForTimeout(200);
    await page.getByTestId('unmount').click();
    await page.getByTestId('remount').click();
    await expectOpenAtNewest(page);
});

test('newer items arriving while the snapshot is open keep the newest item in view', async ({ page }) => {
    await page.goto('/e2e/chat-open-newest.html');
    await page.getByTestId('grow').click();
    await expectOpenAtNewest(page);
    await page.getByTestId('grow-newer').click();
    await expect(page.getByText('Answer 1249 with markdown')).toBeAttached();
    await page.waitForTimeout(800);
    const m = await viewport(page);
    expect(m.scrollHeight - (m.scrollTop + m.clientHeight), JSON.stringify(m)).toBeLessThan(40);
});

test('a pane the reader scrolled up in opens on the newest item when shown again', async ({ page }) => {
    await page.goto('/e2e/chat-open-newest.html');
    await page.getByTestId('grow').click();
    await expectOpenAtNewest(page);
    // The reader scrolls up with a real wheel gesture, then leaves the page.
    await page.mouse.move(600, 300);
    await page.mouse.wheel(0, -2000);
    await page.waitForTimeout(300);
    await page.getByTestId('hide').click();
    await page.waitForTimeout(200);
    await page.getByTestId('show').click();
    await expectOpenAtNewest(page);
});

test('window focus, visibility and the display waking keep the reader in place', async ({ page }) => {
    await page.goto('/e2e/chat-open-newest.html');
    await page.getByTestId('grow').click();
    await expectOpenAtNewest(page);
    await page.mouse.move(600, 300);
    await page.mouse.wheel(0, -2000);
    await page.waitForTimeout(800);
    const place = (await viewport(page)).scrollTop;
    // Coming back from another app: focus, visibility change and a resize.
    await page.evaluate(() => {
        window.dispatchEvent(new Event('blur'));
        window.dispatchEvent(new Event('focus'));
        document.dispatchEvent(new Event('visibilitychange'));
        window.dispatchEvent(new Event('resize'));
    });
    await page.waitForTimeout(600);
    expect((await viewport(page)).scrollTop).toBe(place);
});

// Design's review shots: set R36_SHOTS_DIR to write light and dark frames of
// the pane opened on its newest item (after the hide-and-show round trip).
const shotsDir = process.env.R36_SHOTS_DIR;
for (const [scheme, theme] of [['light', 'paper'], ['dark', 'onedark']] as const) {
    test(`writes ${scheme} shots of the newest-item open`, async ({ page }) => {
        test.skip(!shotsDir, 'R36_SHOTS_DIR not set');
        await page.goto(`/e2e/chat-open-newest.html?theme=${theme}`);
        await page.getByTestId('grow').click();
        await expectOpenAtNewest(page);
        await page.mouse.move(600, 300);
        await page.mouse.wheel(0, -2000);
        await page.waitForTimeout(800);
        await page.getByTestId('hide').click();
        await page.getByTestId('show').click();
        await expectOpenAtNewest(page);
        await page.screenshot({ path: `${shotsDir}/chat-r36-v2-open-newest-${scheme}.png` });
    });
}
