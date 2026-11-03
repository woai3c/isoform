import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// Launch (fresh-install rehearsal, 2026-10-27: the window stayed blank for
// 2.8-3.6 s). The page paints its theme background before any script loads,
// and nothing on it waits on the network.
describe("index.html first paint", () => {
    const html = readFileSync(resolve(__dirname, "../../index.html"), "utf8");
    const css = readFileSync(resolve(__dirname, "../index.css"), "utf8");

    it("loads no remote stylesheet or font before the first paint", () => {
        expect(html).not.toMatch(/<link[^>]+rel="stylesheet"[^>]+https?:/);
        expect(html).not.toMatch(/fonts\.googleapis|fonts\.gstatic/);
    });

    it("paints the light and dark background tokens inline", () => {
        const light = css.match(/@theme\s*\{[\s\S]*?--color-background:\s*(#[0-9a-f]{6})/i)?.[1];
        const dark = css.match(/:root\[data-theme="onedark"\]\s*\{[\s\S]*?--color-background:\s*(#[0-9a-f]{6})/i)?.[1];
        expect(light).toBeTruthy();
        expect(dark).toBeTruthy();
        expect(html).toContain(`html, body { margin: 0; background: ${light}; }`);
        expect(html).toContain(`html[data-theme="onedark"] body { background: ${dark}; }`);
    });

    it("keeps the terminal panel (xterm) out of the launch bundle", () => {
        const app = readFileSync(resolve(__dirname, "../App.tsx"), "utf8");
        expect(app).not.toMatch(/from "@\/components\/terminal-panel"/);
        expect(app).toContain("LazyTerminalPanel");
    });
});
