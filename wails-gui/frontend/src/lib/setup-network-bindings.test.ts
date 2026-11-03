import { readFileSync } from "node:fs";
import { resolve } from "node:path";
import { afterEach, expect, it, vi } from "vitest";
import { GetModelNetworkState, ResumeModelDownload } from "./wails-shim-app";

vi.mock("./browser-bridge", () => ({ callAppMethod: vi.fn() }));
const originalGo = (window as any).go;
afterEach(() => {
    (window as any).go = originalGo;
});

it("explicitly exports and dispatches network and resume bindings through explicit shim bindings", async () => {
    const network = {
        known: true,
        offline: true,
        expensive: false,
        constrained: false,
    };
    const read = vi.fn().mockResolvedValue(network);
    const resume = vi.fn().mockResolvedValue(undefined);
    (window as any).go = {
        main: {
            App: { GetModelNetworkState: read, ResumeModelDownload: resume },
        },
    };
    await expect(GetModelNetworkState()).resolves.toEqual(network);
    await ResumeModelDownload("sensevoice");
    expect(read).toHaveBeenCalledWith();
    expect(read).toHaveBeenCalledOnce();
    expect(resume).toHaveBeenCalledOnce();
    expect(resume).toHaveBeenCalledWith("sensevoice");
});

it("keeps explicit network and resume exports for rule 14 with stale generated bindings", () => {
    const source = readFileSync(
        resolve(__dirname, "wails-shim-app.ts"),
        "utf8",
    );
    for (const name of ["GetModelNetworkState", "ResumeModelDownload"]) {
        expect(source).toMatch(
            new RegExp(
                `export const ${name} = appBinding<[^;]+\\('${name}'\\);`,
            ),
        );
    }
});
