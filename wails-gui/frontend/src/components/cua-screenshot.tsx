import { useEffect, useState } from 'react';
import { ReadCuaScreenshotBase64 } from '../../wailsjs/go/main/App';

interface CuaScreenshotProps {
    /** On-disk path to a cua-driver window capture (e.g. /tmp/alma-cu-state-*.jpg). */
    path: string;
    className?: string;
}

// CuaScreenshot lazily loads a cua-driver window capture from disk and
// renders it inline (the Codex-style "watch the agent work" affordance).
// The bytes live on the host and the webview can't read them directly, so
// we pull them through the ReadCuaScreenshotBase64 binding on demand. Not
// persisted — if the temp file is gone (cleanup / reloaded session) the
// binding returns "" and we render nothing.
export function CuaScreenshot({ path, className }: CuaScreenshotProps) {
    const [src, setSrc] = useState<string | null>(null);

    useEffect(() => {
        let cancelled = false;
        setSrc(null);
        ReadCuaScreenshotBase64(path)
            .then((dataUri) => {
                if (!cancelled) setSrc(dataUri || null);
            })
            .catch(() => {
                if (!cancelled) setSrc(null);
            });
        return () => {
            cancelled = true;
        };
    }, [path]);

    if (!src) return null;
    return (
        <img
            src={src}
            alt="Computer use screenshot"
            className={className ?? 'max-h-64 w-auto rounded-xl border border-border'}
        />
    );
}
