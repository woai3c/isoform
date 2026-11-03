/** Turns a capability key such as "gmail.draft_reply" into "Gmail draft reply". */
export function capabilityLabel(capabilityKey: string): string {
    const label = capabilityKey
        .replaceAll(/[._-]+/g, " ")
        .replaceAll(/\s+/g, " ")
        .trim();
    return label
        ? label.charAt(0).toUpperCase() + label.slice(1)
        : "Unknown capability";
}
