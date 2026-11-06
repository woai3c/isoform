import { test, expect, type Page } from '@playwright/test';

// R38: a card draws at its final size. Nothing below it moves when its words
// arrive a moment after the first paint.

function belowY(page: Page) {
    return page.getByTestId('below').evaluate((el) => el.getBoundingClientRect().top);
}

test.beforeEach(({ page }) => {
    page.on('pageerror', (error) => { console.error('Fixture error:', error.message); });
});

test('with its first words the card draws whole, and nothing below it moves', async ({ page }) => {
    await page.goto('/e2e/brief-card-r38.html?lead=1');
    await expect(page.getByTestId('below')).toBeVisible();
    // The words came with the card, so they are on the first paint.
    await expect(page.getByText('Two replies are waiting on you', { exact: false })).toBeVisible();
    const first = await belowY(page);
    await page.waitForTimeout(1200);
    expect(Math.abs((await belowY(page)) - first), 'below moved after the brief loaded').toBeLessThanOrEqual(1);
});

test('without its words the card holds one line, not three, and nothing below moves', async ({ page }) => {
    await page.goto('/e2e/brief-card-r38.html?lead=0');
    await expect(page.getByTestId('below')).toBeVisible();
    const hold = await page.locator('[data-brief-body]').evaluate((el) => el.getBoundingClientRect().height);
    expect(hold, 'the held body is one line').toBeLessThanOrEqual(22);
    const first = await belowY(page);
    await expect(page.getByText('Two replies are waiting on you', { exact: false })).toBeVisible({ timeout: 10_000 });
    expect(Math.abs((await belowY(page)) - first), 'below moved when the words arrived').toBeLessThanOrEqual(1);
});

const shotsDirR38 = process.env.R38_SHOTS_DIR;
for (const [scheme, theme] of [['light', 'paper'], ['dark', 'onedark']] as const) {
    test(`writes ${scheme} shots of the card with its words`, async ({ page }) => {
        test.skip(!shotsDirR38, 'R38_SHOTS_DIR not set');
        await page.goto(`/e2e/brief-card-r38.html?lead=1&theme=${theme}`);
        await expect(page.getByText('Two replies are waiting on you', { exact: false })).toBeVisible();
        await page.waitForTimeout(1200);
        await page.screenshot({ path: `${shotsDirR38}/chat-r38-v2-brief-card-${scheme}.png` });
    });
}
