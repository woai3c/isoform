import { expect, test } from '@playwright/test';
import path from 'node:path';
for (const theme of ['paper', 'onedark']) for (const lang of ['en', 'zh-CN']) {
    test(`needs count ${lang} ${theme}`, async ({ page }) => {
        await page.goto(`/e2e/walk102-chat.html?theme=${theme}&lang=${lang}`);
        const count = page.getByTestId('main-chat-panel-waiting');
        await expect(count).toContainText(lang === 'en' ? '1 question for you' : '1');
        await page.screenshot({ path: path.join(process.env.CHAT_SHOTS_DIR!, `needs-${lang}-${theme}.png`) });
    });
}
