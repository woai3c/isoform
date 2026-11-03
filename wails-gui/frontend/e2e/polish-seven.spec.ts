import { test, expect } from '@playwright/test';

for (const theme of ['paper', 'onedark']) {
    for (const language of ['en', 'zh-CN']) {
        for (const width of [768, 420, 360]) {
            test(`Polish 7 header: ${width}px ${language}/${theme}`, async ({ page }, testInfo) => {
                await page.setViewportSize({ width: 1400, height: 900 });
                await page.goto(`/e2e/polish-structure.html?page=chat&sidebar=56&many-topics&column=${width}&lang=${language}&theme=${theme}`);
                const row = page.getByTestId('main-chat-filter-row');
                const more = row.getByRole('button', { name: language === 'en' ? 'More' : '更多', exact: true });
                await expect(more).toBeVisible();
                const children = row.locator('[data-header-row] > div:not([aria-hidden])');
                const boxes = await children.evaluateAll(elements => elements.map(el => { const rect = el.getBoundingClientRect(); return { top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right }; }));
                expect(new Set(boxes.map(box => box.top)).size).toBe(1);
                for (let i = 1; i < boxes.length; i++) expect(boxes[i].left).toBeGreaterThanOrEqual(boxes[i - 1].right);
                const controls = await row.locator('h2, button').evaluateAll(elements => elements.map(el => { const rect = el.getBoundingClientRect(); return { left: rect.left, right: rect.right }; }));
                for (let i = 1; i < controls.length; i++) expect(controls[i].left).toBeGreaterThanOrEqual(controls[i - 1].right);
                expect(await row.evaluate(el => el.scrollWidth <= el.clientWidth)).toBe(true);
                const visible = await row.locator('[data-main-chat-filter^="topic:"]').allTextContents();
                await page.screenshot({ path: testInfo.outputPath(`polish-seven-header-${width}-${language}-${theme}.png`) });
                await more.click();
                const hidden = await page.getByRole('menuitem').allTextContents();
                expect([...visible, ...hidden]).toEqual(['Topic 8', 'Topic 11', 'Topic 10', 'Topic 9', 'Topic 7', 'Topic 6', 'Topic 5', 'Topic 4', 'Topic 3', 'Topic 2', 'Topic 1', 'Topic 0']);
            });
        }
    }
}

for (const theme of ['paper', 'onedark']) {
    test(`Polish 7 jump placement and following in ${theme}`, async ({ page }, testInfo) => {
        await page.setViewportSize({ width: 1000, height: 900 });
        await page.goto(`/e2e/polish-structure.html?page=chat&sidebar=56&many-topics&column=768&theme=${theme}`);
        const viewport = page.locator('[data-radix-scroll-area-viewport]');
        await expect(page.getByText('Review the task list, question 39.')).toBeInViewport();
        await viewport.evaluate(element => { element.scrollTop = 0; });
        const bare = page.getByRole('button', { name: 'Jump to newest replies', exact: true });
        await expect(bare).toBeVisible();
        await expect(bare).toHaveText('');
        await page.evaluate(() => { for (let i = 0; i < 2; i++) window.dispatchEvent(new Event('fixture:new-reply')); });
        const jump = page.getByRole('button', { name: '2 new replies', exact: true });
        await expect(jump).toBeVisible();
        const wrapper = (await jump.locator('..').boundingBox())!;
        const stream = (await viewport.boundingBox())!;
        expect(wrapper.x + wrapper.width / 2).toBeCloseTo(stream.x + stream.width / 2, 0);
        expect(stream.y + stream.height - wrapper.y - wrapper.height).toBeCloseTo(12, 0);
        await page.getByTestId('main-chat-column').screenshot({ path: testInfo.outputPath(`polish-seven-chat-${theme}.png`) });
        await jump.click();
        await expect(jump).toHaveCount(0);
        await expect(page.getByText('A new reply for this layout check.').last()).toBeInViewport();
        await page.evaluate(() => window.dispatchEvent(new Event('fixture:new-reply')));
        await expect(page.getByText('A new reply for this layout check.').last()).toBeInViewport();
        await expect(page.getByTestId('jump-to-newest')).toHaveCount(0);
    });
}

for (const theme of ['paper', 'onedark']) {
    for (const width of [360, 420]) {
        for (const kind of ['topic', 'main', 'main-preview']) {
            test(`Round 1 composer gap: ${kind} ${width}px ${theme}`, async ({ page }, testInfo) => {
                await page.setViewportSize({ width: 1000, height: 900 });
                await page.goto(`/e2e/polish-structure.html?page=reply-panel&kind=${kind === 'topic' ? 'topic' : 'main'}&column=${width}&theme=${theme}${kind === 'main-preview' ? '&preview' : ''}`);
                const list = page.getByTestId(kind === 'topic' ? 'topic-panel-list' : 'main-chat-panel-list');
                await expect(page.getByText('Review the task list, question 39.')).toBeInViewport();
                await list.evaluate(element => { element.scrollTop = 0; });
                await expect(page.getByRole('button', { name: 'Jump to newest replies' })).toBeVisible();
                await page.evaluate(() => window.dispatchEvent(new Event('fixture:new-reply')));
                const jump = page.getByRole('button', { name: '1 new reply' });
                await expect(jump).toBeVisible();
                const pill = (await jump.locator('..').boundingBox())!;
                const composer = (await page.locator('[data-topic-composer-surface]').boundingBox())!;
                expect(composer.y - pill.y - pill.height).toBeCloseTo(12, 0);
                expect(pill.x + pill.width / 2).toBeCloseTo(composer.x + composer.width / 2, 0);
                if (kind === 'main-preview') {
                    const preview = page.locator('[data-today-context-preview]');
                    await expect(preview).toBeVisible();
                    expect((await preview.boundingBox())!.y).toBeGreaterThanOrEqual(composer.y + composer.height);
                }
                await page.screenshot({ path: testInfo.outputPath(`round-one-${kind}-${width}-${theme}.png`) });
                await jump.click();
                await expect(jump).toHaveCount(0);
                await expect(page.getByText('A new panel reply.')).toBeInViewport();
            });
        }
    }
}
