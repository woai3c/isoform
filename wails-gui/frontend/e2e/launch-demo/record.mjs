// Records a launch demo take off-screen: headless Chromium renders the stage
// (desktop, menu bar, the real app in its window, a scripted cursor), a CDP
// screencast captures it in real time and ffmpeg writes an H.264 .mov.
//
// Usage (from wails-gui/frontend):
//   CHROME=<chrome-headless-shell binary> node e2e/launch-demo/record.mjs <take> <onedark|paper> <out.mov|out.png>
// Takes are in takes.mjs. A .png output saves one still of the take's first
// frame instead of a video. Never touches the Isoform app or its data.
// Use Playwright's chrome-headless-shell: it holds 60 fps at 1080p, while
// Chrome for Testing's new headless mode drops to about 24.
import { chromium } from '@playwright/test';
import { spawn, execFileSync } from 'node:child_process';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { takes, director } from './takes.mjs';

const [takeName, theme = 'onedark', out] = process.argv.slice(2);
const take = takes[takeName];
if (!take || !out) {
    console.error(`usage: record.mjs <${Object.keys(takes).join('|')}> <onedark|paper> <out.mov|out.png>`);
    process.exit(2);
}
const frontend = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const port = Number(process.env.DEMO_PORT ?? 34191);
const W = 1920, H = 1080;
// 1920x1080 points at 2x (3840x2160 pixels) by default; DEMO_SCALE=1 for 1080p.
const SCALE = Number(process.env.DEMO_SCALE ?? 2);

// Our own Vite server for the harness; stopped when the take ends.
const vite = spawn(path.join(frontend, 'node_modules/.bin/vite'), ['--host', '127.0.0.1', '--port', String(port), '--strictPort'], { cwd: frontend, stdio: 'ignore' });
const stopVite = () => { try { vite.kill('SIGTERM'); } catch { /* already gone */ } };
process.on('exit', stopVite);
const base = `http://127.0.0.1:${port}/e2e/launch-demo/stage.html`;
for (let i = 0; ; i++) {
    try { if ((await fetch(base)).ok) break; } catch { /* starting */ }
    if (i > 120) throw new Error('vite did not start');
    await new Promise((r) => setTimeout(r, 250));
}

const browser = await chromium.launch({
    executablePath: process.env.CHROME || undefined,
    headless: true,
    // The screencast sends CSS-size frames unless the browser itself runs at the scale.
    args: [`--force-device-scale-factor=${SCALE}`, ...(process.env.DEMO_GPU ? ['--use-angle=metal', '--enable-gpu-rasterization', '--ignore-gpu-blocklist', '--enable-zero-copy'] : []), '--force-color-profile=srgb', '--disable-renderer-backgrounding', '--hide-scrollbars', '--font-render-hinting=none'],
});
const ctx = await browser.newContext({ viewport: { width: W, height: H }, deviceScaleFactor: SCALE, colorScheme: theme === 'paper' ? 'light' : 'dark', timezoneId: 'America/Los_Angeles', locale: 'en-US' });
// The demo morning; time runs on from here.
await ctx.clock.install({ time: new Date(process.env.DEMO_TIME ?? '2026-11-03T07:02:00-08:00') });
await ctx.clock.resume();
const page = await ctx.newPage();
page.on('pageerror', (e) => console.error('[pageerror]', e.message));
await page.goto(`${base}?theme=${theme}`);
const d = director(page);
await take.ready(d);
await page.evaluate(() => document.fonts.ready);
await page.waitForTimeout(1200);

if (out.endsWith('.png')) {
    await page.screenshot({ path: out });
    console.log(JSON.stringify({ still: out }));
    await browser.close();
    stopVite();
    process.exit(0);
}

const work = fs.mkdtempSync(path.join(os.tmpdir(), 'launch-demo-'));
const cdp = await ctx.newCDPSession(page);
const frames = [];
cdp.on('Page.screencastFrame', async (f) => {
    const file = path.join(work, String(frames.length).padStart(6, '0') + '.jpg');
    fs.writeFileSync(file, Buffer.from(f.data, 'base64'));
    frames.push({ file, t: f.metadata.timestamp });
    try { await cdp.send('Page.screencastFrameAck', { sessionId: f.sessionId }); } catch { /* closed */ }
});
await cdp.send('Page.startScreencast', { format: 'jpeg', quality: Number(process.env.DEMO_JPEG ?? 95), everyNthFrame: 1, maxWidth: W * SCALE, maxHeight: H * SCALE });
// A tiny cursor wiggle forces the first frame out before the take begins.
await d.nudge();
await page.waitForTimeout(300);
const start = Date.now() / 1000;
await take.run(d);
const end = Date.now() / 1000;
await cdp.send('Page.stopScreencast');
await browser.close();
stopVite();

// Each frame lasts until the next; the clip starts when the take did.
const kept = frames.filter((f, i) => f.t >= start - 0.001 || (frames[i + 1]?.t ?? Infinity) > start);
let list = '';
for (let i = 0; i < kept.length; i++) {
    const from = Math.max(kept[i].t, start);
    const to = i + 1 < kept.length ? kept[i + 1].t : end;
    list += `file '${kept[i].file}'\nduration ${Math.max(to - from, 0.001).toFixed(4)}\n`;
}
list += `file '${kept[kept.length - 1].file}'\n`;
fs.writeFileSync(path.join(work, 'list.txt'), list);
execFileSync('ffmpeg', ['-y', '-loglevel', 'error', '-f', 'concat', '-safe', '0', '-i', path.join(work, 'list.txt'),
    '-vf', 'fps=60,format=yuv420p', '-c:v', 'libx264', '-preset', 'slow', '-crf', '14', '-tag:v', 'avc1', '-movflags', '+faststart', '-an', out]);
fs.rmSync(work, { recursive: true, force: true });
const span = kept[kept.length - 1].t - kept[0].t;
// Frame rate while something moves: gaps under 100 ms (holds send no frames).
const gaps = kept.slice(1).map((f, i) => f.t - kept[i].t).filter((g) => g < 0.1).sort((a, b) => a - b);
const motionFps = gaps.length ? +(1 / gaps[Math.floor(gaps.length / 2)]).toFixed(1) : 0;
const slowGaps = gaps.filter((g) => g > 1 / 40).length;
const p95 = gaps.length ? Math.round(1000 * gaps[Math.floor(gaps.length * 0.95)]) : 0;
console.log(JSON.stringify({ out, seconds: +(end - start).toFixed(2), frames: kept.length, avgFps: +(kept.length / Math.max(span, 0.001)).toFixed(1), motionFps, slowGaps, p95GapMs: p95 }));
