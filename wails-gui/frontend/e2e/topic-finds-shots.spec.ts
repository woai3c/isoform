import { test, expect } from '@playwright/test';

// The skeleton and the saved row are the same height, so nothing below moves.
test('the skeleton and the saved row have the same height', async ({ page }) => {
    await page.goto('/e2e/topic-finds-shots.html?state=saved&theme=paper');
    const saved = await page.getByTestId('topic-finds-row').evaluate((el) => el.getBoundingClientRect().height);
    await page.goto('/e2e/topic-finds-shots.html?state=skeleton&theme=paper');
    const skeleton = await page.getByTestId('topic-finds-skeleton').evaluate((el) => el.getBoundingClientRect().height);
    expect(Math.abs(skeleton - saved), `skeleton ${skeleton}px, saved ${saved}px`).toBeLessThanOrEqual(0.5);
});

// Writes design's review frames when TOPIC_FINDS_SHOTS_DIR is set.
const dir = process.env.TOPIC_FINDS_SHOTS_DIR;
for (const [name, theme] of [['light', 'paper'], ['dark', 'onedark']] as const) {
    for (const which of ['skeleton', 'saved'] as const) {
        test(`writes ${which} ${name}`, async ({ page }) => {
            test.skip(!dir, 'TOPIC_FINDS_SHOTS_DIR not set');
            await page.goto(`/e2e/topic-finds-shots.html?state=${which}&theme=${theme}`);
            if (which === 'saved') await expect(page.getByTestId('topic-finds-row')).toBeVisible();
            await page.waitForTimeout(300);
            await page.screenshot({ path: `${dir}/topic-finds-r67-${which}-${name}-v2.png` });
        });
    }
}
