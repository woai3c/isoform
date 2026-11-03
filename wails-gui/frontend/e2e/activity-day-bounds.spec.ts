import { test, expect } from '@playwright/test';

for (const language of ['en', 'zh-CN']) {
    test(`760px clipped day labels and continuation in ${language}`, async ({ page }) => {
        await page.setViewportSize({ width: 760, height: 1000 });
        await page.goto('/e2e/activity-day-bounds.html?lang=' + language);
        await expect(page.getByTestId('stitched-continues-after')).toBeVisible();
        const axis = page.getByTestId('stitched-axis');
        await expect(axis.locator('span')).not.toHaveCount(0);
        // Real WebKit text geometry, including CJK glyphs and the right edge.
        const boxes = await axis.evaluate((node) => {
            const track = node.getBoundingClientRect();
            return [...node.children].map((label) => {
                const range = document.createRange();
                range.selectNodeContents(label);
                const text = range.getBoundingClientRect();
                return { left: text.left - track.left, right: text.right - track.left, width: track.width, text: label.textContent };
            });
        });
        expect(boxes.length).toBeGreaterThan(1);
        boxes.forEach((box, index) => {
            expect(box.left).toBeGreaterThanOrEqual(0);
            expect(box.right).toBeLessThanOrEqual(box.width);
            if (index) expect(box.left).toBeGreaterThan(boxes[index - 1].right);
        });
        await expect(page.getByText(language === 'en' ? 'In progress' : '进行中', { exact: true })).toHaveCount(0);
        if (language === 'en') {
            await expect(page.getByRole('heading', { name: '9:12 AM to 12:00 AM' })).toBeVisible();
            await expect(page.getByText(/8 moments in 15 hours/)).toBeVisible();
            await page.screenshot({ path: '../../docs/hub-reviews/assets/activity-day-bounds-en-760.png', fullPage: true });
        }
    });
}
