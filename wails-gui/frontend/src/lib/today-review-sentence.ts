/** Display source-provided translations without changing the item's identity. */
export function todayReviewSentence(item: { sentence: string; sentenceLocalized?: Record<string, string> }, language?: string): string {
    const locale = language?.toLowerCase().startsWith("zh") ? "zh-CN" : "en";
    return item.sentenceLocalized?.[locale] || item.sentence;
}
