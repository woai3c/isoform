import { test, expect, type Locator } from '@playwright/test';

async function expectWaitingToken(summary: Locator, label: string, otherLabels: string[] = []) {
    const waiting = summary.getByText(label, { exact: true });
    await expect(waiting).toHaveClass('text-warning');
    const colors = await waiting.evaluate((element) => {
        const token = document.createElement('span');
        token.style.color = 'var(--color-warning)';
        document.body.append(token);
        try {
            return { actual: getComputedStyle(element).color, token: getComputedStyle(token).color };
        } finally {
            token.remove();
        }
    });
    expect(colors.actual).toBe(colors.token);
    const muted = await summary.evaluate((element) => getComputedStyle(element).color);
    expect(colors.actual).not.toBe(muted);
    for (const label of otherLabels) {
        const item = summary.getByText(label, { exact: true });
        await expect(item).not.toHaveClass(/text-warning/);
        expect(await item.evaluate((element) => getComputedStyle(element).color)).toBe(muted);
    }
}

for (const language of ['en', 'zh-CN']) {
    test(`sourced zero waiting uses the warning token in ${language}`, async ({ page }) => {
        await page.setViewportSize({ width: 1400, height: 900 });
        await page.goto(`/e2e/polish-structure.html?page=chat&summary=zero&lang=${language}`);
        const summary = page.getByTestId('main-chat-summary');
        await expect(summary).toHaveText(language === 'en'
            ? 'In your chat today · 0 waiting on you'
            : '今天聊天里有 · 0 件等你处理');
        await expectWaitingToken(summary, language === 'en' ? '0 waiting on you' : '0 件等你处理');
    });

    test(`the summary remains one line in a narrow column in ${language}`, async ({ page }) => {
        await page.setViewportSize({ width: 740, height: 900 });
        await page.goto(`/e2e/polish-structure.html?page=chat&summary=full&lang=${language}`);
        const summary = page.getByTestId('main-chat-summary');
        await expect(summary).toBeAttached();
        const lines = await summary.evaluate((element) => {
            const range = document.createRange();
            range.selectNodeContents(element);
            return Array.from(range.getClientRects()).map((rect) => rect.y);
        });
        expect(new Set(lines).size).toBe(1);
        expect(await summary.evaluate((element) => getComputedStyle(element).whiteSpace)).toBe('nowrap');
        // Keep the whole line available without widening the page or truncating copy.
        expect(await page.getByTestId('page-area').evaluate((element) => element.scrollWidth)).toBe(500);
        expect(await summary.evaluate((element) => getComputedStyle(element).overflowX)).toBe('auto');
    });

    test(`the summary never covers the first commitment title in ${language}`, async ({ page }, testInfo) => {
        await page.setViewportSize({ width: 1400, height: 840 });
        await page.goto(`/e2e/polish-structure.html?page=chat&summary=1&lang=${language}`);
        const summary = page.getByTestId('main-chat-summary');
        await expect(summary).toBeAttached();
        const title = page.getByRole('heading', { name: 'Review the release commitment 1.' });
        await expect(title).toBeAttached();
        const viewport = page.locator('[data-radix-scroll-area-viewport]');
        // Wait for the stream's initial follow-latest scroll and the overview read.
        await expect.poll(() => viewport.evaluate((element) => element.scrollTop)).toBeGreaterThan(0);
        const titleBox = (await title.boundingBox())!;
        const stripBox = (await summary.boundingBox())!;
        const viewportBox = (await viewport.boundingBox())!;
        expect(titleBox.y).toBeGreaterThanOrEqual(stripBox.y + stripBox.height);
        expect(titleBox.y).toBeGreaterThanOrEqual(viewportBox.y);
        expect(titleBox.y + titleBox.height).toBeLessThanOrEqual(viewportBox.y + viewportBox.height);
        await expect(title).toBeInViewport({ ratio: 1 });
        expect(await title.evaluate((element) => {
            const bounds = element.getBoundingClientRect();
            return [bounds.top + 1, bounds.bottom - 1].every((y) => element.contains(document.elementFromPoint(bounds.left + 1, y)));
        })).toBe(true);
        await expect(page.locator('[data-proactive-moment="commitment-0"] [data-proactive-action="open"]')).toBeInViewport();

        // At the top, the strip precedes the whole first card in normal flow.
        await viewport.evaluate((element) => { element.scrollTop = 0; });
        await expect(summary).toBeInViewport({ ratio: 1 });
        await expect(title).toBeInViewport({ ratio: 1 });
        expect((await title.boundingBox())!.y).toBeGreaterThan((await summary.boundingBox())!.y + (await summary.boundingBox())!.height);
        const filters = page.getByTestId('main-chat-filter-row');
        const filterTop = (await filters.boundingBox())!.y;
        expect(await filters.evaluate((element) => getComputedStyle(element).position)).toBe('sticky');
        expect(await summary.evaluate((element) => {
            const viewport = element.closest('[data-radix-scroll-area-viewport]');
            for (let node: Element | null = element; node && node !== viewport; node = node.parentElement) {
                if (['sticky', 'fixed'].includes(getComputedStyle(node).position)) return false;
            }
            return Boolean(viewport);
        })).toBe(true);
        await page.screenshot({ path: testInfo.outputPath(`chat-summary-${language}.png`) });
        const summaryTop = (await summary.boundingBox())!.y;
        await viewport.evaluate((element) => { element.scrollTop = 60; });
        await expect.poll(async () => (await summary.boundingBox())!.y).toBeLessThan(summaryTop);
        expect((await filters.boundingBox())!.y).toBe(filterTop);
    });

    test(`the entire summary shares one line at 768px in ${language}`, async ({ page }, testInfo) => {
        await page.setViewportSize({ width: 1400, height: 900 });
        await page.goto(`/e2e/polish-structure.html?page=chat&summary=full&lang=${language}`);
        const summary = page.getByTestId('main-chat-summary');
        await expect(summary).toBeAttached();
        expect((await page.getByTestId('main-chat-column').boundingBox())!.width).toBe(768);
        expect(await summary.textContent()).toBe(language === 'en'
            ? 'In your chat today · 1 waiting on you · 3 with you · 1 finished · 9 routine, folded'
            : '今天聊天里有 · 1 件等你处理 · 3 段与你的聊天 · 1 项已完成 · 9 条例行更新，已折叠');
        const lines = await summary.evaluate((element) => {
            const range = document.createRange();
            range.selectNodeContents(element);
            return Array.from(range.getClientRects()).map((rect) => rect.y);
        });
        expect(new Set(lines).size).toBe(1);
        await expectWaitingToken(summary,
            language === 'en' ? '1 waiting on you' : '1 件等你处理',
            language === 'en' ? ['3 with you', '1 finished', '9 routine, folded'] : ['3 段与你的聊天', '1 项已完成', '9 条例行更新，已折叠'],
        );
        expect(await summary.evaluate((element) => element.scrollWidth)).toBeLessThanOrEqual((await summary.boundingBox())!.width);
        await page.locator('[data-radix-scroll-area-viewport]').evaluate((element) => { element.scrollTop = 0; });
        await page.screenshot({ path: testInfo.outputPath(`chat-summary-full-${language}.png`) });
    });
}
