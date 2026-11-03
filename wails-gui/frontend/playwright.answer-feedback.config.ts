import { defineConfig } from '@playwright/test';

export default defineConfig({
    testDir: './e2e',
    testMatch: 'answer-feedback.spec.ts',
    timeout: 180_000,
    workers: 1,
    use: { baseURL: 'http://127.0.0.1:34132', headless: true, browserName: 'webkit' },
    webServer: {
        command: 'pnpm exec vite --host 127.0.0.1 --port 34132 --strictPort',
        url: 'http://127.0.0.1:34132/e2e/portrait.html',
        reuseExistingServer: false,
        timeout: 120_000,
    },
});
