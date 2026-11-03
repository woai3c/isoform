import i18n from 'i18next';
import LanguageDetector from 'i18next-browser-languagedetector';
import { initReactI18next } from 'react-i18next';

// Shared by the Spotlight helper so it uses this initialized instance.
export { useTranslation } from 'react-i18next';

import commonEn from './locales/en/common.json';
import chatEn from './locales/en/chat.json';
import settingsEn from './locales/en/settings.json';
import appEn from './locales/en/app.json';
import onboardingEn from './locales/en/onboarding.json';
import activityEn from './locales/en/activity.json';
import knowledgeEn from './locales/en/knowledge.json';
import memoryEn from './locales/en/memory.json';
import peopleEn from './locales/en/people.json';
import todayEn from './locales/en/today.json';
import commonZhCN from './locales/zh-CN/common.json';
import chatZhCN from './locales/zh-CN/chat.json';
import settingsZhCN from './locales/zh-CN/settings.json';
import appZhCN from './locales/zh-CN/app.json';
import onboardingZhCN from './locales/zh-CN/onboarding.json';
import activityZhCN from './locales/zh-CN/activity.json';
import knowledgeZhCN from './locales/zh-CN/knowledge.json';
import memoryZhCN from './locales/zh-CN/memory.json';
import peopleZhCN from './locales/zh-CN/people.json';
import todayZhCN from './locales/zh-CN/today.json';

/**
 * i18n scaffolding.
 *
 * English is the source of truth for every key. Other languages mirror
 * the English files and are looked up by namespace+key.
 *
 * Conventions:
 *   - One namespace per feature (e.g. `chat`, `settings`). `common` is
 *     reserved for truly cross-cutting strings (Save, Cancel, ...).
 *   - Keys may be flat or lightly nested. Pick whichever reads better
 *     within a namespace and keep it consistent inside that file.
 *   - Never pass an inline default value to `t()`. The English JSON
 *     file is authoritative; missing keys should surface as bugs.
 *
 * Translation strategy:
 *   - English (`en`) is the source of truth — every key must exist here.
 *   - Both release languages must define every key; pnpm test checks parity.
 *     Unsupported language selections still fall back to English because
 *     `fallbackLng: 'en'` and `returnEmptyString: false` are set below.
 *     English fallback does not replace complete release translations.
 */
export const defaultNS = 'common';

export const resources = {
    en: {
        common: commonEn,
        chat: chatEn,
        settings: settingsEn,
        app: appEn,
        onboarding: onboardingEn,
        activity: activityEn,
        knowledge: knowledgeEn,
        memory: memoryEn,
        people: peopleEn,
        today: todayEn,
    },
    'zh-CN': {
        common: commonZhCN,
        chat: chatZhCN,
        settings: settingsZhCN,
        app: appZhCN,
        onboarding: onboardingZhCN,
        activity: activityZhCN,
        knowledge: knowledgeZhCN,
        memory: memoryZhCN,
        people: peopleZhCN,
        today: todayZhCN,
    },
} as const;

export const supportedLngs = ['en', 'zh-CN'] as const;

export type SupportedLanguage = (typeof supportedLngs)[number];

/** What the user picked in Settings: a fixed language, or 'system' to follow
 *  the computer's language. */
export type LanguagePreference = SupportedLanguage | 'system';

// Only an explicit choice is stored. With nothing stored, detection falls
// through to the browser (system) language on every launch, which is what
// "System default" means. Older builds cached the detected language here on
// first run; those users see that language selected until they pick System
// default.
const LANGUAGE_STORAGE_KEY = 'i18nextLng';

void i18n
    .use(LanguageDetector)
    .use(initReactI18next)
    .init({
        resources,
        fallbackLng: 'en',
        supportedLngs: [...supportedLngs],
        defaultNS,
        ns: ['common', 'chat', 'settings', 'app', 'onboarding', 'activity', 'knowledge', 'memory', 'people', 'today'],
        nsSeparator: ':',
        keySeparator: '.',
        returnNull: false,
        returnEmptyString: false,
        debug: false,
        interpolation: {
            escapeValue: false,
        },
        react: {
            useSuspense: false,
        },
        detection: {
            order: ['localStorage', 'navigator'],
            // Not cached automatically: setLanguagePreference stores explicit
            // choices only, so System default keeps following the computer.
            caches: [],
            lookupLocalStorage: LANGUAGE_STORAGE_KEY,
        },
    });

/** The language choice shown in Settings. */
export function getLanguagePreference(): LanguagePreference {
    try {
        const stored = localStorage.getItem(LANGUAGE_STORAGE_KEY)?.toLowerCase();
        if (stored?.startsWith('zh')) return 'zh-CN';
        if (stored?.startsWith('en')) return 'en';
    } catch {
        // Storage unavailable: nothing was stored, so the system language applies.
    }
    return 'system';
}

/** Applies a language choice and remembers it. 'system' forgets the stored
 *  choice and switches to the computer's language. */
export function setLanguagePreference(preference: LanguagePreference): Promise<unknown> {
    try {
        if (preference === 'system') {
            localStorage.removeItem(LANGUAGE_STORAGE_KEY);
        } else {
            localStorage.setItem(LANGUAGE_STORAGE_KEY, preference);
        }
    } catch {
        // Storage unavailable: the language still changes for this session.
    }
    // changeLanguage(undefined) re-runs detection, which now skips the
    // removed stored value and lands on the system language.
    return i18n.changeLanguage(preference === 'system' ? undefined : preference);
}

export default i18n;
