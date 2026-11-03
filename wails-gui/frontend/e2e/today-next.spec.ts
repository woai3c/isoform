import { test, expect } from '@playwright/test';
import { todayNextBrief } from '../src/components/today/today-next.fixture';

for (const theme of ['paper', 'onedark']) {
    for (const language of ['en', 'zh-CN']) {
        test(`Today next preserves copy, fits topics in the first screen, and only previews answers in ${language}/${theme}`, async ({ page }, info) => {
            await page.clock.setFixedTime(new Date('2026-10-23T10:00:00-07:00'));
            await page.setViewportSize({ width: 1440, height: 1000 });
            await page.goto(`/e2e/polish-structure.html?today-next&many-topics&sidebar=56&theme=${theme}&lang=${language}`);
            const title = page.getByTestId('today-header').getByRole('heading', { level: 1 });
            await expect(title).toHaveCSS('font-size', '30px');
            await expect(title).toHaveCSS('line-height', '37.5px');
            await expect(title).toHaveCSS('letter-spacing', '-0.3px');
            const card = (await page.getByTestId('brief-card').boundingBox())!;
            expect((await title.boundingBox())!.x).toBe(card.x);
            const meta = page.getByTestId('today-status');
            await expect(meta).toHaveCSS('flex-direction', 'row');
            expect((await meta.boundingBox())!.height).toBeLessThan(55);
            const paragraphs = page.getByTestId('brief-narrative').locator('p');
            expect(await paragraphs.allTextContents()).toEqual(todayNextBrief.narrative.slice(0, -1).map((p) => p.text));
            const topics = page.getByTestId('today-topics');
            expect((await topics.boundingBox())!.y).toBeLessThan(1000);
            await expect(topics.getByRole('heading')).toBeInViewport();
            const track = page.getByTestId('today-day-track');
            await expect(track).toHaveCSS('height', '40px');
            await expect(track).toHaveClass(/patina-ground/);
            const events = page.getByTestId('today-day-event');
            const blocks = await events.evaluateAll((nodes) => nodes.map((n) => n.getBoundingClientRect().toJSON()));
            expect(blocks[2].x - blocks[1].x - blocks[1].width).toBeCloseTo(2, 0);
            const before = await paragraphs.allTextContents();
            const now = await page.getByTestId('today-day-now').boundingBox();
            const answers = page.getByTestId('brief-conversation-actions').getByRole('button');
            await answers.nth(0).click();
            await expect(page.getByTestId('today-day-hold')).toHaveAttribute('data-state', 'held');
            const box = page.getByTestId('topic-panel-pinned').locator('textarea');
            await expect(box).toHaveValue(todayNextBrief.cards!.find((c) => c.id === 'hold')!.message);
            expect(await paragraphs.allTextContents()).toEqual(before);
            expect(await page.getByTestId('today-day-now').boundingBox()).toEqual(now);
            expect(await page.evaluate(() => (window as any).fixtureCalls.filter((method: string) => /^(Send|Resolve|Create|Update)/.test(method)))).toEqual([]);
            await page.screenshot({ path: info.outputPath(`today-next-${language}-${theme}.png`), fullPage: true });
            await answers.nth(1).click();
            await expect(page.getByTestId('today-day-hold')).toHaveCount(0);
            await expect(box).toHaveValue(todayNextBrief.cards!.find((c) => c.id === 'decline')!.message);
            const send = page.getByTestId('topic-panel-pinned').getByRole('button', { name: language === 'en' ? 'Send' : '发送', exact: true });
            // The native sender remains behind the composer's explicit Send.
            await send.click();
            await expect.poll(() => page.evaluate(() => (window as any).fixtureCalls.filter((method: string) => method === 'SendMainChatWithContext').length)).toBe(1);
        });
    }
}
for (const width of [450, 699, 700, 760]) {
    test(`topic columns follow page width at ${width}`, async ({ page }) => {
        await page.setViewportSize({ width, height: 1000 });
        await page.goto('/e2e/polish-structure.html?page=topics');
        const cards = page.getByTestId('today-topic-card');
        await expect(cards).toHaveCount(6);
        const tops = await cards.evaluateAll((nodes) => nodes.map((n) => n.getBoundingClientRect().top));
        expect(tops.filter((y) => y === tops[0])).toHaveLength(width < 700 ? 2 : 3);
    });
}
for (const state of ['empty', 'disconnected', 'overlap']) {
    test(`day strip ${state} state`, async ({ page }) => {
        await page.goto(`/e2e/polish-structure.html?today-next&${state}&sidebar=56`);
        const track = page.getByTestId('today-day-track');
        if (state === 'disconnected') await expect(track).toHaveCount(0);
        else if (state === 'overlap') {
            await expect(track).toHaveCSS('height', '40px');
            const events = page.getByTestId('today-day-event');
            const longer = (await events.nth(0).boundingBox())!;
            const shorter = (await events.nth(1).boundingBox())!;
            expect(shorter.y - longer.y).toBe(4);
            expect(longer.y + longer.height - shorter.y - shorter.height).toBe(4);
        }
        else {
            await expect(track).toHaveText('Nothing on your calendar today');
            await expect(page.getByTestId('today-day-now')).toBeVisible();
        }
    });
}

for (const sidebar of [56, 240]) {
    test(`the 200-word brief leaves topics inside a 1000px screen with ${sidebar}px sidebar`, async ({ page }) => {
        expect(todayNextBrief.narrative.map((p) => p.text).join(' ').split(/\s+/)).toHaveLength(200);
        await page.clock.setFixedTime(new Date('2026-10-23T10:00:00-07:00'));
        await page.setViewportSize({ width: 1440, height: 1000 });
        await page.goto(`/e2e/polish-structure.html?today-next&many-topics&sidebar=${sidebar}`);
        const heading = page.getByTestId('today-topics').getByRole('heading');
        await expect(heading).toBeInViewport();
        expect((await heading.boundingBox())!.y).toBeLessThan(1000);
    });
}

test('tomorrow omits now, while a late same-day marker stays within the strip', async ({ page }) => {
    await page.goto('/e2e/polish-structure.html?today-next&tomorrow&sidebar=56');
    await expect(page.getByTestId('today-day-now')).toHaveCount(0);
    await expect(page.getByTestId('today-day-caption')).toContainText('Tomorrow');
    await page.goto('/e2e/polish-structure.html?today-next&late&sidebar=56');
    const marker = page.getByTestId('today-day-now');
    await expect(marker).toBeVisible();
    const mark = (await marker.boundingBox())!;
    const strip = (await page.getByTestId('today-day-track').boundingBox())!;
    expect(mark.x).toBeGreaterThanOrEqual(strip.x);
    expect(mark.x + mark.width).toBeLessThanOrEqual(strip.x + strip.width);
    await expect(page.getByTestId('today-day-event-group').first()).toHaveCSS('opacity', '0.45');
});

for (const language of ['en', 'zh-CN']) {
    test(`overnight range keeps the requested day and paused capture stays a footer link in ${language}`, async ({ page }) => {
        await page.clock.setFixedTime(new Date('2026-10-23T10:00:00-07:00'));
        await page.goto(`/e2e/polish-structure.html?today-next&overnight&paused-capture&sidebar=56&lang=${language}`);
        await expect(page.getByTestId('today-day-now')).toBeVisible();
        await expect(page.getByTestId('today-day-caption')).not.toContainText(language === 'en' ? 'Thursday' : '星期四');
        const footer = page.getByTestId('today-quiet-footer');
        const capture = footer.getByTestId('today-capture');
        await expect(capture).toHaveRole('button');
        await capture.click();
        await expect(page.getByTestId('today-header').getByTestId('today-capture')).toHaveCount(0);
    });
}

for (const width of [450, 1440]) {
    test(`overlap rims remain 4px at ${width}px`, async ({ page }, info) => {
        await page.setViewportSize({ width, height: 1000 });
        await page.goto('/e2e/polish-structure.html?today-next&overlap&sidebar=56');
        const track = page.getByTestId('today-day-track');
        await expect(track).toHaveCSS('height', width === 450 ? '32px' : '40px');
        const events = page.getByTestId('today-day-event');
        const longer = (await events.nth(0).boundingBox())!;
        const shorter = (await events.nth(1).boundingBox())!;
        expect(shorter.y - longer.y).toBe(4);
        expect(longer.y + longer.height - shorter.y - shorter.height).toBe(4);
        await page.screenshot({ path: info.outputPath(`day-strip-overlap-${width}.png`), fullPage: true });
    });
}

test('partial overlap keeps Alexandra in one full-span label above both background pieces', async ({ page }, info) => {
    await page.setViewportSize({ width: 1440, height: 1000 });
    await page.goto('/e2e/polish-structure.html?page=day-strip&sidebar=56&theme=paper');
    const track = page.getByTestId('today-day-track');
    await expect(track).toHaveCSS('width', '1100px');
    await expect(track).toHaveCSS('height', '40px');
    const name = track.getByText('Alexandra', { exact: true });
    await expect(name).toHaveCount(1);
    await expect(name).toBeVisible();
    const dimensions = await name.evaluate((node) => ({ width: node.getBoundingClientRect().width, scroll: node.scrollWidth, client: node.clientWidth }));
    expect(dimensions.width).toBeGreaterThan(64);
    expect(dimensions.width).toBeLessThan(65);
    expect(dimensions.scroll).toBeLessThanOrEqual(dimensions.client);
    const labelLayer = await name.evaluate((node) => Number(getComputedStyle(node).zIndex));
    const blockLayers = await page.getByTestId('today-day-event').evaluateAll((nodes) => nodes.map((node) => Number(getComputedStyle(node).zIndex)));
    expect(labelLayer).toBeGreaterThan(Math.max(...blockLayers));
    await expect(track.getByText('Planning', { exact: true })).toHaveCount(1);
    await track.screenshot({ path: info.outputPath('partial-overlap-alexandra.png') });
});

for (const [theme, minimumContrast] of [['paper', 2.6], ['onedark', 3.6]] as const) {
    test(`ended Jenny keeps its single-fade label contrast in ${theme}`, async ({ page }, info) => {
        await page.setViewportSize({ width: 1440, height: 1000 });
        await page.goto(`/e2e/polish-structure.html?today-next&late&sidebar=56&theme=${theme}`);
        const label = page.getByTestId('today-day-event-label').filter({ hasText: 'Jenny' });
        await expect(label).toHaveCount(1);
        await expect(label).toBeVisible();
        // Read actual composited pixels from the glyph area, away from rounded
        // corners. Sampling the ground at a corner would mask faded text.
        const png = await label.screenshot({ scale: 'css' });
        const contrast = await page.evaluate(async (base64) => {
            const picture = new Image();
            picture.src = `data:image/png;base64,${base64}`;
            await picture.decode();
            const canvas = document.createElement('canvas');
            canvas.width = picture.width;
            canvas.height = picture.height;
            const context = canvas.getContext('2d')!;
            context.drawImage(picture, 0, 0);
            const pixels = context.getImageData(0, 0, canvas.width, canvas.height).data;
            const linear = (value: number) => {
                const s = value / 255;
                return s <= 0.04045 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
            };
            let darkest = 1, lightest = 0;
            for (let y = 7; y < canvas.height - 7; y++) {
                for (let x = 4; x < canvas.width - 4; x++) {
                    const i = (y * canvas.width + x) * 4;
                    const luminance = 0.2126 * linear(pixels[i]) + 0.7152 * linear(pixels[i + 1]) + 0.0722 * linear(pixels[i + 2]);
                    darkest = Math.min(darkest, luminance);
                    lightest = Math.max(lightest, luminance);
                }
            }
            return (lightest + 0.05) / (darkest + 0.05);
        }, png.toString('base64'));
        console.log(`${theme} ended Jenny contrast: ${contrast.toFixed(3)}`);
        expect(contrast).toBeGreaterThanOrEqual(minimumContrast);
        const group = label.locator('..');
        await expect(group).toHaveCSS('opacity', '0.45');
        await expect(label).toHaveCSS('opacity', '1');
        await expect(group.getByTestId('today-day-event')).toHaveCSS('opacity', '1');
        await page.getByTestId('today-day-track').screenshot({ path: info.outputPath(`ended-contrast-${theme}.png`) });
    });
}
