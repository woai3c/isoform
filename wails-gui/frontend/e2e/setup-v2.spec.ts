import { mkdirSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, test, type Page } from '@playwright/test';

// Each gallery still beside the same state of the design prototype
// (docs/handoff-2026-10-29/setup-2.2/, v2.2.0), light and dark, at 600 x 560 @2x.
const repo = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../..');
const prototype = path.join(repo, 'docs/handoff-2026-10-29/setup-2.2/handoff-2026-10-29-1523-setup-v2.2.0-prototype.html');
const out = path.join(repo, 'docs/setup-v2-qa/raw');
mkdirSync(out, { recursive: true });

// How to reach each state in the prototype: the scene index, then clicks.
// ai-picked and finish-tile have no Reduce Motion path there; their design
// stills stand in (see the README).
const PROTO: Record<string, { scene: number; then?: (page: Page) => Promise<void> } | null> = {
    'on-this-mac': { scene: 0, then: (p) => p.click('#local') },
    notices: { scene: 0 },
    'ai-empty': { scene: 1 },
    'ai-picked': null,
    'ai-one': { scene: 1, then: (p) => p.click('#variant [data-v="one"]') },
    'ai-none': { scene: 1, then: (p) => p.click('#variant [data-v="none"]') },
    sees: { scene: 2 },
    'sees-on': { scene: 2, then: async (p) => { await p.click('#permbtn'); await expect(p.locator('#permbtn')).toHaveText('Continue'); } },
    runs: { scene: 3 },
    'runs-off': { scene: 3, then: (p) => p.click('.mr[data-k="after"] .tg') },
    'runs-downloading': { scene: 3, then: (p) => p.click('#mbtn') },
    send: { scene: 4 },
    reaches: { scene: 5 },
    'reaches-paired': { scene: 5, then: (p) => p.click('#qrbox') },
    'finish-tile': null,
};

for (const theme of ['light', 'dark'] as const) {
    for (const [still, proto] of Object.entries(PROTO)) {
        test(`${still} in ${theme}`, async ({ page }) => {
            const errors: string[] = [];
            page.on('pageerror', (error) => errors.push(error.message));
            await page.setViewportSize({ width: 600, height: 560 });
            await page.goto(`/e2e/setup-v2.html?still=${still}&theme=${theme}`);
            await expect(page.getByTestId('setup-v2')).toBeVisible();
            await page.evaluate(() => document.fonts.ready);
            await page.waitForTimeout(400);
            await page.screenshot({ path: path.join(out, `app-${still}-${theme}.png`) });
            expect(errors).toEqual([]);

            if (!proto) return;
            await page.setViewportSize({ width: 1100, height: 900 });
            await page.goto(`file://${prototype}`);
            await page.evaluate((t) => { document.documentElement.dataset.theme = t; }, theme);
            for (let i = 0; i < proto.scene; i++) await page.keyboard.press('ArrowRight');
            if (proto.then) await proto.then(page);
            await page.addStyleTag({ content: '#setup .tl { visibility: hidden !important; } #setup { box-shadow: none !important; border-color: transparent !important; border-radius: 0 !important; }' });
            await page.evaluate(() => document.fonts.ready);
            await page.waitForTimeout(600);
            await page.locator('#setup').screenshot({ path: path.join(out, `proto-${still}-${theme}.png`) });
        });
    }
}
