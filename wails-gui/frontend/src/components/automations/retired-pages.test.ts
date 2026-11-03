import { existsSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

describe("retired standalone pages", () => {
    it.each(["automations/UpcomingPage.tsx", "sops/SopsPage.tsx"])("does not retain %s", (page) => {
        expect(existsSync(resolve(__dirname, "..", page))).toBe(false);
    });
});
