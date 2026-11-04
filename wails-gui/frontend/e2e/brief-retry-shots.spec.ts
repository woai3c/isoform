import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

for (const [language, theme] of [['en', 'paper'], ['en', 'onedark'], ['zh-CN', 'paper'], ['zh-CN', 'onedark']]) {
    for (const state of ['retry', 'making']) {
        test(`brief ${state} ${language} ${theme}`, async ({ page }) => {
            await page.clock.setFixedTime(new Date('2026-10-23T10:00:00-07:00'));
            await page.setViewportSize({ width: 1440, height: 1000 });
            await page.goto(`/e2e/brief-retry.html?state=${state}&theme=${theme}&lang=${language}`);
            const card = page.getByTestId('brief-pending');
            await expect(card).toContainText(state === 'making' ? (language === 'en' ? 'Making your morning brief now.' : '正在生成你的早间简报。') : (language === 'en' ? 'Your AI is signed out.' : '你的 AI 已退出登录。'));
            const out = process.env.BRIEF_SHOTS ?? '/tmp/brief-codex-shots';
            mkdirSync(out, { recursive: true });
            await page.screenshot({ path: join(out, `${state}-${language}-${theme}.png`), fullPage: true });
        });
    }
}
