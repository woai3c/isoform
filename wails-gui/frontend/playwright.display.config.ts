import { defineConfig } from '@playwright/test';

export default defineConfig({
    testDir: './e2e',
    testMatch: 'ppmori-display.spec.ts',
    timeout: 60_000,
    expect: { timeout: 15_000 },
    workers: 1,
    outputDir: process.env.DISPLAY_PROOF_DIR ?? '/tmp/ppmori-display-browser',
    use: { baseURL: 'http://127.0.0.1:34119', headless: true, browserName: 'webkit', reducedMotion: 'reduce', viewport: { width: 1400, height: 1000 } },
    webServer: {
        command: 'pnpm exec vite --host 127.0.0.1 --port 34119 --strictPort',
        url: 'http://127.0.0.1:34119/e2e/ppmori-display.html',
        reuseExistingServer: false,
    },
});
