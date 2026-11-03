export const MAX_RECENT_SUMMARY_ENTRIES = 5;

interface ContentPreviewOptions {
    ignoredPrefixes?: string[];
    maxLines?: number;
    maxLength?: number;
}

const DEFAULT_CONTENT_PREVIEW_LINES = 1;
const DEFAULT_CONTENT_PREVIEW_LENGTH = 120;

// Frontmatter-style metadata lines like `- **Updated**: 2026-04-17 14:28` should
// not be surfaced as preview text — they are structured metadata, not content.
// The trailing value is optional so keys with nested children (e.g.
// `- **Activity Session**:` followed by indented group/session bullets) are
// also treated as metadata.
const FRONTMATTER_METADATA_LINE = /^- \*\*[^*]+\*\*:/;

// Indented `- ` bullets are children of a frontmatter key (e.g. the
// `**Update ...** (initial)` group headers and `<sess-id> — ...` rows nested
// under `- **Activity Session**:`), not body content.
const NESTED_METADATA_BULLET = /^\s+-\s/;

export function generateContentPreview(
    content: string,
    {
        ignoredPrefixes = ['#'],
        maxLines = DEFAULT_CONTENT_PREVIEW_LINES,
        maxLength = DEFAULT_CONTENT_PREVIEW_LENGTH,
    }: ContentPreviewOptions = {},
) {
    return content
        .split('\n')
        .filter((line) => {
            if (NESTED_METADATA_BULLET.test(line)) return false;
            const trimmed = line.trim();
            if (!trimmed) return false;
            if (ignoredPrefixes.some((prefix) => trimmed.startsWith(prefix))) {
                return false;
            }
            if (FRONTMATTER_METADATA_LINE.test(trimmed)) return false;
            return true;
        })
        .map((line) => line.trim())
        .slice(0, maxLines)
        .join(' ')
        .slice(0, maxLength);
}
