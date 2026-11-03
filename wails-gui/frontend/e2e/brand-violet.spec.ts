// Browser component checks use WebKit and synthetic host replies. They never
// launch Isoform, a native helper, a backend or the iPhone simulator.
import { test, expect } from '@playwright/test';
import { mkdir } from 'node:fs/promises';
import { resolve } from 'node:path';

const reviewDir = resolve(process.env.VIOLET_REVIEW_DIR ?? '../../docs/hub-reviews/brand-violet');
for (const theme of ['paper', 'onedark']) {
    for (const screen of ['today', 'chat', 'activity', 'settings', 'welcome', 'spotlight']) {
        test(`${screen} uses flat brand violet in ${theme}`, async ({ page }) => {
            await page.setViewportSize(screen === 'spotlight' ? { width: 768, height: 88 } : { width: 1440, height: 1000 });
            await page.route('http://127.0.0.1:4242/**', (route) => route.fulfill({
                json: route.request().url().includes('/theme') ? { theme, language: 'en', texture: true } : { suggestions: [] },
                headers: { 'Access-Control-Allow-Origin': '*', 'Access-Control-Allow-Headers': '*' },
            }));
            const url = ['today', 'chat', 'activity'].includes(screen)
                ? `/e2e/polish-structure.html?today-next&brand-violet&page=${screen}&theme=${theme}&sidebar=0`
                : `/e2e/brand-violet.html?${screen}&theme=${theme}`;
            await page.goto(url);
            await expect(page.locator('#root')).not.toBeEmpty();
            await expect(page.locator('html')).toHaveAttribute('data-theme', theme);
            await page.addStyleTag({ content: '* { animation: none !important; transition: none !important; caret-color: transparent !important; }' });
            await page.evaluate(() => document.fonts.ready);
            await expect.poll(() => page.locator('#root').innerText()).not.toBe('');
            const paint = await page.evaluate(() => {
                const root = getComputedStyle(document.documentElement);
                return {
                    ground: root.getPropertyValue('--color-background').trim(),
                    accent: root.getPropertyValue('--color-accent').trim(),
                    textures: ['grain', 'mottle-hi', 'mottle-lo', 'copper-hi'].map((name) => root.getPropertyValue(`--texture-${name}`).trim()),
                    images: [...document.querySelectorAll('.patina-ground, .patina-verdigris')].map((node) => getComputedStyle(node).backgroundImage),
                };
            });
            expect(paint.ground).toBe(theme === 'paper' ? '#f6f6f9' : '#121217');
            expect(paint.accent).toBe(theme === 'paper' ? '#4f5ed8' : '#7a8aff');
            expect(paint.textures).toEqual(['none', 'none', 'none', 'none']);
            expect(paint.images.every((image) => image === 'none')).toBe(true);
            if (screen === 'settings') {
                await expect(page.getByText('Texture', { exact: true })).toHaveCount(0);
                const active = page.locator('.settings-nav-item[data-active="true"]');
                await expect(active).toHaveCSS('color', theme === 'paper' ? 'rgb(79, 94, 216)' : 'rgb(122, 138, 255)');
                await expect(active).toHaveCSS('background-color', theme === 'paper' ? 'rgb(246, 246, 249)' : 'rgb(18, 18, 23)');
            }
            if (screen === 'chat') {
                await expect(page.locator('[data-isoform-line]').first()).toHaveCSS('color', theme === 'paper' ? 'rgb(79, 94, 216)' : 'rgb(122, 138, 255)');
                const ink = theme === 'paper' ? 'rgb(22, 22, 29)' : 'rgb(236, 236, 242)';
                await expect(page.getByRole('heading', { name: 'Prepared notes' })).toHaveCSS('color', ink);
                await expect(page.getByRole('heading', { name: 'Sources', exact: true })).toHaveCSS('color', ink);
                await expect(page.getByText('Prepare the meeting notes.', { exact: true })).toHaveCSS('color', ink);
            }
            await mkdir(reviewDir, { recursive: true });
            const before = await page.screenshot({ path: resolve(reviewDir, `${screen}-${theme}.png`) });
            // A leftover preference and even an old DOM attribute are inert.
            await page.evaluate(() => {
                localStorage.setItem('yansu.appearance.texture', 'false');
                document.documentElement.dataset.texture = 'on';
            });
            expect(before.equals(await page.screenshot())).toBe(true);
        });
    }
    test(`Chinese Appearance has no Texture setting in ${theme}`, async ({ page }) => {
        await page.goto(`/e2e/brand-violet.html?settings&theme=${theme}&lang=zh-CN`);
        await expect(page.getByText('纹理', { exact: true })).toHaveCount(0);
        await expect(page.getByRole('radiogroup').first()).toBeVisible();
    });
}
