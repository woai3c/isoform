import { defineConfig } from '@playwright/test';
import polish from './playwright.polish.config';
export default defineConfig({ ...polish, testMatch: ['today-f1.spec.ts'] });
