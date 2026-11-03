import { useEffect, useState, type ReactNode } from 'react';

interface RedactedKeywordProps {
    index?: number;
    children?: ReactNode;
}

type Phase = 'visible' | 'flashing' | 'masking' | 'masked';

const INITIAL_DELAY_MS = 1800;
const STAGGER_MS = 1200;
const FLASH_DURATION_MS = 700;
const MASK_DURATION_MS = 500;

/**
 * Privacy-notice keyword that runs a one-shot intro after the Activity
 * page mounts. Each instance is offset by `index`, so they redact one
 * after another (passwords -> API keys -> ...):
 *   1. visible    – sharp original text, no decoration
 *   2. flashing   – a dotted "scanner" border flashes around the word
 *   3. masking    – clip-path sweeps the sharp text away L->R
 *   4. masked     – resting state, only the pixel-mosaic copy shows
 * Hovering a masked keyword temporarily reveals the original.
 */
export function RedactedKeyword({ index = 0, children }: RedactedKeywordProps) {
    const [phase, setPhase] = useState<Phase>('visible');

    useEffect(() => {
        const base = INITIAL_DELAY_MS + index * STAGGER_MS;
        const t1 = window.setTimeout(() => setPhase('flashing'), base);
        const t2 = window.setTimeout(() => setPhase('masking'), base + FLASH_DURATION_MS);
        const t3 = window.setTimeout(
            () => setPhase('masked'),
            base + FLASH_DURATION_MS + MASK_DURATION_MS,
        );
        return () => {
            window.clearTimeout(t1);
            window.clearTimeout(t2);
            window.clearTimeout(t3);
        };
    }, [index]);

    return (
        <span className={`redaction-keyword redaction-keyword-${phase}`}>
            <span className="redaction-keyword-shadow" aria-hidden>
                {children}
            </span>
            <span className="redaction-keyword-text">{children}</span>
        </span>
    );
}
