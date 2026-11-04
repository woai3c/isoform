import { expect, test, type Page } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

// today-review v1.1.14 D8 and D9 on the production App (fixture: today-launch.tsx).
// A busy WebView can stop advancing CSS animations and transitions; the
// timeline is frozen here so nothing on screen may wait on one.
const out = process.env.TASKS_THU_SHOTS ?? '/tmp/tasks-thu-shots';
const NAV = ['Today', 'Chat', 'Tasks', 'Activity', 'About you', 'How you do things', 'Who you know'];
const shoot = async (page: Page, name: string) => {
    mkdirSync(out, { recursive: true });
    await page.screenshot({ path: join(out, `${name}.png`) });
};
const freeze = async (page: Page) => {
    const cdp = await page.context().newCDPSession(page);
    await cdp.send('Animation.enable');
    await cdp.send('Animation.setPlaybackRate', { playbackRate: 0 });
};
const selected = (page: Page) => page.evaluate((nav) => [...document.querySelectorAll('button')]
    .filter((b) => nav.includes(b.innerText.trim()) && (b.getAttribute('aria-current') === 'page' || getComputedStyle(b).backgroundColor !== 'rgba(0, 0, 0, 0)'))
    .map((b) => `${b.innerText.trim()}${b.getAttribute('aria-current') === 'page' ? '' : ' (filled only)'}`), NAV);
const expectToday = async (page: Page) => {
    const column = page.getByTestId('today-column');
    await expect(column).toHaveCSS('opacity', '1');
    await expect(page.getByTestId('today-header').getByRole('heading', { level: 1 })).toHaveText('Morning brief');
    await expect(page.getByTestId('today-status')).toContainText('next brief at 5:00 PM');
};

test('D8 D9 Today paints while the brief read hangs and the selected row follows the page', async ({ page }) => {
    await page.goto('/e2e/today-launch.html?hang=GetLatestBrief');
    await expect(page.getByTestId('today-header')).toBeVisible({ timeout: 30_000 });
    await freeze(page);
    await expect.poll(() => selected(page)).toEqual(['Today']);
    await expectToday(page);
    await expect(page.getByTestId('brief-card-loading')).toBeVisible();
    await shoot(page, '09-today-brief-read-hangs');
    for (const name of ['About you', 'Today', 'Tasks', 'How you do things', 'Who you know', 'Today']) {
        await page.getByRole('button', { name, exact: true }).first().click();
        await page.mouse.move(1000, 790);
        await expect.poll(() => selected(page)).toEqual([name]);
        if (name === 'Today') await expectToday(page);
        if (name === 'About you') await shoot(page, '10-sidebar-follows-the-page');
    }
});

test('D8 a failed brief read says so in copper with Try again', async ({ page }) => {
    await page.goto('/e2e/today-launch.html?fail=GetLatestBrief');
    await freeze(page);
    const failed = page.getByTestId('brief-load-failed');
    await expect(failed).toHaveText("Couldn't load your brief. Try again", { timeout: 30_000 });
    await expect(page.getByTestId('today-column')).toHaveCSS('opacity', '1');
    await expect(failed).not.toHaveCSS('color', 'rgb(93, 95, 110)');
    await shoot(page, '11-today-brief-read-failed');
});
