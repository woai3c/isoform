// Screenshots for docs/sidebar-qa/: each state of the sidebar handoff in light
// and dark, from e2e/sidebar-qa.tsx. Run with
// `pnpm exec playwright test -c playwright.sidebar-qa.config.ts`.
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test } from '@playwright/test';

const OUT = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../docs/sidebar-qa');
const STATES = [
    { name: '01-tasks', query: 'page=tasks', ready: 'Reply to Priya about Thursday' },
    { name: '03-tasks-first-day', query: 'page=tasks&day=first', ready: 'Morning brief' },
    { name: '04-about-you', query: 'page=about-you', ready: 'Renew passport' },
    { name: '05-how-you-do-things', query: 'page=know-how', ready: 'Weekly update' },
    { name: '06-who-you-know', query: 'page=people', ready: 'Joon Park' },
    { name: '07-account-menu', query: 'page=tasks&menu=1', ready: 'Keyboard shortcuts' },
];

for (const theme of ['paper', 'onedark']) {
    for (const state of STATES) {
        test(`${state.name} ${theme}`, async ({ page }) => {
            await page.goto(`/e2e/sidebar-qa.html?${state.query}&theme=${theme}`);
            await expect(page.getByText(state.ready).first()).toBeVisible();
            await page.waitForTimeout(600);
            await page.screenshot({ path: path.join(OUT, `${state.name}-${theme === 'paper' ? 'light' : 'dark'}.png`) });
        });
    }
}
