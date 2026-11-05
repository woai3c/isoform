import { defineConfig } from '@playwright/test';
import polish from './playwright.today-launch.config';
export default defineConfig({ ...polish, testMatch: ['brief-latest-shots.spec.ts'] });
