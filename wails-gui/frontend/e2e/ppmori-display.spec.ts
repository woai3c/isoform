import { writeFile } from 'node:fs/promises';
import { expect, test, type Page } from '@playwright/test';

test.beforeEach(async ({ page }) => {
    // Fixture reads are in memory. No app bridge or remote service may be contacted.
    await page.route('**/*', route => new URL(route.request().url()).origin === 'http://127.0.0.1:34119'
        ? route.continue() : route.abort());
});

// Scan computed styles on every rendered element, including classless children
// and portaled menus. Checking class names alone misses inherited font leaks.
async function audit(page: Page) {
    const result = await page.evaluate(() => {
        const leaks: string[] = [];
        const weights: string[] = [];
        for (const element of document.querySelectorAll('body *')) {
            const style = getComputedStyle(element);
            if (!/PP\s?Mori/.test(style.fontFamily)) continue;
            const root = element.closest('.display, .portrait');
            if (!root) leaks.push(element.outerHTML.slice(0, 180));
            if (style.fontWeight !== '400' && !(style.fontWeight === '600' &&
                element.matches('.display b, .display strong'))) weights.push(element.outerHTML.slice(0, 180));
        }
        return { leaks, weights, targets: Array.from(document.querySelectorAll('.display, .portrait')).map(element => {
            const style = getComputedStyle(element);
            return { tag: element.tagName, kind: element.classList.contains('portrait') ? 'portrait' : 'display',
                text: element.textContent, family: style.fontFamily, weight: style.fontWeight,
                size: style.fontSize, leading: style.lineHeight, tracking: style.letterSpacing };
        }) };
    });
    expect(result.leaks).toEqual([]);
    expect(result.weights).toEqual([]);
    for (const target of result.targets) {
        expect(target.family).toMatch(/^"?PP Mori"?,/);
        expect(target.weight).toBe('400');
        expect(target.size).toBe(target.kind === 'portrait' ? '19px' : '30px');
        expect(target.leading).toBe(target.kind === 'portrait' ? '31.35px' : '37.5px');
        expect(target.tracking).toBe(target.kind === 'portrait' ? 'normal' : '-0.3px');
    }
    return result;
}

for (const theme of ['paper', 'onedark']) {
    for (const lang of ['en', 'zh-CN']) {
        for (const surface of ['today', 'activity', 'portrait', 'settings']) {
            test(`${surface} confines PP Mori to its display moment in ${theme}, ${lang}`, async ({ page }, testInfo) => {
                const errors: string[] = [];
                page.on('pageerror', error => errors.push(error.message));
                await page.goto(`/e2e/ppmori-display.html?page=${surface}&theme=${theme}&lang=${lang}`);
                const target = page.locator(surface === 'portrait' ? 'p.portrait' : '.display');
                if (surface === 'settings') await expect(page.getByRole('radiogroup').first()).toBeVisible();
                else await expect(target).toBeVisible();
                await page.evaluate(() => document.fonts.ready);
                const result = await audit(page);
                expect(result.targets).toHaveLength(surface === 'settings' ? 0 : 1);
                await page.screenshot({ path: testInfo.outputPath(`${surface}-${theme}-${lang}.png`) });
                if (surface === 'portrait') {
                    // Source-backed phrases keep the paragraph's 400 face and still open the belief.
                    const phrase = target.getByRole('button');
                    await expect(phrase).toHaveCSS('font-size', '19px');
                    await expect(phrase).toHaveCSS('font-weight', '400');
                    await phrase.click();
                    await expect(page.getByRole('heading', { name: 'Writing style', exact: true })).toBeVisible();
                    await audit(page);
                }
                if (surface === 'today') {
                    expect(result.targets[0].text).toBe(lang === 'en' ? 'Morning brief' : '早间简报');
                    // The brief body retains its text and system font beneath the display title.
                    await expect(page.getByText('Good morning. Your weekly Friday 1:1', { exact: false })).toBeVisible();
                }
                expect(errors).toEqual([]);
                await writeFile(testInfo.outputPath('computed.json'), JSON.stringify(result, null, 4) + '\n');
            });
        }
    }

    test(`all bundled faces decode in WebKit in ${theme}`, async ({ page }) => {
        await page.goto(`/e2e/ppmori-display.html?page=today&theme=${theme}`);
        await expect(page.locator('h1.display')).toBeVisible();
        const faces = await page.evaluate(async () => {
            await document.fonts.load('400 30px "PP Mori"');
            await document.fonts.load('italic 400 30px "PP Mori"');
            await document.fonts.load('600 30px "PP Mori"');
            return Array.from(document.fonts).filter(face => face.family === 'PP Mori' || face.family === '"PP Mori"')
                .map(face => ({ weight: face.weight, style: face.style, display: face.display, status: face.status }));
        });
        expect(faces).toEqual([
            { weight: '400', style: 'normal', display: 'optional', status: 'loaded' },
            { weight: '400', style: 'italic', display: 'optional', status: 'loaded' },
            { weight: '600', style: 'normal', display: 'optional', status: 'loaded' },
        ]);
    });

    test(`evening, recorded and empty states preserve display scope in ${theme}`, async ({ page }) => {
        await page.goto(`/e2e/ppmori-display.html?page=today&theme=${theme}&slot=evening`);
        await expect(page.locator('h1.display')).toHaveText('Evening brief');
        expect((await audit(page)).targets).toHaveLength(1);
        await page.goto(`/e2e/ppmori-display.html?page=activity&theme=${theme}&recorded`);
        await expect(page.locator('p.display')).toBeVisible();
        expect((await audit(page)).targets).toHaveLength(1);
        await page.goto(`/e2e/ppmori-display.html?page=activity&theme=${theme}&state=empty`);
        await expect(page.locator('main')).toContainText('What you worked on');
        expect((await audit(page)).targets).toHaveLength(0);
        for (const state of ['empty', 'stale', 'failed', 'loading']) {
            await page.goto(`/e2e/ppmori-display.html?page=portrait&theme=${theme}&state=${state}`);
            await expect(page.locator('#about-you-portrait')).toBeVisible();
            if (state === 'loading') await expect(page.locator('[aria-busy="true"]')).toBeVisible();
            else if (state === 'failed') await expect(page.getByRole('alert')).toBeVisible();
            else await expect(page.getByText(state === 'empty' ? /No portrait yet/ : /A belief in your portrait/)).toBeVisible();
            expect((await audit(page)).targets).toHaveLength(0);
        }
    });

    test(`chat stream, composer, tags and menus retain the system font in ${theme}`, async ({ page }) => {
        await page.goto(`/e2e/polish-structure.html?page=chat&theme=${theme}`);
        await expect(page.getByText('Review the task list, question 39.')).toBeVisible();
        expect((await audit(page)).targets).toHaveLength(0);
    });

    test(`a missing optional font preserves display metrics in ${theme}`, async ({ page }) => {
        await page.route('**/PPMori-*.woff2', route => route.abort());
        await page.goto(`/e2e/ppmori-display.html?page=today&theme=${theme}`);
        await expect(page.locator('h1.display')).toBeVisible();
        await page.evaluate(() => document.fonts.ready);
        await audit(page);
        const widths = await page.locator('h1.display').evaluate(node => {
            const range = document.createRange();
            range.selectNodeContents(node);
            const fallback = range.getBoundingClientRect().width;
            (node as HTMLElement).style.fontFamily = 'var(--font-sans)';
            return { fallback, system: range.getBoundingClientRect().width };
        });
        expect(widths.fallback).toBe(widths.system);
    });
}
