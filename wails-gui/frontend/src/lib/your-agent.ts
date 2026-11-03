// Your agent (coming soon): the page-name and email rules the Settings page
// checks while the person types. They match the Go rules in
// wails-gui/your_agent.go and the server's rules (docs/agents-api.md).

/** 3 to 30 characters of a to z, 0 to 9 and dots; no dot first or last, never two in a row. */
const NAME_PATTERN = /^[a-z0-9](?:[a-z0-9]|\.(?=[a-z0-9])){2,29}$/;

/** Names kept for Isoform. A reserved name is refused with "That name isn't available." */
export const YOUR_AGENT_RESERVED_NAMES: readonly string[] = [
    "isoform", "agents", "agent", "admin", "administrator", "support", "help", "www", "api",
    "app", "apps", "mail", "email", "root", "system", "status", "blog", "docs", "about",
    "login", "signup", "settings", "bo", "team", "staff", "official", "security", "billing",
];

const EMAIL_PATTERN = /^[^\s@]+@[^\s@]+\.[^\s@]+$/;

/** Lowercases and trims what the person typed, as the server does. */
export function normalizeYourAgentName(name: string): string {
    return name.trim().toLowerCase();
}

/** True when the name has the right shape. Reserved names are checked separately. */
export function isValidYourAgentNameFormat(name: string): boolean {
    return NAME_PATTERN.test(name);
}

/** True when the name has the right shape and is not kept for Isoform. */
export function isUsableYourAgentName(name: string): boolean {
    return NAME_PATTERN.test(name) && !YOUR_AGENT_RESERVED_NAMES.includes(name);
}

/**
 * Cleans the name field as the person types: lowercases, refuses anything but
 * a to z, 0 to 9 and dots, and refuses a second dot in a row. Length and a dot
 * at either end are left for the rules line on blur or Send (v2.0.1).
 */
export function sanitizeYourAgentNameInput(raw: string): string {
    let out = "";
    for (const ch of raw.toLowerCase()) {
        if (!/[a-z0-9.]/.test(ch)) continue;
        if (ch === "." && out.endsWith(".")) continue;
        out += ch;
    }
    return out;
}

/** The start of the next UTC hour: the fallback retry time until the server names one. */
export function nextUtcHour(now: Date): Date {
    return new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate(), now.getUTCHours() + 1));
}

/**
 * A short local clock time for the rate-limit line: "9:40 PM" in English,
 * "晚上 9:40" in Chinese (v2.0.1).
 */
export function formatYourAgentClock(date: Date, language: string): string {
    const hours = date.getHours();
    const minutes = String(date.getMinutes()).padStart(2, "0");
    const hour12 = hours % 12 === 0 ? 12 : hours % 12;
    if (language.toLowerCase().startsWith("zh")) {
        let period = "凌晨";
        if (hours >= 6 && hours < 12) period = "上午";
        else if (hours === 12) period = "中午";
        else if (hours > 12 && hours < 18) period = "下午";
        else if (hours >= 18) period = "晚上";
        return `${period} ${hour12}:${minutes}`;
    }
    return `${hour12}:${minutes} ${hours < 12 ? "AM" : "PM"}`;
}

/** Form check only, as the spec says: no mailbox lookup. */
export function isValidYourAgentEmail(email: string): boolean {
    const trimmed = email.trim();
    return trimmed.length <= 254 && EMAIL_PATTERN.test(trimmed);
}

/** Keeps the single verification field numeric and at most six digits. */
export function sanitizeYourAgentCode(raw: string): string {
    return raw.replace(/[^0-9]/g, "").slice(0, 6);
}
