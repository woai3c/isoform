import { defineConfig } from '@playwright/test';
export default defineConfig({
    testDir: './e2e', testMatch: 'walk102-chat.spec.ts', workers: 1, timeout: 120_000,
    use: { baseURL: 'http://127.0.0.1:34130', browserName: 'webkit', headless: true, reducedMotion: 'reduce', viewport: { width: 1280, height: 800 }, deviceScaleFactor: 2 },
    webServer: { command: 'pnpm exec vite --host 127.0.0.1 --port 34130 --strictPort', url: 'http://127.0.0.1:34130/e2e/walk102-chat.html', reuseExistingServer: false, timeout: 120_000 },
});
