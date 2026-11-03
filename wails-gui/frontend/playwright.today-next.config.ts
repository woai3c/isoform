import { defineConfig } from '@playwright/test';
import polish from './playwright.polish.config';
export default defineConfig({ ...polish, testMatch: ['today-next.spec.ts'] });
