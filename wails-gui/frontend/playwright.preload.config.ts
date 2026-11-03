import { defineConfig } from '@playwright/test';

export default defineConfig({
    testDir: './e2e',
    testMatch: ['preload-pages.spec.ts'],
    timeout: 120_000,
    workers: 1,
    use: { baseURL: 'http://127.0.0.1:5179', browserName: 'webkit', headless: true, reducedMotion: 'reduce' },
    webServer: {
        command: 'pnpm exec vite --host 127.0.0.1 --port 5179 --strictPort',
        url: 'http://127.0.0.1:5179/e2e/preload-pages.html',
        reuseExistingServer: false,
    },
});
