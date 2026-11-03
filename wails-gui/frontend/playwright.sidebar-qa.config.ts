import { defineConfig } from '@playwright/test';

export default defineConfig({
    testDir: './e2e',
    testMatch: ['sidebar-qa.spec.ts'],
    timeout: 120_000,
    workers: 1,
    outputDir: process.env.SIDEBAR_QA_OUTPUT ?? '/tmp/sidebar-qa-browser',
    use: { baseURL: 'http://127.0.0.1:34127', browserName: 'webkit', headless: true, reducedMotion: 'reduce', viewport: { width: 1180, height: 900 }, deviceScaleFactor: 2 },
    webServer: {
        command: 'pnpm exec vite --host 127.0.0.1 --port 34127 --strictPort',
        url: 'http://127.0.0.1:34127/e2e/sidebar-qa.html',
        reuseExistingServer: false,
        timeout: 120_000,
    },
});
