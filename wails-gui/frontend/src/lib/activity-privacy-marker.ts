// Display guard for redaction tags (pii.RedactText writes "[REDACTED:<kind>]").
// Go strips them when a title is written and read (activity_privacy_marker.go);
// this mirrors that rule for titles saved before then, so a tag never reaches a
// screen. Keep the two patterns in step.
const marker = /\s*[·•|/:\-—,]?\s*\[redacted(?::[a-z_]*)?\]?[\p{L}\p{N}_]*/giu;
const edge = /^[\s·•|/:\-—,]+|[\s·•|/:\-—,]+$/gu;
const letterOrDigit = /[\p{L}\p{N}]/u;

export function hasPrivacyMarker(text: string): boolean {
    return /\[redacted/i.test(text);
}

/** The text without redaction tags. Returns "" when nothing readable is left,
 *  so the caller shows its "a private item" label instead. */
export function stripPrivacyMarkers(text: string): string {
    if (!hasPrivacyMarker(text)) return text;
    const out = text.replace(marker, " ").split(/\s+/).filter(Boolean).join(" ").replace(edge, "");
    return letterOrDigit.test(out) ? out : "";
}
