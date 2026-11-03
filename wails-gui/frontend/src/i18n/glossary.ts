// Product and feature names that stay in English in every locale.
//
// These are proper names: they match Isoform's docs and the other tools it
// builds on (Claude Code, Codex, MCP), so translating them would make the UI
// diverge from what users already see elsewhere. Keep the English spelling
// inside a translated sentence (for example "Running Skill" inside Chinese).
//
// "Skill / Skills" is stored as two entries so the whole-word check in
// `glossary.test.ts` catches both the singular and the plural. This array is
// the source of truth for the "Do not translate" list in the i18n README.
export const UNTRANSLATED_TERMS = [
    'Isoform',
    'Skill',
    'Skills',
    'MCP',
    'Claude Code',
    'Codex',
] as const;
