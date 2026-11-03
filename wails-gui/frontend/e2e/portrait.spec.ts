import { expect, test, type Page } from '@playwright/test';

// Portrait v1.0.0 §8: every state, light and dark, against the stills in
// docs/handoff-2026-10-28/portrait-1.0.0/.
const shots: { name: string; state: string; width?: number; act?: (page: Page) => Promise<void> }[] = [
    { name: '01-ready', state: 'ready' },
    { name: '03-evidence', state: 'ready', act: async (page) => {
        await page.getByRole('button', { name: 'You guard your time before noon' }).click();
        await expect(page.getByRole('dialog')).toBeVisible();
    } },
    { name: '04-why-this-name', state: 'ready', act: async (page) => {
        await page.getByRole('button', { name: 'Why this name' }).click();
        await expect(page.getByRole('dialog')).toBeVisible();
    } },
    { name: '05-still-learning', state: 'learning' },
    { name: '06-writing-the-first', state: 'writing' },
    { name: '07-not-quite', state: 'ready', act: async (page) => {
        await page.getByRole('button', { name: 'Not quite' }).click();
        await page.getByRole('textbox').fill('I start early, but afternoons are for people.');
    } },
    { name: '08-not-quite-rewriting', state: 'rewriting' },
    { name: '09-not-quite-rewritten', state: 'rewritten' },
    { name: '10-belief-forgotten', state: 'forgotten' },
    { name: '11-couldnt-write', state: 'failed' },
    { name: '12-narrow', state: 'ready', width: 420 },
    { name: '13-couldnt-update', state: 'couldntUpdate' },
    { name: '14-new-version-ready', state: 'ready', act: async (page) => {
        await page.evaluate(() => (window as any).newVersion());
        await expect(page.getByText('A new version is ready.')).toBeVisible();
    } },
    { name: '15-thats-me', state: 'ready', act: async (page) => {
        await page.getByRole('button', { name: "That's me" }).click();
        await expect(page.getByText("You said that's you")).toBeVisible();
    } },
    { name: '16-today-card', state: 'today' },
    // Portrait v2.0.0, against docs/handoff-2026-10-29/portrait-2.0.0/.
    { name: 'v2-01-what-survived', state: 'survived' },
    { name: 'v2-03-confirming', state: 'survived', act: async (page) => {
        await page.getByRole('button', { name: "That's me" }).nth(1).click();
        await expect(page.getByText('Writing with what you confirmed')).toBeVisible();
    } },
    { name: 'v2-04-after-confirming', state: 'confirmed' },
    { name: 'v2-05-only-the-shape', state: 'shape' },
    { name: 'v2-06-couldnt-reach-the-ai', state: 'limit' },
    { name: 'v2-07-narrow-survived', state: 'survived', width: 420 },
];

for (const theme of ['light', 'dark']) {
    for (const shot of shots) {
        test(`${shot.name} ${theme}`, async ({ page }, testInfo) => {
            const errors: string[] = [];
            page.on('pageerror', (error) => errors.push(error.message));
            await page.route('**/api/**', (route) => { errors.push('Unexpected backend request'); return route.abort(); });
            await page.setViewportSize({ width: shot.width ?? 900, height: 1100 });
            await page.goto(`/e2e/portrait.html?state=${shot.state}&theme=${theme}`);
            await expect(page.getByTestId(shot.state === 'today' ? 'today-portrait-card' : 'portrait-shape').first()).toBeVisible();
            await page.evaluate(() => document.fonts.ready);
            if (shot.act) await shot.act(page);
            await page.waitForTimeout(250);
            await page.screenshot({ path: testInfo.outputPath(`${shot.name}-${theme}.png`), fullPage: true });
            expect(errors).toEqual([]);
        });
    }
}

test('zh-CN ready', async ({ page }, testInfo) => {
    await page.setViewportSize({ width: 900, height: 1100 });
    await page.goto('/e2e/portrait.html?state=ready&language=zh-CN');
    await expect(page.getByTestId('portrait-paragraph')).toBeVisible();
    await page.evaluate(() => document.fonts.ready);
    await page.screenshot({ path: testInfo.outputPath('01-ready-zh-CN.png'), fullPage: true });
});
