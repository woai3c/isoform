/** Brief keys use section.hash; older records can use section:id or section-id. */
export function isCommitmentItemKey(key: string | undefined): boolean {
    return key !== undefined && /^commitments[.:-]/.test(key);
}

/** Only this action opens a calendar proposal; other commitment chips retain their action. */
export function isFindTimeLabel(label: string | undefined): boolean {
    return /^(?:find time\b|为.*留出时间)/i.test(label ?? "");
}
