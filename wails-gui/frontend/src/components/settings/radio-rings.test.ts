import { describe, expect, it } from "vitest";
import { readFileSync } from "node:fs";
import { resolve } from "node:path";

const css = readFileSync(resolve(__dirname, "../../index.css"), "utf8");

// settings-review ST62: an unselected radio is a hollow ring in both themes;
// the chosen one is a ring with a dot.
describe("radio rings", () => {
    it("draws radios itself instead of the native solid dark-mode disc", () => {
        const base = css.slice(css.indexOf('input[type="radio"] {'));
        expect(base).toMatch(/appearance: none;[\s\S]*border-radius: 9999px;[\s\S]*background: transparent;/);
        expect(base).toMatch(/input\[type="radio"\]:checked \{[\s\S]*radial-gradient\(circle, var\(--color-accent-2\)/);
    });
});
