import { expect, test } from '@playwright/test';

for (const language of ['en', 'zh-CN']) {
    test('topic text stays inside its containers from 720 px up in ' + language, async ({ page }, testInfo) => {
        const errors: string[] = [];
        page.on('pageerror', error => errors.push(error.message));
        // Any accidental bridge call fails this isolated component test.
        await page.route('**/api/**', route => { errors.push('Unexpected backend request'); return route.abort(); });
        await page.setViewportSize({ width: 720, height: 900 });
        await page.goto('/e2e/topic-wrapping.html?language=' + language);
        await expect(page.getByTestId('topic-panel-user').first()).toBeVisible();
        await expect(page.getByTestId('brief-question-chip')).toHaveCount(2);
        await page.evaluate(() => document.fonts.ready);
        // Every integer window width, including both sides of the 700 px Today
        // container breakpoint; also exercise larger windows without a fixed cap.
        for (const width of [...Array.from({ length: 1201 }, (_, index) => 720 + index), 2560, 3840]) {
            await page.setViewportSize({ width, height: 900 });
            const overflow = await page.evaluate(() => {
                const failures: string[] = [];
                if (document.documentElement.scrollWidth > innerWidth) failures.push('document');
                for (const node of document.querySelectorAll<HTMLElement>('[data-testid="wrapping-fixture"] *')) {
                    if (!node.getClientRects().length || node instanceof HTMLTextAreaElement || node instanceof SVGElement || node.classList.contains('sr-only')) continue;
                    if (node.scrollWidth > node.clientWidth + 1 && getComputedStyle(node).display !== 'inline') failures.push(node.dataset.testid ?? node.tagName);
                }
                return [...new Set(failures)];
            });
            expect(overflow, 'horizontal overflow at ' + width + ' px').toEqual([]);
            if ([720, 1440].includes(width)) {
                expect(await page.locator('.today-topics-grid').evaluate(node => getComputedStyle(node).gridTemplateColumns.split(' ').length)).toBe(width === 720 ? 2 : 3);
                const cards = page.getByTestId('today-topic-card');
                const before = await cards.evaluateAll(nodes => nodes.map(node => node.getBoundingClientRect().toJSON()));
                await page.evaluate(() => (window as any).setTopicRefreshing(true));
                await expect(page.getByTestId('today-topics-refreshing')).toBeVisible();
                expect(await cards.evaluateAll(nodes => nodes.map(node => node.getBoundingClientRect().toJSON()))).toEqual(before);
                await page.evaluate(() => (window as any).setTopicRefreshing(false));
                await expect(page.getByTestId('today-topics-refreshing')).toHaveCount(0);
                expect(await cards.evaluateAll(nodes => nodes.map(node => node.getBoundingClientRect().toJSON()))).toEqual(before);
                await page.getByTestId('wrapping-fixture').evaluate(node => { node.scrollTop = 0; });
                await page.screenshot({ path: testInfo.outputPath(language + '-' + width + '.png') });
                await page.getByTestId('narrow-detail').screenshot({ path: testInfo.outputPath(language + '-' + width + '-detail.png') });
                await page.getByTestId('wrapping-fixture').evaluate(node => { node.scrollTop = 0; });
            }
        }
        expect(errors).toEqual([]);
    });
}
