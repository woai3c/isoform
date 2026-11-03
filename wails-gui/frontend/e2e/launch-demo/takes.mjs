// The scripted takes. Each beat is one cursor motion at a human pace: a
// pause, a slow travel, a short settle, the click.
const LENA = 'Prep me for my 2:00 with Lena';
const PHRASE = 'two sentences';

// DEMO_PACE stretches every pause and travel (1.15 for a calmer second take).
const PACE = Number(process.env.DEMO_PACE ?? 1);

export function director(page) {
    const app = page.frameLocator('#app');
    const center = async (target) => {
        const box = await target.boundingBox();
        if (!box) throw new Error(`not on screen: ${target}`);
        return { x: box.x + box.width / 2, y: box.y + box.height / 2 };
    };
    const moveTo = async (target, ms = 900) => {
        ms *= PACE;
        const { x, y } = 'x' in target ? target : await center(target);
        await page.evaluate(([x, y, ms]) => window.stage.moveTo(x, y, ms), [x, y, ms]);
        await page.mouse.move(x, y);
        return { x, y };
    };
    return {
        app,
        page,
        hold: (ms) => page.waitForTimeout(ms * PACE),
        nudge: async () => { await page.evaluate(() => window.stage.place(1700, 990)); await page.evaluate(() => window.stage.place(1701, 990)); },
        moveTo,
        /** Pause, travel, settle, press. */
        click: async (target, { before = 1000, travel = 900, settle = 250 } = {}) => {
            await page.waitForTimeout(before * PACE);
            const { x, y } = await moveTo(target, travel);
            await page.waitForTimeout(settle);
            await page.evaluate(() => window.stage.press(true));
            await page.mouse.down();
            await page.waitForTimeout(90);
            await page.mouse.up();
            await page.evaluate(() => window.stage.press(false));
            return { x, y };
        },
        hover: async (target, { before = 1000, travel = 900 } = {}) => {
            await page.waitForTimeout(before * PACE);
            return moveTo(target, travel);
        },
        /** Slow wheel steps until `target`'s top reaches `y`, for at most `ms`. */
        scrollUntil: async (target, y, ms) => {
            const until = Date.now() + ms * PACE * 1.5;
            while (Date.now() < until && (await target.boundingBox()).y > y) {
                await page.mouse.wheel(0, 14);
                await page.waitForTimeout(16);
            }
        },
        /** A slow wheel scroll over a point, `dy` pixels in `ms`. */
        scroll: async (dy, ms) => {
            const steps = Math.round(ms / 16);
            for (let i = 0; i < steps; i++) {
                await page.mouse.wheel(0, dy / steps);
                await page.waitForTimeout(16);
            }
        },
    };
}

const todayReady = async (d) => {
    await d.app.getByRole('button', { name: LENA }).waitFor();
    await d.app.locator('[data-card-review]').waitFor();
    await d.page.evaluate(() => window.stage.place(1700, 990));
};

const portraitCard = (app) => app.locator('section[aria-labelledby="about-you-portrait"]');

/** Your portrait: the card, the beliefs behind "two sentences", then How to work with me. */
const portraitBeat = async (d, { cardHold, endHold }) => {
    const { app } = d;
    await d.hold(cardHold);
    const phrase = app.getByRole('button', { name: PHRASE, exact: true });
    await d.click(phrase, { before: 150, travel: 700 });
    const row = app.getByRole('dialog').getByText(/Priya reads on her phone between calls/);
    await row.waitFor();
    await d.hover(row, { before: 300, travel: 600 });
    await d.hold(2100);
    await d.click(phrase, { before: 100, travel: 500, settle: 150 });
    await row.waitFor({ state: 'hidden' });
    // Down to How to work with me, then hold on it.
    const heading = app.getByRole('heading', { name: 'How to work with me' });
    await d.moveTo({ x: 1500, y: 700 }, 450);
    await d.scrollUntil(heading, 320, 1000);
    await d.hold(endHold);
};

const openAboutYou = async (d, travel = 1000) => {
    await d.click(d.app.getByRole('button', { name: 'About you' }), { before: 200, travel });
    await portraitCard(d.app).getByText(/You plan the whole thing/).waitFor();
};

export const takes = {
    hero: {
        ready: todayReady,
        run: async (d) => {
            const { app } = d;
            const t0 = Date.now();
            await d.hold(1800);
            // Lena: the chip fills the chat box; Send asks; the prep answers.
            await d.click(app.getByRole('button', { name: LENA }), { before: 600 });
            await d.click(app.getByRole('button', { name: 'Send', exact: true }), { before: 500, travel: 800 });
            await app.getByText('Your 2:00 with Lena').waitFor();
            await d.moveTo({ x: 1640, y: 640 }, 900);
            await d.hold(1500);
            // Priya: the waiting card opens with the draft; Send sends it.
            await d.click(app.locator('[data-card-review]'), { before: 200 });
            await app.getByText('Hi Priya,').waitFor();
            await d.hold(1400);
            await d.click(app.getByRole('button', { name: /Send to Priya/ }), { before: 400 });
            await d.hold(1000);
            await d.click(app.getByRole('button', { name: 'Close' }).first(), { before: 300, travel: 800 });
            await app.getByText(/Sent via Mail/).waitFor();
            await d.hold(1000);
            // Your portrait, fully written: open About you and hold, no interaction.
            await openAboutYou(d, 900);
            await d.moveTo({ x: 1700, y: 990 }, 1000);
            await d.page.waitForTimeout(Math.max(5000, 24000 * PACE - (Date.now() - t0)));
        },
    },
    explainer: {
        ready: todayReady,
        run: async (d) => {
            const { app } = d;
            // The brief, written.
            await d.moveTo(app.getByText(/Halcyon is close/), 900);
            await d.hold(1300);
            // Priya's waiting reply: the draft, then Send.
            await d.click(app.locator('[data-card-review]'), { before: 200 });
            await app.getByText('Hi Priya,').waitFor();
            await d.hold(1200);
            await d.click(app.getByRole('button', { name: /Send to Priya/ }), { before: 400 });
            await d.hold(400);
            await d.click(app.getByRole('button', { name: 'Close' }).first(), { before: 200, travel: 700 });
            await app.getByText(/Sent via Mail/).waitFor();
            await d.hold(400);
            // Your portrait, and the beliefs behind one phrase.
            await openAboutYou(d, 800);
            await d.moveTo({ x: 1640, y: 560 }, 500);
            await portraitBeat(d, { cardHold: 2000, endHold: 3000 });
            // Settings: what it sees, and Pause.
            await d.click(app.getByRole('button', { name: /Bo/ }).last(), { before: 200, travel: 700 });
            await d.click(app.getByRole('button', { name: /^Settings/ }), { before: 300, travel: 400 });
            await app.getByText('How Isoform works for you').waitFor();
            await d.hover(app.getByRole('button', { name: 'Pause for 1 hour' }), { before: 400 });
            await d.hold(1200);
            await d.click(app.getByRole('button', { name: 'Close' }).last(), { before: 200, travel: 500 });
            await d.click(app.getByRole('button', { name: 'Today' }), { before: 300, travel: 700 });
            await d.hold(1600);
        },
    },
    // The portrait beat alone, starting on the open card.
    portrait: {
        ready: async (d) => {
            await todayReady(d);
            await d.app.getByRole('button', { name: 'About you' }).click();
            await portraitCard(d.app).getByText(/You plan the whole thing/).waitFor();
            await d.page.evaluate(() => window.stage.place(1640, 560));
        },
        run: async (d) => {
            await d.moveTo({ x: 1641, y: 560 }, 50);
            await portraitBeat(d, { cardHold: 1000, endHold: 2500 });
        },
    },
    // The LinkedIn still: Today with the brief, no cursor.
    still: {
        ready: async (d) => { await todayReady(d); await d.page.evaluate(() => window.stage.place(-100, -100)); },
        run: async () => {},
    },
};
