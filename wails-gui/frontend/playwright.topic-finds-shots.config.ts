import { defineConfig } from '@playwright/test';
export default defineConfig({
    testDir: './e2e', testMatch: 'topic-finds-shots.spec.ts', workers: 1, timeout: 120_000,
    use: { baseURL: 'http://127.0.0.1:34133', browserName: 'webkit', headless: true, reducedMotion: 'reduce', viewport: { width: 880, height: 520 }, deviceScaleFactor: 1 },
    webServer: { command: 'pnpm exec vite --host 127.0.0.1 --port 34133 --strictPort', url: 'http://127.0.0.1:34133/e2e/topic-finds-shots.html', reuseExistingServer: false, timeout: 120_000 },
});
