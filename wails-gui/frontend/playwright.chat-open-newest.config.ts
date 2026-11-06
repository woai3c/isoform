import { defineConfig } from '@playwright/test';
export default defineConfig({
    testDir: './e2e', testMatch: ['chat-open-newest.spec.ts', 'stream-window.spec.ts'], workers: 1, timeout: 120_000,
    use: { baseURL: 'http://127.0.0.1:34131', browserName: 'webkit', headless: true, reducedMotion: 'reduce', viewport: { width: 1280, height: 800 }, deviceScaleFactor: 1 },
    webServer: { command: 'pnpm exec vite --host 127.0.0.1 --port 34131 --strictPort', url: 'http://127.0.0.1:34131/e2e/chat-open-newest.html', reuseExistingServer: false, timeout: 120_000 },
});
