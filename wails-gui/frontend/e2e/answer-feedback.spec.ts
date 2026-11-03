import { expect, test, type Page } from '@playwright/test';

// Answer feedback v1.0.0: the moments right after That's me, Not quite and
// Forget, against docs/handoff-2026-10-29/answer-feedback-1.0.0/.
const cardsReady = async (page: Page) => expect(page.getByRole('region', { name: 'New this week' })).toBeVisible();
const shots: { name: string; state: string; language?: string; ready?: (page: Page) => Promise<void>; act?: (page: Page) => Promise<void> }[] = [
    { name: '01-portrait-thats-me', state: 'ready', act: async (page) => {
        await page.getByRole('button', { name: "That's me" }).click();
        await expect(page.getByText('Isoform keeps this version. Anything newer waits for you.')).toBeVisible();
        await page.waitForTimeout(900);
    } },
    { name: '01-portrait-next-time', state: 'kept' },
    { name: '02-portrait-rewritten-from-note', state: 'rewrittenNote' },
    { name: '03-confirm-rows-answered', state: 'survived', act: async (page) => {
        const rows = page.getByTestId('portrait-confirm-rows');
        await rows.getByRole('button', { name: "That's me" }).first().click();
        await rows.getByRole('button', { name: 'Not quite' }).first().click();
    } },
    { name: '03-sentence-back', state: 'back', act: async (page) => { await page.waitForTimeout(900); } },
    { name: '04-new-this-week-before', state: 'cards', ready: cardsReady },
    { name: '04-new-this-week-answered', state: 'cards', ready: cardsReady, act: async (page) => {
        const cards = page.getByRole('article');
        await cards.nth(0).getByRole('button', { name: 'That’s me' }).click();
        await cards.nth(1).getByRole('button', { name: 'That’s me' }).click();
        await cards.nth(2).getByRole('button', { name: 'Not quite' }).click();
        await cards.nth(2).getByRole('button', { name: 'Save' }).click();
        await cards.nth(3).getByRole('button', { name: 'Forget' }).click();
    } },
    { name: '04-new-this-week-not-quite-field', state: 'cards', ready: cardsReady, act: async (page) => {
        await page.getByRole('article').nth(2).getByRole('button', { name: 'Not quite' }).click();
    } },
    { name: '10-new-this-week-zh', state: 'cards', language: 'zh-CN', ready: async (page) => expect(page.getByRole('region', { name: '本周新认识' })).toBeVisible(), act: async (page) => {
        const cards = page.getByRole('article');
        await cards.nth(0).getByRole('button', { name: '说得对' }).click();
        await cards.nth(2).getByRole('button', { name: '不太对' }).click();
        await cards.nth(2).getByRole('button', { name: '保存' }).click();
    } },
];

for (const theme of ['light', 'dark']) {
    for (const shot of shots) {
        test(`${shot.name} ${theme}`, async ({ page }, testInfo) => {
            const errors: string[] = [];
            page.on('pageerror', (error) => errors.push(error.message));
            await page.route('**/api/**', (route) => { errors.push('Unexpected backend request'); return route.abort(); });
            await page.setViewportSize({ width: 900, height: 1100 });
            await page.goto(`/e2e/portrait.html?state=${shot.state}&theme=${theme}${shot.language ? `&language=${shot.language}` : ''}`);
            if (shot.ready) await shot.ready(page);
            else await expect(page.getByTestId('portrait-shape').first()).toBeVisible();
            await page.evaluate(() => document.fonts.ready);
            if (shot.act) await shot.act(page);
            await page.waitForTimeout(250);
            await page.screenshot({ path: testInfo.outputPath(`${shot.name}-${theme}.png`), fullPage: true });
            expect(errors).toEqual([]);
        });
    }
}
