import { defineConfig } from '@playwright/test';

export default defineConfig({
    testDir: './e2e',
    testMatch: ['today-one-state.spec.ts'],
    timeout: 120_000,
    workers: 1,
    use: { baseURL: 'http://127.0.0.1:34321', browserName: 'webkit', headless: true, reducedMotion: 'reduce', timezoneId: 'America/Los_Angeles', viewport: { width: 1280, height: 800 }, deviceScaleFactor: 2 },
    webServer: {
        command: 'pnpm exec vite --host 127.0.0.1 --port 34321 --strictPort',
        url: 'http://127.0.0.1:34321/e2e/today-one-state.html',
        reuseExistingServer: false,
        timeout: 120_000,
    },
});
