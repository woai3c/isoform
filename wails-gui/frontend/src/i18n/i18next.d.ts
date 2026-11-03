import 'i18next';

import type { defaultNS, resources } from './index';

/**
 * Augments react-i18next so `useTranslation()` and `t()` are type-checked
 * against the English source files. Adding a key to `locales/en/<ns>.json`
 * automatically makes it available to the typed API.
 */
declare module 'i18next' {
    interface CustomTypeOptions {
        defaultNS: typeof defaultNS;
        resources: (typeof resources)['en'];
        returnNull: false;
        returnEmptyString: false;
        nsSeparator: ':';
        keySeparator: '.';
    }
}
