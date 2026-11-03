// Screenshots for docs/first-day-qa/: each page on the first day in light and
// dark, from e2e/first-day-qa.tsx, plus setup screen 2 from the setup gallery.
// Run with `pnpm exec playwright test -c playwright.first-day-qa.config.ts`.
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';

const OUT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../docs/first-day-qa');
// Tuesday 1:10 PM, so the afternoon has meetings and free time left.
const NOW = new Date(2026, 9, 27, 13, 10);
const STATES: { name: string; query: string; ready: string; width: number; scroll?: string }[] = [
    { name: '01-today', query: 'page=today', ready: 'getting-to-know-you', width: 1920 },
    { name: '02-today-narrow', query: 'page=today', ready: 'getting-to-know-you', width: 1440, scroll: 'getting-to-know-you' },
    { name: '03-today-no-history-no-calendar', query: 'page=today-bare', ready: 'getting-to-know-you', width: 1440 },
    { name: '04-chat', query: 'page=chat', ready: 'first-day-starters', width: 1180 },
    { name: '05-about-you', query: 'page=about-you', ready: 'Writes the tests before the fix', width: 1180 },
    { name: '06-how-you-do-things', query: 'page=know-how', ready: 'Price per seat, not per project', width: 1180 },
    { name: '07-who-you-know', query: 'page=people', ready: 'people-calendar', width: 1180 },
    { name: '08-activity', query: 'page=activity', ready: 'activity-now-row', width: 1180 },
    { name: '09-privacy', query: 'page=privacy', ready: 'Read my Claude Code and Codex history', width: 1180 },
];

for (const theme of ['paper', 'onedark']) {
    const suffix = theme === 'paper' ? 'light' : 'dark';
    for (const state of STATES) {
        test(`${state.name} ${theme}`, async ({ page }) => {
            const errors: string[] = [];
            page.on('pageerror', (error) => { if (!error.message.includes('bridge')) errors.push(error.message); });
            await page.clock.setFixedTime(NOW);
            await page.setViewportSize({ width: state.width, height: 900 });
            await page.goto(`/e2e/first-day-qa.html?${state.query}&theme=${theme}`);
            const ready = /^[a-z0-9-]+$/.test(state.ready) ? page.getByTestId(state.ready) : page.getByText(state.ready).first();
            await expect(ready).toBeVisible();
            await page.waitForTimeout(600);
            if (state.scroll) await page.getByTestId(state.scroll).evaluate((el) => el.scrollIntoView({ block: 'center' }));
            if (state.name === '04-chat') await page.locator('[data-starter=meeting]').click();
            await page.screenshot({ path: path.join(OUT, `${state.name}-${suffix}.png`) });
            expect(errors).toEqual([]);
        });
    }
    test(`10-setup-your-ai ${theme}`, async ({ page }) => {
        await page.setViewportSize({ width: 600, height: 560 });
        await page.goto(`/e2e/setup-v2.html?still=ai-picked&theme=${suffix}`);
        await expect(page.getByTestId('setup-v2-ai-history')).toBeVisible();
        await page.waitForTimeout(400);
        await page.screenshot({ path: path.join(OUT, `10-setup-your-ai-${suffix}.png`) });
    });
}
