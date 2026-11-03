import { defineConfig } from '@playwright/test';

// Setup v2 side by side with the Isoform design prototype, in WebKit (the
// engine of the app's own web view), with Reduce Motion so every state is
// its finished frame. Screenshots land in docs/setup-v2-qa/raw/.
export default defineConfig({
    testDir: './e2e',
    testMatch: ['setup-v2.spec.ts'],
    timeout: 120_000,
    expect: { timeout: 30_000 },
    workers: 1,
    outputDir: process.env.SETUP_V2_OUTPUT ?? '/tmp/setup-v2-browser',
    use: { baseURL: 'http://127.0.0.1:34118', headless: true, browserName: 'webkit', reducedMotion: 'reduce', deviceScaleFactor: 2 },
    webServer: {
        command: 'pnpm exec vite --host 127.0.0.1 --port 34118 --strictPort',
        url: 'http://127.0.0.1:34118/e2e/setup-v2.html',
        reuseExistingServer: false,
        timeout: 120_000,
    },
});
