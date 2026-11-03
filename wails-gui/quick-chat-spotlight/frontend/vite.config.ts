import path from "path";
import tailwindcss from "@tailwindcss/vite";
import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";

// Spotlight reuses the chat-rendering surface from the main Yansu
// frontend (../../frontend/src/components/...). The two projects ship
// as separate Wails binaries with different bound Go methods, so any
// `wailsjs/...` import inside the reused components has to be
// redirected to spotlight's own bindings (or to no-op stubs for
// methods the spotlight binary doesn't expose). This plugin runs
// before Vite's default resolver and rewrites those imports.
function spotlightWailsRedirectPlugin(): Plugin {
    // Hand-written shims that delegate to native bridge (macOS) or the
    // Wails-injected window.runtime / window.go globals (Windows/Linux).
    // Neither imports from generated wailsjs/, so the macOS native build
    // (no `wails build`) needs no generated bindings at all.
    const spotlightRuntimeShim = path.resolve(__dirname, "./src/lib/spotlight-runtime.ts");
    const wailsAppStub = path.resolve(__dirname, "./src/lib/wails-app-stub.ts");

    // The shims must never be redirected into themselves (would create a
    // resolve loop). They don't import the redirected sources today, but
    // guard defensively.
    const skipImporters = new Set([
        wailsAppStub.replace(/\\/g, "/"),
        spotlightRuntimeShim.replace(/\\/g, "/"),
    ]);

    function isSelfImport(importer: string): boolean {
        const norm = importer.split("?")[0]!.split("#")[0]!.replace(/\\/g, "/");
        return skipImporters.has(norm);
    }

    return {
        name: "spotlight-wails-redirect",
        enforce: "pre",
        resolveId(source, importer) {
            if (!importer) return null;
            if (isSelfImport(importer)) return null;
            // Anything importing `wailsjs/runtime/runtime` gets the
            // spotlight runtime shim (native CustomEvents on macOS,
            // window.runtime delegation on Windows).
            if (source.endsWith("wailsjs/runtime/runtime")) {
                return spotlightRuntimeShim;
            }
            // Both the spotlight's lowercase `app` (its Go struct is
            // `type app`) and the main UI's capital `App` resolve to the
            // stub, which provides the real spotlight methods (native
            // bridge / window.go) plus no-ops for the ~200 main-UI methods
            // the spotlight doesn't expose.
            if (/wailsjs\/go\/main\/[Aa]pp$/.test(source)) {
                return wailsAppStub;
            }
            // Reused main-UI modules sometimes import the shim
            // (lib/wails-shim-app) directly instead of through
            // `wailsjs/go/main/App`. The main project Vite-aliases
            // App -> shim, and the shim ends with
            // `export * from "@wails-generated-app"` — an alias only the
            // main project defines. Redirect the shim to the same stub so
            // the spotlight build resolves it. (e.g. lib/tts-player, pulled
            // in via the reused stream-card chat component.)
            if (/(^|\/)wails-shim-app(\.[tj]sx?)?$/.test(source)) {
                return wailsAppStub;
            }
            return null;
        },
    };
}

export default defineConfig({
    plugins: [react(), tailwindcss(), spotlightWailsRedirectPlugin()],
    clearScreen: false,
    server: {
        port: 5181,
        strictPort: true,
        host: "127.0.0.1",
    },
    envPrefix: ["VITE_"],
    build: {
        target: "esnext",
    },
    resolve: {
        alias: {
            // `@/...` matches the main UI's import convention so its
            // chat components import-resolve identically when reused
            // from this project. Points at the main UI's src/ root.
            "@": path.resolve(__dirname, "../../frontend/src"),
            // `@wails/...` mirrors the main UI's alias for the wails-gui
            // root (so `@wails/assets/appicon.png` etc. resolves the
            // same way). Both projects share the same Yansu assets.
            "@wails": path.resolve(__dirname, "../.."),
            // `~spotlight/...` is the spotlight project's own src.
            "~spotlight": path.resolve(__dirname, "./src"),
        },
    },
});
