import { defineConfig } from '@playwright/test';
export default defineConfig({
    testDir: './e2e', testMatch: 'brief-card-r38.spec.ts', workers: 1, timeout: 120_000,
    use: { baseURL: 'http://127.0.0.1:34132', browserName: 'webkit', headless: true, reducedMotion: 'reduce', viewport: { width: 1280, height: 800 }, deviceScaleFactor: 1 },
    webServer: { command: 'pnpm exec vite --host 127.0.0.1 --port 34132 --strictPort', url: 'http://127.0.0.1:34132/e2e/brief-card-r38.html', reuseExistingServer: false, timeout: 120_000 },
});
