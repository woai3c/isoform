import { useTranslation } from "react-i18next";
import { displayModelText } from "@/lib/model-display";
import ReactMarkdown, { type Components } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { BrowserOpenURL } from '../../wailsjs/runtime/runtime';

// The digest's Markdown (headings, lists, bold) in the app's type styles.
// "card" is the Activity digest card. "compact" is Today's "Today so far"
// card, built from the design system (docs/design-system): body-sm text on
// its 1.4 line height, headings as small group labels (11px medium,
// uppercase, wide tracking, color-muted), links in color-accent-2, list
// markers in color-subtle, inline code on color-panel-code. react-markdown
// renders no raw HTML, so a summary can only ever produce these elements.
const DIGEST_MARKDOWN_STYLES = {
    card: `text-[14px] text-ink leading-[1.65]
        [&>*+*]:mt-3
        [&_p]:my-0
        [&_strong]:font-semibold [&_strong]:text-ink
        [&_em]:italic
        [&_a]:text-accent [&_a]:underline [&_a]:underline-offset-2
        [&_ul]:list-disc [&_ul]:pl-5 [&_ul]:space-y-1
        [&_ol]:list-decimal [&_ol]:pl-5 [&_ol]:space-y-1
        [&_li]:marker:text-muted/60
        [&_h1]:text-[16px] [&_h1]:font-semibold [&_h1]:text-ink [&_h1]:mt-4
        [&_h2]:text-[14px] [&_h2]:font-semibold [&_h2]:text-ink [&_h2]:mt-4
        [&_h3]:text-[13px] [&_h3]:font-semibold [&_h3]:text-ink [&_h3]:mt-3 [&_h3]:uppercase [&_h3]:tracking-wider [&_h3]:text-muted
        [&_code]:text-[12.5px] [&_code]:bg-border/40 [&_code]:text-ink [&_code]:px-1 [&_code]:py-0.5 [&_code]:rounded
        [&_pre]:bg-border/30 [&_pre]:p-3 [&_pre]:rounded-md [&_pre]:overflow-x-auto [&_pre_code]:bg-transparent [&_pre_code]:p-0
        [&_blockquote]:border-l-2 [&_blockquote]:border-border [&_blockquote]:pl-3 [&_blockquote]:text-muted [&_blockquote]:italic
        [&_hr]:border-border/60 [&_hr]:my-4`,
    compact: `text-body-sm leading-[1.4] text-ink
        [&>*+*]:mt-2
        [&_p]:my-0
        [&_strong]:font-semibold [&_strong]:text-ink
        [&_em]:italic
        [&_a]:text-accent-2 [&_a]:underline [&_a]:underline-offset-2
        [&_ul]:list-disc [&_ul]:pl-5 [&_ul]:space-y-1
        [&_ol]:list-decimal [&_ol]:pl-5 [&_ol]:space-y-1
        [&_li]:marker:text-muted
        [&_h1]:text-[11px] [&_h1]:font-medium [&_h1]:uppercase [&_h1]:tracking-wider [&_h1]:text-muted [&_h1:not(:first-child)]:mt-3
        [&_h2]:text-[11px] [&_h2]:font-medium [&_h2]:uppercase [&_h2]:tracking-wider [&_h2]:text-muted [&_h2:not(:first-child)]:mt-3
        [&_h3]:text-[11px] [&_h3]:font-medium [&_h3]:uppercase [&_h3]:tracking-wider [&_h3]:text-muted [&_h3:not(:first-child)]:mt-3
        [&_h4]:text-[11px] [&_h4]:font-medium [&_h4]:uppercase [&_h4]:tracking-wider [&_h4]:text-muted [&_h4:not(:first-child)]:mt-3
        [&_code]:text-caption [&_code]:bg-panel-code [&_code]:text-ink [&_code]:px-1 [&_code]:py-0.5 [&_code]:rounded
        [&_pre]:bg-panel-code [&_pre]:p-3 [&_pre]:rounded-lg [&_pre]:overflow-x-auto [&_pre_code]:bg-transparent [&_pre_code]:p-0
        [&_blockquote]:border-l-2 [&_blockquote]:border-border [&_blockquote]:pl-3 [&_blockquote]:text-muted [&_blockquote]:italic
        [&_hr]:border-border [&_hr]:my-3`,
} as const;

// A link opens in the browser, as in chat; the app's window never navigates
// away from Isoform.
const components: Components = {
    a({ href, children }) {
        return (
            <a
                href={href}
                onClick={(event) => {
                    event.preventDefault();
                    if (href && /^(https?:|mailto:)/i.test(href)) BrowserOpenURL(href);
                }}
            >
                {children}
            </a>
        );
    },
};

export function DigestMarkdown({ text, variant = 'card', className = '' }: {
    text: string;
    variant?: keyof typeof DIGEST_MARKDOWN_STYLES;
    className?: string;
}) {
    const { t } = useTranslation("common");
    return (
        <div className={`${DIGEST_MARKDOWN_STYLES[variant]} ${className}`}>
            <ReactMarkdown remarkPlugins={[remarkGfm]} components={components}>{displayModelText(text, t)}</ReactMarkdown>
        </div>
    );
}
