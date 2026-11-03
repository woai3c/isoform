import { useEffect, useRef } from 'react';
import { RecordMainChatProactiveReceipt } from '@/lib/wails-shim-app';
import { productWindowActive } from './useProductTelemetry';

function unobscuredByDialog(element: HTMLElement): boolean {
    if (element.closest('[aria-hidden="true"], [inert]')) return false;
    const dialogs = Array.from(document.querySelectorAll<HTMLElement>(
        '[role="dialog"][data-state="open"], [role="alertdialog"][data-state="open"], [aria-modal="true"], dialog[open]',
    )).filter((dialog) => !dialog.closest('[hidden], [aria-hidden="true"], [data-state="closed"]'));
    const top = dialogs.at(-1);
    return !top || top.contains(element);
}

/** A mounted card was delivered; it is opened only in the focused viewport.
 * Digest wrappers have no receipt of their own. Their children each record
 * the underlying moment. The backend deduplicates receipts across remounts. */
export function useMainChatProactiveReceipts<T extends HTMLElement = HTMLElement>(momentId: string | null, canOpen = true) {
    const elementRef = useRef<T>(null);
    const canOpenRef = useRef(canOpen);
    canOpenRef.current = canOpen;

    useEffect(() => {
        const element = elementRef.current;
        if (!momentId || !element) return;
        let disposed = false;
        let inView = false;
        let delivered = false;
        let opened = false;
        let delivering = false;
        let opening = false;

        const record = async () => {
            if (disposed) return;
            if (!delivered) {
                if (delivering) return;
                delivering = true;
                try {
                    await RecordMainChatProactiveReceipt(momentId, 'delivered');
                    delivered = true;
                } catch {
                    return;
                } finally {
                    delivering = false;
                }
            }
            if (disposed || opened || opening || !canOpenRef.current || !inView || !productWindowActive() || !unobscuredByDialog(element)) return;
            opening = true;
            try {
                await RecordMainChatProactiveReceipt(momentId, 'opened');
                opened = true;
                dialogObserver.disconnect();
            } catch {
                // Retry at the next visibility or focus change.
            } finally {
                opening = false;
            }
        };
        const recordVisible = () => void record();
        // Radix keeps chat mounted behind Settings and hides that background
        // from assistive technology. Recheck when a dialog opens or closes,
        // even when window focus and viewport intersection never changed.
        const dialogObserver = new MutationObserver(recordVisible);
        dialogObserver.observe(document.body, {
            childList: true, subtree: true, attributes: true,
            attributeFilter: ['aria-hidden', 'aria-modal', 'data-state', 'hidden', 'inert', 'open'],
        });
        const observer = typeof IntersectionObserver === 'undefined' ? null : new IntersectionObserver((entries) => {
            inView = entries.some((entry) => entry.isIntersecting);
            recordVisible();
        });
        observer?.observe(element);
        recordVisible();
        window.addEventListener('focus', recordVisible);
        document.addEventListener('visibilitychange', recordVisible);
        return () => {
            disposed = true;
            dialogObserver.disconnect();
            observer?.disconnect();
            window.removeEventListener('focus', recordVisible);
            document.removeEventListener('visibilitychange', recordVisible);
        };
    }, [momentId]);

    return elementRef;
}
