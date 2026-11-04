import { defineConfig } from '@playwright/test';

export default defineConfig({
    testDir: './e2e',
    testMatch: ['today-launch.spec.ts'],
    timeout: 120_000,
    workers: 1,
    outputDir: process.env.TODAY_LAUNCH_OUTPUT ?? '/tmp/today-launch-browser',
    use: { baseURL: 'http://127.0.0.1:34131', browserName: 'chromium', headless: true, colorScheme: 'light', locale: 'en-US', timezoneId: 'America/Los_Angeles', viewport: { width: 1280, height: 800 }, deviceScaleFactor: 2 },
    webServer: {
        command: 'pnpm exec vite --host 127.0.0.1 --port 34131 --strictPort',
        url: 'http://127.0.0.1:34131/e2e/today-launch.html',
        reuseExistingServer: false,
        timeout: 120_000,
    },
});
