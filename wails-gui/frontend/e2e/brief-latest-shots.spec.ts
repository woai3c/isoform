import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

for (const [language, theme] of [['en', 'paper'], ['en', 'onedark'], ['zh-CN', 'paper'], ['zh-CN', 'onedark']]) {
    test(`latest brief ${language} ${theme}`, async ({ page }) => {
        await page.setViewportSize({ width: 1440, height: 1000 });
        await page.goto(`/e2e/brief-latest.html?theme=${theme}&lang=${language}`);
        await expect(page.getByRole('status')).toContainText(language === 'en' ? 'Nothing new since your 9:12 AM brief.' : '自你 9:12 的简报以来没有新内容。');
        await expect(page.getByTestId('brief-card')).toBeVisible();
        const out = process.env.BRIEF_SHOTS ?? '/tmp/brief-latest-codex';
        mkdirSync(out, { recursive: true });
        await page.screenshot({ path: join(out, `${language}-${theme}.png`), fullPage: true });
    });
}
