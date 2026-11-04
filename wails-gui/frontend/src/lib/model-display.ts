import type { TFunction } from "i18next";
import i18n from "@/i18n";
import type common from "@/i18n/locales/en/common.json";

// Final render guard for cached documents and streamed chat. Go's
// pii.DisplayModelText owns the same boundary for phone and speech delivery.
export function displayModelText(text: string, t: TFunction<"common"> = i18n.getFixedT(null, "common")): string {
    // The phrase for a mask is in the text's own language, never the app's
    // when they differ (today-review v1.1.20 R46).
    const own = modelTextLanguage(text);
    const shown = (t as { lng?: string | null }).lng ?? i18n.language;
    const tt = own && own !== (shown?.startsWith("zh") ? "zh-CN" : "en") ? i18n.getFixedT(own, "common") : t;
    let previousEnd = -1;
    return text.replace(/(?:(^|[\s(“"'])(the|a|an|this|that|these|those|your|my|our|his|her|their|its)\s+|(这个|那个|该))?([\p{Script=Latin}\p{N}\p{M}_]*)\[REDACTED(?::([^\]\r\n]*))?\]?([\p{Script=Latin}\p{N}\p{M}_]*)/giu,
        (tag: string, lead: string | undefined, determiner: string | undefined, zhDeterminer: string | undefined, _before: string, kind: string | undefined, _after: string, offset: number) => {
            const key = kind?.toLowerCase() as keyof typeof common.redactions;
            const known = key && i18n.exists(`common:redactions.${key}`);
            let phrase: string = tt(`redactions.${known ? key : "private_detail"}`);
            // The phrase brings its own article: "The [REDACTED:code] release"
            // reads "A private release" (today-review v1.1.16 D15).
            const next = /^\s+([a-z][a-z'’-]*)/.exec(text.slice(offset + tag.length));
            if (phrase.startsWith("a private ") && next && !MASK_FUNCTION_WORDS.has(next[1])) phrase = "a private";
            if (determiner && /^\p{Lu}/u.test(determiner)) phrase = phrase.charAt(0).toUpperCase() + phrase.slice(1);
            const start = offset + (lead?.length ?? 0);
            const spaced = !determiner && !zhDeterminer && /^[a-z]/i.test(phrase) && (start === previousEnd || /[,;:.!?]/.test(text[start - 1] ?? ""));
            previousEnd = offset + tag.length;
            return (lead ?? "") + (spaced ? " " : "") + phrase;
        });
}

// Go's pii.modelTextLanguage matches: Han means zh-CN, else Latin means en,
// else (masks and punctuation only) the caller's language.
function modelTextLanguage(text: string): "zh-CN" | "en" | null {
    const rest = text.replace(/\[REDACTED(?::[^\]\r\n]*)?\]?/giu, "");
    if (/\p{Script=Han}/u.test(rest)) return "zh-CN";
    return /\p{Script=Latin}/u.test(rest) ? "en" : null;
}

// Words after a mask that start the rest of the sentence rather than name
// the thing the mask describes. Go's pii.modelRedactionFunctionWords matches.
const MASK_FUNCTION_WORDS = new Set([
    "is", "was", "are", "were", "has", "have", "had", "will", "would", "can", "could", "should", "may", "might",
    "and", "or", "but", "to", "in", "on", "at", "for", "with", "from", "by", "of", "that", "which", "who",
    "not", "still", "now", "today", "tomorrow", "again",
]);

// Change copy fields only. Source identities, timestamps, URLs and validation
// digests remain byte-identical, and cached source objects are never mutated.
const copyFields = new Set([
    "text", "title", "name", "names", "topicName", "detail", "summary", "nextStep",
    "question", "userActed", "attentionReason", "label", "prompt", "message",
    "preview", "sourceLabel", "blockedReason", "reason", "interestWhy", "headline",
    "body", "description", "content", "suggestedFollowup", "actionLabels", "params",
]);
export function displayModelCopy<T>(value: T): T {
    if (!(JSON.stringify(value) ?? "").toUpperCase().includes("[REDACTED")) return value;
    function project(node: unknown, copy: boolean): unknown {
        if (typeof node === "string") return copy ? displayModelText(node) : node;
        if (Array.isArray(node)) return node.map((item) => project(item, copy));
        if (node && typeof node === "object") {
            return Object.fromEntries(Object.entries(node).map(([key, item]) => [key, project(item, copy || copyFields.has(key))]));
        }
        return node;
    }
    return project(value, false) as T;
}
