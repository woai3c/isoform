import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

// The Quick Chat Spotlight bundles main-UI components through
// quick-chat-spotlight/frontend/src/lib/wails-app-stub.ts, which must export
// every binding the main frontend imports from wailsjs/go/main/App. A missing
// export breaks the spotlight build, and build-wails.sh only warns and ships
// no helper, so the gap is caught here instead.
describe("spotlight wails-app-stub covers the main shim", () => {
    it("exports every appBinding the shim declares", () => {
        const here = resolve(__dirname);
        const shim = readFileSync(resolve(here, "wails-shim-app.ts"), "utf8");
        const stub = readFileSync(
            resolve(here, "../../../quick-chat-spotlight/frontend/src/lib/wails-app-stub.ts"),
            "utf8",
        );
        const bindings = [...shim.matchAll(/^export const (\w+) = appBinding/gm)].map((m) => m[1]);
        const stubbed = new Set(
            [...stub.matchAll(/^export (?:const|async function|function) (\w+)/gm)].map((m) => m[1]),
        );
        const missing = bindings.filter((name) => !stubbed.has(name));
        expect(missing, "add `export const X = makeNoop(\"X\");` to wails-app-stub.ts").toEqual([]);
    });
});
