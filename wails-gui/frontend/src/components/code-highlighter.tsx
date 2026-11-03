import { Prism as SyntaxHighlighter } from "react-syntax-highlighter";
import { oneDark } from "react-syntax-highlighter/dist/esm/styles/prism";

// Prism and its languages (refractor) are about a megabyte; this module loads
// after the first paint instead of with the launch bundle. Code blocks show
// as plain text until it arrives, as they already did while highlights were
// queued (markdown-content.tsx).
const customCodeTheme = {
    ...oneDark,
    'pre[class*="language-"]': {
        ...oneDark['pre[class*="language-"]'],
        background: "var(--color-code-bg)",
        margin: 0,
        borderRadius: "0 0 10px 10px",
    },
    'code[class*="language-"]': {
        ...oneDark['code[class*="language-"]'],
        background: "var(--color-code-bg)",
        fontFamily: "var(--font-mono)",
        fontSize: "13px",
    },
};

export default function CodeHighlighter({ lang, code }: { lang: string; code: string }) {
    return (
        <SyntaxHighlighter
            style={customCodeTheme}
            language={lang}
            PreTag="div"
            wrapLongLines
            customStyle={{
                margin: 0,
                padding: "16px",
                background: "var(--color-code-bg)",
                fontSize: "13px",
                whiteSpace: "pre-wrap",
                wordWrap: "break-word",
            }}
            codeTagProps={{
                style: {
                    fontFamily: "var(--font-mono)",
                    whiteSpace: "pre-wrap",
                    overflowWrap: "anywhere",
                },
            }}
        >
            {code}
        </SyntaxHighlighter>
    );
}
