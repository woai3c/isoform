// node e2e/about-you-thu-shots.mjs <outDir> [only]; THEME=onedark renders dark-<name>
import { chromium } from '@playwright/test';
const [out, only] = process.argv.slice(2);
const base = 'http://127.0.0.1:5181/e2e/about-you-thu.html';
const menu = async (page, item) => {
    await page.getByRole('button', { name: /More About you actions|更多/ }).first().click();
    if (item) await page.getByRole('menuitem', { name: item }).click();
};
const addGoal = async (page, text = 'Finish the Ridge Loop under two hours', zh = false) => {
    await menu(page, zh ? '添加目标' : 'Add a goal');
    await page.getByPlaceholder(zh ? '例如：三月跑一个半马' : 'For example: Run a half marathon in March').fill(text);
};
const shots = [
    ['live-stored-belief', 'state=live', (p) => p.getByText(/You hand work to other agents and coordinate them/).first().scrollIntoViewIfNeeded()],
    ['live-evidence', 'state=live&select=mornings'],
    ['live-zh-portrait', 'state=live&lang=zh-CN'],
    // v1.0.2: Y28 every verb agrees, Y34 waiting portrait counts nothing, Y35 source in words, Y36 one date per row.
    ['41-v102-beliefs-every-verb-agrees', 'state=v102', (p) => p.getByText(/and decline meetings then/).first().scrollIntoViewIfNeeded()],
    ['42-zh-v102-beliefs-every-verb-agrees', 'state=v102&lang=zh-CN', (p) => p.getByText(/and decline meetings then/).first().scrollIntoViewIfNeeded()],
    ['43-v102-waiting-portrait-counts-nothing', 'state=learning'],
    ['44-zh-v102-waiting-portrait-counts-nothing', 'state=learning&lang=zh-CN'],
    ['45-v102-day-one-no-counts', 'state=dayone'],
    ['46-zh-v102-day-one-no-counts', 'state=dayone&lang=zh-CN'],
    ['47-v102-detail-source-in-words', 'state=v102&select=mornings'],
    ['48-zh-v102-detail-source-in-words', 'state=v102&select=mornings&lang=zh-CN'],
    ['49-v102-detail-no-source-no-heading', 'state=v102&select=replies'],
    ['50-zh-v102-detail-no-source-no-heading', 'state=v102&select=replies&lang=zh-CN'],
    ['51-v102-row-one-date', 'state=v102', (p) => p.getByText(/You write short replies/).first().scrollIntoViewIfNeeded()],
    ['52-zh-v102-row-one-date', 'state=v102&lang=zh-CN', (p) => p.getByText(/You write short replies/).first().scrollIntoViewIfNeeded()],
    ['01-empty-page', 'state=empty'],
    ['26-v101-beliefs-second-person', 'state=v101', (p) => p.getByText(/You guard mornings for deep work/).first().scrollIntoViewIfNeeded()],
    ['27-v101-work-with-me-real-source', 'state=v101', (p) => p.getByText('How to work with me').scrollIntoViewIfNeeded()],
    ['28-v101-goal-short-name', 'state=v101', (p) => p.getByText('AFO calf raises, every day').scrollIntoViewIfNeeded()],
    ['29-v101-portrait-updated-today', 'state=v101', (p) => p.getByText('Updated today').scrollIntoViewIfNeeded()],
    ['30-v101-new-this-week-learned-today-once', 'state=v101', (p) => p.locator('article').first().scrollIntoViewIfNeeded()],
    ['31-zh-v101-portrait-and-sources', 'state=v101&lang=zh-CN', (p) => p.getByText('怎么和我共事').first().scrollIntoViewIfNeeded()],
    ['02-zh-empty-page', 'state=empty&lang=zh-CN'],
    ['32-zh-day-one-check-these-first', 'state=dayone&lang=zh-CN'],
    ['33-zh-portrait-couldnt-open', 'state=page&fail=GetAboutYouPortrait&lang=zh-CN'],
    ['34-zh-portrait-couldnt-update', 'state=couldntUpdate&lang=zh-CN'],
    ['39-portrait-couldnt-use-answer', 'state=couldntUse'],
    ['40-zh-portrait-couldnt-use-answer', 'state=couldntUse&lang=zh-CN'],
    ['35-zh-phrase-not-quite-review-field', 'state=page&lang=zh-CN', async (p) => {
        await p.getByRole('button', { name: 'You guard your time before noon' }).click();
        await p.getByRole('dialog').getByRole('button', { name: '不太对' }).click();
    }],
    ['36-zh-thats-me-didnt-save', 'state=page&fail=ConfirmAboutYouPortrait&lang=zh-CN', (p) => p.getByRole('button', { name: '说得对' }).first().click()],
    ['37-zh-more-menu-without-developer-tools', 'state=page&lang=zh-CN', (p) => menu(p)],
    ['38-zh-belief-detail', 'state=page&select=mornings&lang=zh-CN'],
    ['03-day-one-check-these-first', 'state=dayone'],
    ['04-more-menu-without-developer-tools', 'state=page', (p) => menu(p)],
    ['05-add-a-goal-in-place', 'state=page', (p) => addGoal(p)],
    ['06-goal-added', 'state=page', async (p) => { await addGoal(p); await p.getByRole('button', { name: 'Add', exact: true }).click(); }],
    ['07-goal-didnt-save', 'state=page&fail=CreateGoal', async (p) => { await addGoal(p); await p.getByRole('button', { name: 'Add', exact: true }).click(); }],
    ['08-phrase-not-quite-review-field', 'state=page', async (p) => {
        await p.getByRole('button', { name: 'You guard your time before noon' }).click();
        await p.getByRole('dialog').getByRole('button', { name: 'Not quite' }).click();
    }],
    ['09-thats-me-didnt-save', 'state=page&fail=ConfirmAboutYouPortrait', (p) => p.getByRole('button', { name: "That's me" }).first().click()],
    ['10-copy-didnt-copy', 'state=page&fail=NativeClipboardSetText,ClipboardSetText', (p) => p.getByRole('button', { name: 'Copy', exact: true }).click()],
    ['11-portrait-couldnt-open', 'state=page&fail=GetAboutYouPortrait'],
    ['12-portrait-couldnt-update', 'state=couldntUpdate'],
    ['13-zh-portrait-still-learning', 'state=learning&lang=zh-CN'],
    ['14-rewriting-with-your-note', 'state=rewriting'],
    ['15-belief-detail', 'state=page&select=mornings'],
    ['16-forget-dialog', 'state=page&select=mornings', (p) => p.getByRole('button', { name: 'Forget this' }).click()],
    ['17-zh-forget-dialog', 'state=page&select=mornings&lang=zh-CN', (p) => p.getByRole('button', { name: '忘记这条' }).click()],
    ['18-wrong-subject', 'state=page&select=mornings', async (p) => {
        await p.getByRole('button', { name: 'More memory actions' }).click();
        await p.getByRole('menuitem', { name: 'Wrong subject' }).click();
    }],
    ['19-add-a-note-about-you', 'state=page', (p) => menu(p, 'Add a note about you')],
    ['20-import-couldnt-look', 'state=page&fail=ScanAIMemories', (p) => menu(p, 'Import from AI tools')],
    ['21-import-none-found', 'state=page', (p) => menu(p, 'Import from AI tools')],
    ['22-search-nothing-matches', 'state=page', (p) => p.getByPlaceholder('Search what Isoform believes').fill('sailing')],
    ['23-translate-some-couldnt', 'state=page&fail=StartTranslateAllCurrentMemoryEntries', async (p) => {
        await menu(p, 'Translate memories');
        await p.getByRole('dialog').getByRole('button', { name: 'Translate', exact: true }).click();
    }],
    ['24-translate-done', 'state=page', async (p) => {
        await menu(p, 'Translate memories');
        await p.getByRole('dialog').getByRole('button', { name: 'Translate', exact: true }).click();
        await p.waitForTimeout(300);
        await p.evaluate(() => window.emit('global-translation:done', { scope: 'memory', summary: { translated: 3, failed: 0, total: 3, targetLanguage: 'English' } }));
    }],
    ['25-zh-goal-didnt-save', 'state=page&fail=CreateGoal&lang=zh-CN', async (p) => {
        await addGoal(p, '十月底前跑完山脊环线', true);
        await p.getByRole('button', { name: '添加', exact: true }).click();
    }],
];
const browser = await chromium.connectOverCDP(process.env.CDP ?? 'http://localhost:29229');
const context = await browser.newContext({ viewport: { width: 1280, height: 800 }, deviceScaleFactor: 2, reducedMotion: 'reduce', timezoneId: 'America/Los_Angeles' });
for (const [name, query, act] of shots) {
    if (only && !name.includes(only)) continue;
    const page = await context.newPage();
    const errors = [];
    page.on('pageerror', (e) => errors.push(e.message));
    try {
        await page.goto(`${base}?${query}${process.env.THEME ? `&theme=${process.env.THEME}` : ''}`);
        await page.waitForTimeout(1200);
        if (act) await act(page);
        await page.waitForTimeout(700);
        await page.screenshot({ path: `${out}/${process.env.THEME === 'onedark' ? 'dark-' : ''}${name}.png` });
        console.log('ok', name, errors.length ? errors.slice(0, 2).join(' | ') : '');
    } catch (e) {
        console.log('FAIL', name, String(e.message).split('\n')[0], errors.slice(0, 2).join(' | '));
    }
    await page.close();
}
await context.close();
process.exit(0);
