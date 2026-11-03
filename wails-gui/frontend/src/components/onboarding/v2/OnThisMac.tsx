import { useEffect, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import { SetupIcon } from "./SetupIcons";
import { WhatIsoformSeesSheet } from "@/components/what-isoform-sees";

// "On this computer" (handoff-2026-10-28-1315-setup-v2.1.0 §1): a pill in the
// setup window's top-right corner on every screen. It opens the four lines
// of what stays here; a click outside or Escape closes them. Settings ›
// Privacy opens with the same lines (OnThisMacLines).

export const ON_THIS_MAC_LINES = ["recordings", "ai", "phone", "usage"] as const;

const bold = { b: <span /> };

/** The four lines, each led by what it is about. */
export function OnThisMacLines({ className }: { className?: string }) {
    const { t } = useTranslation("onboarding");
    return (
        <ul className={className}>
            {ON_THIS_MAC_LINES.map((key) => (
                <li key={key}>
                    <Trans t={t} i18nKey={`v2.local.lines.${key}`} components={bold} />
                </li>
            ))}
        </ul>
    );
}

export function OnThisMac({ initiallyOpen = false, onNavigate }: { initiallyOpen?: boolean; onNavigate?: (job: "mail" | "calendar" | "calls" | "screen" | "assistant" | "contacts") => void }) {
    const { t } = useTranslation("onboarding");
    const [open, setOpen] = useState(initiallyOpen);
    useEffect(() => {
        if (!open) return;
        const onKey = (event: KeyboardEvent) => {
            if (event.key !== "Escape" || event.isComposing || event.keyCode === 229) return;
            event.preventDefault();
            event.stopPropagation();
            setOpen(false);
        };
        const onPointer = (event: PointerEvent) => {
            const target = event.target;
            if (target instanceof Element && !target.closest('[data-what-isoform-sees], [data-testid="setup-v2-local"]')) setOpen(false);
        };
        window.addEventListener("keydown", onKey, true);
        window.addEventListener("pointerdown", onPointer, true);
        return () => { window.removeEventListener("keydown", onKey, true); window.removeEventListener("pointerdown", onPointer, true); };
    }, [open]);
    return (
        <div>
            <button
                className="local"
                type="button"
                data-testid="setup-v2-local"
                aria-expanded={open}
                onClick={() => setOpen((v) => !v)}
            >
                <SetupIcon id="i-lock" />
                {t("v2.local.pill")}
            </button>
            <WhatIsoformSeesSheet open={open} onOpenChange={setOpen} onNavigate={onNavigate} />
        </div>
    );
}
