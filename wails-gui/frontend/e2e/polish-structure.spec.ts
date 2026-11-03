import { test, expect } from '@playwright/test';
import enApp from '../src/i18n/locales/en/app.json' with { type: 'json' };
import zhApp from '../src/i18n/locales/zh-CN/app.json' with { type: 'json' };
import enChat from '../src/i18n/locales/en/chat.json' with { type: 'json' };
import zhChat from '../src/i18n/locales/zh-CN/chat.json' with { type: 'json' };

test.beforeEach(({ page }) => {
    page.on('pageerror', (error) => { console.error('Fixture error:', error.message); });
});

for (const viewport of [1400, 1100]) {
    for (const sidebar of [240, 56]) {
        test(`Today and chat columns at ${viewport}px with ${sidebar}px sidebar`, async ({ page }, testInfo) => {
            await page.setViewportSize({ width: viewport, height: 900 });
            await page.goto(`/e2e/polish-structure.html?sidebar=${sidebar}`);
            const column = page.getByTestId('today-column');
            await expect(column).toBeVisible();
            const area = (await page.getByTestId('page-area').boundingBox())!;
            const panelWidth = area.width >= 920 ? Math.max(360, Math.min(420, Math.round(area.width * 3 / 8))) : 0;
            const today = (await column.boundingBox())!;
            expect(today.width).toBeCloseTo(Math.min(760, area.width - panelWidth), 0);
            expect(today.x + today.width / 2).toBeCloseTo(area.x + (area.width - panelWidth) / 2, 0);
            if (panelWidth) {
                await expect(page.getByTestId('topic-panel-pinned')).toBeVisible();
                expect((await page.getByTestId('topic-panel-pinned').boundingBox())!.width).toBeCloseTo(panelWidth, 0);
            } else await expect(page.getByTestId('topic-panel-pinned')).toHaveCount(0);

            const chips = page.getByTestId('brief-question-chip');
            await expect(chips).toHaveCount(2);
            const chipBounds = await chips.evaluateAll((buttons) => buttons.map((button) => ({
                text: button.textContent ?? '', rect: button.getBoundingClientRect().toJSON(),
                whiteSpace: getComputedStyle(button).whiteSpace,
            })));
            expect(chipBounds[0].rect.y).toBe(chipBounds[1].rect.y);
            for (const chip of chipBounds) {
                expect(Array.from(chip.text).length).toBeLessThanOrEqual(40);
                expect(chip.whiteSpace).toBe('nowrap');
                expect(chip.rect.width).toBeGreaterThan(80);
            }
            if (viewport === 1400 && sidebar === 56) await page.screenshot({ path: testInfo.outputPath('polish-structure-today-1400.png') });

            await page.goto(`/e2e/polish-structure.html?page=chat&sidebar=${sidebar}`);
            const chat = page.getByTestId('main-chat-column');
            await expect(chat).toBeVisible();
            const bounds = (await chat.boundingBox())!;
            expect(bounds.width).toBeCloseTo(768, 0);
            expect(bounds.x + bounds.width / 2).toBeCloseTo(sidebar + (viewport - sidebar) / 2, 0);
            // The latest message stays visible on open. The summary belongs
            // to the conversation's flow above its first row, and scrolls with it.
            await expect(page.getByText('Review the task list, question 39.')).toBeInViewport();
            await page.locator('[data-radix-scroll-area-viewport]').evaluate((element) => { element.scrollTop = 0; });
            await expect(page.getByTestId('main-chat-summary')).toBeInViewport();
            await expect(page.getByTestId('main-chat-summary')).toContainText('In your chat today');
            await expect(page.getByText('Review the task list, question 0.')).toBeInViewport();
            if (viewport === 1400 && sidebar === 240) await page.screenshot({ path: testInfo.outputPath('polish-structure-chat-1400.png') });
        });
    }
}

for (const language of ['en', 'zh-CN']) {
    test(`short, evenly spaced Activity ticks and a 170px label column in ${language}`, async ({ page }, testInfo) => {
        await page.setViewportSize({ width: 1400, height: 900 });
        await page.goto(`/e2e/polish-structure.html?page=activity&lang=${language}`);
        const axis = page.getByTestId('stitched-axis');
        await expect(axis).toBeVisible();
        expect((await axis.boundingBox())!.width).toBeGreaterThan(500);
        const labels = axis.locator(':scope > span');
        await expect(labels).toHaveText(language === 'en' ? ['9 AM', '12 PM'] : ['上午9时', '中午12时']);
        const rightColumn = axis.locator('xpath=following-sibling::*[1]');
        expect((await rightColumn.boundingBox())!.width).toBe(170);
        const first = (await labels.nth(0).boundingBox())!;
        const second = (await labels.nth(1).boundingBox())!;
        expect(second.x).toBeGreaterThan(first.x + first.width);
        if (language === 'en') await page.screenshot({ path: testInfo.outputPath('polish-structure-activity-1400.png') });
    });
}

// Old/retained sessions must not escape the Chat page's reading column.
for (const sidebar of [56, 240]) {
    test(`Polish 5: retained chat stays 768px at 1400px with ${sidebar}px sidebar`, async ({ page }) => {
        await page.setViewportSize({ width: 1400, height: 900 });
        await page.goto(`/e2e/polish-structure.html?page=chat&legacy&sidebar=${sidebar}`);
        const area = (await page.getByTestId('page-area').boundingBox())!;
        const column = (await page.getByTestId('page-area').locator(':scope > div').boundingBox())!;
        expect(column.width).toBe(768);
        expect(column.x + column.width / 2).toBeCloseTo(area.x + area.width / 2, 0);
    });
}

for (const theme of ['paper', 'onedark']) {
    test(`Polish 5: text and scope occupy separate rows in ${theme}`, async ({ page }, testInfo) => {
        await page.setViewportSize({ width: 1400, height: 900 });
        await page.goto(`/e2e/polish-structure.html?page=chat&sidebar=56&theme=${theme}`);
        const text = page.locator('[data-composer-textarea]');
        const scope = page.getByRole('button', { name: 'Choose chat scope' });
        const attach = page.getByRole('button', { name: 'Attach files' });
        await expect(scope).toBeVisible();
        const textBox = (await text.boundingBox())!;
        const scopeBox = (await scope.boundingBox())!;
        const attachBox = (await attach.boundingBox())!;
        expect(scopeBox.y).toBeGreaterThanOrEqual(textBox.y + textBox.height);
        expect(scopeBox.y).toBe(attachBox.y);
        await page.screenshot({ path: testInfo.outputPath(`polish-five-chat-${theme}.png`) });
        await scope.click();
        await page.getByRole('menuitem', { name: 'Halcyon ERP sync' }).click();
        const chip = page.getByTestId('topic-composer-chip');
        await expect(chip).toBeVisible();
        const topicText = (await page.getByRole('textbox').boundingBox())!;
        expect((await chip.boundingBox())!.y).toBeGreaterThanOrEqual(topicText.y + topicText.height);
        expect((await page.getByTestId('topic-composer-toolbar').boundingBox())!.y).toBeGreaterThanOrEqual(topicText.y + topicText.height);
        await page.screenshot({ path: testInfo.outputPath(`polish-five-topic-${theme}.png`) });
        await chip.getByRole('button').click();
        await expect(page.getByRole('button', { name: 'Choose chat scope' })).toBeVisible();
    });
}


for (const width of [477, 478, 696, 699, 700, 760]) {
    test(`Polish 6: topic grid at ${width}px of page width`, async ({ page }) => {
        await page.setViewportSize({ width, height: 1000 });
        await page.goto('/e2e/polish-structure.html?page=topics');
        const column = page.getByTestId('today-column');
        await expect(column).toBeVisible();
        expect((await column.boundingBox())!.width).toBe(width);
        const cards = column.getByTestId('today-topic-card');
        await expect(cards).toHaveCount(6);
        const boxes = await cards.evaluateAll((nodes) => nodes.map((node) => node.getBoundingClientRect().toJSON()));
        const columns = boxes.filter((box) => box.y === boxes[0].y).length;
        expect(columns).toBe(width < 700 ? 2 : 3);
        for (const box of boxes) expect(box.width).toBeGreaterThanOrEqual(200);
    });
}


for (const theme of ['paper', 'onedark']) {
    test(`Polish 6: Activity marks keep their roles in ${theme}`, async ({ page }, testInfo) => {
        await page.setViewportSize({ width: 1400, height: 1000 });
        await page.goto(`/e2e/polish-structure.html?page=activity&theme=${theme}`);
        const marks = page.getByTestId('stitched-legend').locator('[aria-hidden]');
        await expect(marks).toHaveCount(3);
        const colors = await marks.evaluateAll((nodes) => nodes.map((node, i) => {
            const style = getComputedStyle(node);
            return i === 2 ? style.borderTopColor : style.backgroundColor;
        }));
        expect(new Set(colors).size).toBe(3);
        const work = page.locator('[data-thread-lane]:not([data-interruption]) [data-testid="stitched-stretch"]').first();
        const interruption = page.locator('[data-interruption] [data-testid="stitched-stretch"]').first();
        await expect(work).toHaveCSS('background-color', colors[0]);
        await expect(interruption).toHaveCSS('background-color', colors[1]);
        await expect(page.getByTestId('stitched-gap-link').first()).toHaveCSS('border-top-color', colors[2]);
        await expect(page.locator('[data-work-stretch]').first()).toHaveCSS('background-color', colors[0]);
        await page.screenshot({ path: testInfo.outputPath(`polish-six-activity-${theme}.png`), fullPage: true });
    });
}


for (const theme of ['paper', 'onedark']) {
    test(`Round 1: unseen reply navigation floats above the composer in ${theme}`, async ({ page }) => {
        await page.setViewportSize({ width: 1400, height: 1000 });
        await page.goto(`/e2e/polish-structure.html?page=chat&sidebar=56&theme=${theme}`);
        const viewport = page.locator('[data-radix-scroll-area-viewport]');
        await expect(page.getByText('Review the task list, question 39.')).toBeInViewport();
        await viewport.evaluate((element) => { element.scrollTop = 0; });
        await expect(page.getByText('Review the task list, question 0.')).toBeInViewport();
        await page.evaluate(() => window.dispatchEvent(new Event('fixture:new-reply')));
        const button = page.getByRole('button', { name: '1 new reply' });
        await expect(button).toBeVisible();
        const toolbar = page.getByTestId('composer-toolbar');
        await expect(toolbar.getByRole('button', { name: '1 new reply' })).toHaveCount(0);
        const box = (await button.locator('..').boundingBox())!;
        const stream = (await viewport.boundingBox())!;
        expect(box.x + box.width / 2).toBeCloseTo(stream.x + stream.width / 2, 0);
        expect(stream.y + stream.height - box.y - box.height).toBeCloseTo(12, 0);
        await expect(button).toHaveCSS('border-radius', '8px');
        await expect(button).toHaveCSS('position', 'static');
        await button.click();
        await expect(button).toHaveCount(0);
        await expect(page.getByText('A new reply for this layout check.')).toBeInViewport();
    });
}


for (const theme of ['paper', 'onedark']) {
    for (const language of ['en', 'zh-CN']) {
        for (const layout of ['450-window', 'today-min', 'today-max', 'compact-inline']) {
            test(`Round 2: all composer controls fit ${layout} in ${language}/${theme}`, async ({ page }, testInfo) => {
                const app = language === 'en' ? enApp : zhApp;
                const chat = language === 'en' ? enChat : zhChat;
                const viewportWidth = layout === '450-window' ? 450 : 1400;
                const size = layout === 'today-min' ? '&panel=920' : layout === 'today-max' ? '&panel=1120' : layout === 'compact-inline' ? '&column=520' : '';
                await page.setViewportSize({ width: viewportWidth, height: 1000 });
                await page.goto(`/e2e/polish-structure.html?page=chat&sidebar=56&crowded&theme=${theme}&lang=${language}${size}`);
                if (layout.startsWith('today-')) expect((await page.getByTestId('narrow-chat-column').boundingBox())!.width).toBe(layout === 'today-min' ? 360 : 420);
                const toolbar = page.getByTestId('composer-toolbar');
                const scope = toolbar.getByRole('button', { name: chat.mainChat.view.scope });
                const send = toolbar.getByTitle(app.composer.actions.queueMessage, { exact: true });
                const stop = toolbar.getByTitle(app.composer.actions.stopStreaming, { exact: true });
                const voice = toolbar.locator('button').filter({ has: page.locator('svg.lucide-mic') });
                await expect(toolbar.getByRole('combobox')).toContainText('Extra High');
                const icons = [scope, send, stop, voice];
                for (const icon of icons) expect((await icon.boundingBox())!.width).toBeCloseTo(32, 1);
                const viewport = page.locator('[data-radix-scroll-area-viewport]');
                await expect(page.getByText('Review the task list, question 39.')).toBeInViewport();
                await viewport.evaluate((element) => { element.scrollTop = 0; });
                await expect(page.getByText('Review the task list, question 0.')).toBeInViewport();
                await page.evaluate(() => {
                    for (let i = 0; i < 3; i++) window.dispatchEvent(new Event('fixture:new-reply'));
                });
                const fullLabel = app.composer.newReplies_other.replace('{{count}}', '3');
                const reply = page.getByRole('button', { name: fullLabel, exact: true });
                await expect(reply).toBeVisible();
                const surface = (await toolbar.locator('..').boundingBox())!;
                for (const control of [...icons, toolbar.getByRole('combobox')]) {
                    const rect = (await control.boundingBox())!;
                    expect(rect.x).toBeGreaterThanOrEqual(surface.x);
                    expect(rect.x + rect.width).toBeLessThanOrEqual(surface.x + surface.width);
                }
                for (const icon of icons) expect((await icon.boundingBox())!.width).toBeCloseTo(32, 1);
                await expect(reply).toHaveAttribute('title', fullLabel);
                await expect(reply).toHaveAttribute('aria-label', fullLabel);
                await expect(toolbar.getByRole('button', { name: fullLabel, exact: true })).toHaveCount(0);
                const box = (await reply.locator('..').boundingBox())!;
                const stream = (await viewport.boundingBox())!;
                expect(box.x + box.width / 2).toBeCloseTo(stream.x + stream.width / 2, 0);
                expect(stream.y + stream.height - box.y - box.height).toBeCloseTo(12, 0);
                expect(await toolbar.locator('..').evaluate((element) => element.scrollWidth <= element.clientWidth)).toBe(true);
                if (layout === '450-window') await page.screenshot({ path: testInfo.outputPath(`round-two-${language}-${theme}.png`) });
                await reply.click();
                await expect(reply).toHaveCount(0);
                await expect(page.getByText('A new reply for this layout check.').last()).toBeInViewport();
            });
        }
    }
}
