import { test, expect } from '@playwright/test';
import { mkdirSync } from 'node:fs';
import { join } from 'node:path';

for (const theme of ['paper', 'onedark']) for (const language of ['en', 'zh-CN']) for (const state of ['retry', 'building']) {
    test(`${state} ${language} ${theme}`, async ({ page }) => {
        await page.clock.setFixedTime(new Date('2026-10-23T08:30:00-07:00'));
        await page.goto(`/e2e/today-one-state.html?theme=${theme}&lang=${language}&state=${state}`);
        const line = language === 'en' ? state === 'retry' ? 'Tried 6 times. It timed out. Isoform tries again at 8:45 AM.' : 'Making your morning brief now.' : state === 'retry' ? '试了 6 次。超时了。Isoform 会在8:45再试。' : '正在生成你的早间简报。';
        await expect(page.getByTestId('brief-pending')).toContainText(line);
        const out = process.env.TODAY_ONE_STATE_SHOTS || '/tmp/today-one-state-shots';
        mkdirSync(out, { recursive: true });
        await page.screenshot({ path: join(out, `${state}-${language}-${theme}.png`) });
    });
}
