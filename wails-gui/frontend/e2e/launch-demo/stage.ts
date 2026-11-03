// The desktop around the app for the recorder: a menu bar, the window frame
// and a scripted cursor. URL params: theme=onedark|paper.
import trayIcon from '@wails/assets/menubar-icon@3x.png';

const params = new URLSearchParams(location.search);
const theme = params.get('theme') === 'paper' ? 'paper' : 'onedark';
document.documentElement.dataset.theme = theme;
(document.getElementById('tray') as HTMLImageElement).src = trayIcon;
(document.getElementById('app') as HTMLIFrameElement).src = `/e2e/launch-demo/index.html?theme=${theme}`;

const clock = document.getElementById('clock')!;
const tick = () => {
    const now = new Date();
    const day = now.toLocaleDateString('en-US', { weekday: 'short', month: 'short', day: 'numeric' }).replace(',', '');
    clock.textContent = `${day}  ${now.toLocaleTimeString('en-US', { hour: 'numeric', minute: '2-digit' })}`;
};
tick();
setInterval(tick, 1000);

const cursor = document.getElementById('cursor')!;
let pos = { x: 1500, y: 1000 };
let scale = 1;
const draw = () => { cursor.style.transform = `translate(${pos.x - 3}px, ${pos.y - 3}px) scale(${scale})`; };
draw();
const ease = (t: number) => t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;

/** Moves the cursor along a gentle arc to (x, y) in `ms`. */
function moveTo(x: number, y: number, ms: number): Promise<void> {
    const from = { ...pos };
    const bend = Math.min(80, Math.hypot(x - from.x, y - from.y) * 0.12);
    const start = performance.now();
    return new Promise((resolve) => {
        const step = (t: number) => {
            const k = Math.min(1, (t - start) / ms);
            const e = ease(k);
            const arc = Math.sin(Math.PI * e) * bend;
            pos = { x: from.x + (x - from.x) * e + arc * 0.35, y: from.y + (y - from.y) * e - arc };
            draw();
            if (k < 1) requestAnimationFrame(step); else resolve();
        };
        requestAnimationFrame(step);
    });
}
/** The press: the arrow dips while the button is down. */
function press(down: boolean) { scale = down ? 0.86 : 1; draw(); }
function place(x: number, y: number) { pos = { x, y }; draw(); }

Object.assign(window, { stage: { moveTo, press, place } });
