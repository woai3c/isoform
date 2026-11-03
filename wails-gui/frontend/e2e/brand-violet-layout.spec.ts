// Production components import the complete authored index.css through Vite.
// These computed layout checks protect functional rules during token changes.
import { test, expect } from '@playwright/test';

for (const theme of ['paper', 'onedark']) {
    for (const width of [450, 699, 700, 800]) {
        test(`Today uses equal topic columns in a ${width}px reading column in ${theme}`, async ({ page }) => {
            // A wide viewport with a narrow column also catches viewport queries
            // accidentally replacing the named page container.
            await page.setViewportSize({ width: 1440, height: 1000 });
            await page.goto(`/e2e/polish-structure.html?page=topics&theme=${theme}`);
            const content = page.locator('.today-page-content');
            await content.evaluate((node, pixels) => {
                (node as HTMLElement).style.flex = `0 0 ${pixels}px`;
            }, width);
            await expect(content).toHaveCSS('width', `${width}px`);
            const cards = page.getByTestId('today-topic-card');
            await expect(cards).toHaveCount(6);
            const columns = width < 700 ? 2 : 3;
            const grid = page.locator('.today-topics-grid');
            const tracks = await grid.evaluate((node) => getComputedStyle(node).gridTemplateColumns.split(' ').map(parseFloat));
            expect(tracks).toHaveLength(columns);
            expect(Math.max(...tracks) - Math.min(...tracks)).toBeLessThan(0.1);
            const boxes = await cards.evaluateAll((nodes) => nodes.map((node) => node.getBoundingClientRect().toJSON()));
            expect(boxes.filter((box) => box.y === boxes[0].y)).toHaveLength(columns);
            expect(boxes[columns].y).toBeGreaterThan(boxes[0].y);
            await expect(content).toHaveCSS('container-type', 'inline-size');
            await expect(content).toHaveCSS('container-name', 'today-page');
        });
    }

    test(`phone day strip compacts only inside its reading column in ${theme}`, async ({ page }) => {
        await page.setViewportSize({ width: 1440, height: 1000 });
        await page.goto(`/e2e/polish-structure.html?today-next&overlap&sidebar=56&theme=${theme}`);
        const content = page.locator('.today-page-content');
        const track = page.getByTestId('today-day-track');
        const event = track.locator('[data-testid="today-day-event"][data-inset="false"]').first();
        for (const width of [478, 477, 450, 800]) {
            await content.evaluate((node, pixels) => {
                (node as HTMLElement).style.flex = `0 0 ${pixels}px`;
            }, width);
            const compact = width <= 477;
            await expect(track).toHaveCSS('height', compact ? '32px' : '40px');
            await expect(page.locator('.today-day-hour-secondary').first()).toHaveCSS('display', compact ? 'none' : 'block');
            await expect(page.getByTestId('today-day-caption')).toHaveCSS('display', compact ? 'none' : 'flex');
            await expect(track.locator('[data-testid="today-day-event"][data-inset="false"]').first()).toHaveCSS('height', compact ? '24px' : '28px');
            await expect(page.getByTestId('today-day-hold')).toHaveCSS('height', compact ? '24px' : '28px');
            await expect(track.locator('[data-inset="true"]').first()).toHaveCSS('height', compact ? '16px' : '20px');
            await expect(page.locator('.today-day-block-label').first()).toHaveCSS('height', compact ? '24px' : '28px');
            await expect(event).toHaveCSS('margin-top', compact ? '-2px' : '0px');
            await expect(track).toHaveCSS('background-image', 'none');
        }
    });

    test(`composer establishes a named inline-size container in ${theme}`, async ({ page }) => {
        await page.setViewportSize({ width: 1440, height: 1000 });
        await page.goto(`/e2e/polish-structure.html?page=chat&column=420&theme=${theme}`);
        const surface = page.locator('.composer-surface');
        await expect(surface).toHaveCSS('container-type', 'inline-size');
        await expect(surface).toHaveCSS('container-name', 'composer');
        // Exercise the named container, including a resize, rather than only
        // asserting declarations. The probe adds no layout styling to the app.
        await surface.evaluate((node) => {
            const probe = document.createElement('span');
            probe.dataset.layoutProbe = 'composer';
            node.appendChild(probe);
        });
        await page.addStyleTag({ content: '[data-layout-probe="composer"] { --layout-size: wide; } @container composer (max-width: 477px) { [data-layout-probe="composer"] { --layout-size: narrow; } }' });
        const size = () => surface.locator('[data-layout-probe="composer"]').evaluate((node) => getComputedStyle(node).getPropertyValue('--layout-size').trim());
        await expect.poll(size).toBe('narrow');
        await page.getByTestId('narrow-chat-column').evaluate((node) => { (node as HTMLElement).style.width = '800px'; });
        await expect.poll(size).toBe('wide');
    });
}
