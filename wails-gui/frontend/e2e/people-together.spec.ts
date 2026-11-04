import { expect, test } from '@playwright/test';
import path from 'node:path';

for (const theme of ['paper', 'onedark']) {
    for (const lang of ['en', 'zh-CN']) {
        test(`Together ${lang} ${theme}`, async ({ page }) => {
            const errors: string[] = [];
            page.on('pageerror', (error) => { if (!error.message.includes('bridge-token')) errors.push(error.message); });
            await page.clock.setFixedTime(new Date('2026-11-04T18:00:00Z'));
            await page.setViewportSize({ width: 1280, height: 800 });
            await page.goto(`/e2e/people-together.html?theme=${theme}&lang=${lang}`);
            await expect(page.getByText(lang === 'en' ? 'You messaged Mira about the release timing · WeChat' : '你和王晓雨聊了发布时间 · 微信')).toBeVisible();
            const name = lang === 'en' ? '17-together' : '18-zh-together';
            await page.screenshot({ path: path.join(process.env.PEOPLE_SHOTS_DIR!, `${name}${theme === 'onedark' ? '-dark' : ''}.png`) });
            expect(errors).toEqual([]);
        });
    }
}
