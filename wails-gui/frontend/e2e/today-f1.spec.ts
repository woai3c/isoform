import { test, expect } from '@playwright/test';

for (const theme of ['paper', 'onedark']) {
    for (const language of ['en', 'zh-CN']) {
        test(`Today frame in ${language}/${theme}`, async ({ page }, info) => {
            await page.clock.setFixedTime(new Date('2026-10-23T10:00:00-07:00'));
            await page.setViewportSize({ width: 1440, height: 900 });
            await page.goto(`/e2e/polish-structure.html?today-next&sidebar=56&theme=${theme}&lang=${language}`);
            const status = page.getByTestId('today-status');
            await expect(status).not.toContainText(/Last brief|7 days|过去 7 天/);
            await expect(page.getByTestId('today-day-caption')).toHaveCount(0);
            const strip = page.getByTestId('today-day-track');
            const marker = page.getByTestId('today-day-now');
            await expect(marker).toHaveCSS('z-index', '0');
            const labels = strip.getByTestId('today-day-free');
            for (const label of await labels.all()) {
                const box = await label.boundingBox();
                const text = label.locator('span');
                if (box && await text.count()) expect((await text.boundingBox())!.width).toBeLessThanOrEqual(box.width);
            }
            await page.screenshot({ path: info.outputPath(`today-f1-${language}-${theme}.png`) });
        });
    }
}
