import { defineConfig } from '@playwright/test';

export default defineConfig({
    testDir: './e2e',
    testMatch: 'activity-day-bounds.spec.ts',
    timeout: 60_000,
    workers: 1,
    outputDir: '/tmp/codex-activity-day-bounds-browser',
    use: { baseURL: 'http://127.0.0.1:34129', headless: true, browserName: 'webkit' },
    webServer: {
        command: 'pnpm exec vite --host 127.0.0.1 --port 34129 --strictPort',
        url: 'http://127.0.0.1:34129/e2e/activity-day-bounds.html',
        reuseExistingServer: false,
        timeout: 60_000,
    },
});
