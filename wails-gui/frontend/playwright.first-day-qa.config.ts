import { defineConfig } from '@playwright/test';

export default defineConfig({
    testDir: './e2e',
    testMatch: ['first-day-qa.spec.ts'],
    timeout: 120_000,
    workers: 1,
    outputDir: process.env.FIRST_DAY_QA_OUTPUT ?? '/tmp/first-day-qa-browser',
    use: { baseURL: 'http://127.0.0.1:34128', browserName: 'webkit', headless: true, reducedMotion: 'reduce', viewport: { width: 1180, height: 900 }, deviceScaleFactor: 2 },
    webServer: {
        command: 'pnpm exec vite --host 127.0.0.1 --port 34128 --strictPort',
        url: 'http://127.0.0.1:34128/e2e/first-day-qa.html',
        reuseExistingServer: false,
        timeout: 120_000,
    },
});
