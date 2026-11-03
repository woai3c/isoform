/** Build-time public contact address. Reject mailto headers and URL syntax. */
export function supportEmail(value: unknown): string {
    const address = typeof value === "string" ? value.trim() : "";
    return /^[A-Za-z0-9.!#$'*+/=_`{|}~-]+@[A-Za-z0-9-]+(?:\.[A-Za-z0-9-]+)+$/.test(address)
        ? address
        : "bo@isoform.ai";
}

export function feedbackMailURL(address: string, subject: string, body: string): string {
    return `mailto:${encodeURIComponent(supportEmail(address))}?subject=${encodeURIComponent(subject)}&body=${encodeURIComponent(body)}`;
}
