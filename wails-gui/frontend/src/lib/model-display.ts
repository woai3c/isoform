import type { TFunction } from "i18next";
import i18n from "@/i18n";
import type common from "@/i18n/locales/en/common.json";

// Final render guard for cached documents and streamed chat. Go's
// pii.DisplayModelText owns the same boundary for phone and speech delivery.
export function displayModelText(text: string, t: TFunction<"common"> = i18n.getFixedT(null, "common")): string {
    let previousEnd = -1;
    return text.replace(/([\p{Script=Latin}\p{N}\p{M}_]*)\[REDACTED(?::([^\]\r\n]*))?\]?([\p{Script=Latin}\p{N}\p{M}_]*)/giu,
        (tag: string, _before: string, kind: string | undefined, _after: string, offset: number) => {
            const key = kind?.toLowerCase() as keyof typeof common.redactions;
            const known = key && i18n.exists(`common:redactions.${key}`);
            const phrase = t(`redactions.${known ? key : "private_detail"}`);
            const spaced = /^[a-z]/i.test(phrase) && (offset === previousEnd || /[,;:.!?]/.test(text[offset - 1] ?? ""));
            previousEnd = offset + tag.length;
            return (spaced ? " " : "") + phrase;
        });
}

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
