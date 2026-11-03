/* eslint-disable */
// Runtime shim for the spotlight. The vite redirect plugin points every
// `wailsjs/runtime/runtime` import (App.tsx + reused main-UI components)
// at this module, so the macOS native build needs no generated wailsjs/.
//
//   - macOS: the helper is a native NSPanel with no Wails runtime.
//     `EventsOn` listens for the CustomEvents the panel dispatches via
//     evaluateJavaScript (e.g. `spotlight:show`); the rest delegate to
//     safe browser fallbacks.
//   - Windows/Linux: the helper is still a Wails app, so we delegate to
//     the injected `window.runtime` globals.
//
// Only the runtime symbols actually imported across the bundled code are
// provided (union over the main UI). If a reused component adds a new one,
// vite build fails with "X is not exported by spotlight-runtime.ts" — add
// it here delegating to window.runtime.

type EventCallback = (...data: any[]) => void;

interface WailsRuntimeGlobal {
    EventsOn(name: string, cb: EventCallback): () => void;
    EventsEmit(name: string, ...data: any[]): void;
    BrowserOpenURL(url: string): void;
    ClipboardSetText(text: string): Promise<boolean>;
    LogError(message: string): void;
    LogInfo(message: string): void;
    WindowGetSize(): Promise<{ w: number; h: number }>;
    OnFileDrop(cb: (x: number, y: number, paths: string[]) => void, useDropTarget: boolean): void;
    OnFileDropOff(): void;
}

function rt(): Partial<WailsRuntimeGlobal> {
    return ((window as unknown as { runtime?: Partial<WailsRuntimeGlobal> }).runtime ?? {});
}

interface SpotlightMessageHandler {
    postMessage(message: unknown): void;
}

function nativeBridge(): SpotlightMessageHandler | null {
    return (
        (window as unknown as { webkit?: { messageHandlers?: { spotlight?: SpotlightMessageHandler } } })
            .webkit?.messageHandlers?.spotlight ?? null
    );
}

export function EventsOn(eventName: string, callback: EventCallback): () => void {
    const bridge = nativeBridge();
    if (bridge) {
        const handler = (e: Event) => callback((e as CustomEvent).detail);
        window.addEventListener(eventName, handler as EventListener);
        // Tell the native panel the listener is live so a buffered first
        // show (fired before React mounted) gets flushed. Keyed to
        // spotlight:show — the event the panel dispatches on summon.
        if (eventName === "spotlight:show") {
            bridge.postMessage({ type: "ready" });
        }
        return () => window.removeEventListener(eventName, handler as EventListener);
    }
    return rt().EventsOn?.(eventName, callback) ?? (() => {});
}

export function EventsEmit(eventName: string, ...data: any[]): void {
    rt().EventsEmit?.(eventName, ...data);
}

export function BrowserOpenURL(url: string): void {
    const fn = rt().BrowserOpenURL;
    if (fn) {
        fn(url);
        return;
    }
    window.open(url, "_blank", "noopener");
}

export function ClipboardSetText(text: string): Promise<boolean> {
    const fn = rt().ClipboardSetText;
    if (fn) return fn(text);
    return navigator.clipboard
        .writeText(text)
        .then(() => true)
        .catch(() => false);
}

export function LogError(message: string): void {
    rt().LogError?.(message);
}

export function LogInfo(message: string): void {
    rt().LogInfo?.(message);
}

export function WindowGetSize(): Promise<{ w: number; h: number }> {
    const fn = rt().WindowGetSize;
    if (fn) return fn();
    return Promise.resolve({ w: window.innerWidth, h: window.innerHeight });
}

export function OnFileDrop(
    callback: (x: number, y: number, paths: string[]) => void,
    useDropTarget: boolean,
): void {
    rt().OnFileDrop?.(callback, useDropTarget);
}

export function OnFileDropOff(): void {
    rt().OnFileDropOff?.();
}
