// wails-shim-runtime.ts
// Unified shim for wailsjs/runtime/runtime that works in both Wails native
// mode and plain browser mode. Every exported function checks
// `window.runtime?.<Fn>` at call time and delegates to it when present;
// otherwise a browser-native fallback is used.
//
// Covers every API declared in wailsjs/runtime/runtime.d.ts, so the shim
// can transparently replace the generated runtime module via the Vite
// resolver.

import { onEvent, offEvent, emitEvent as bridgeEmit } from './browser-bridge';

type AnyFn = (...args: any[]) => any;

function native(): any {
  return (globalThis as any)?.window?.runtime;
}

function call<T extends AnyFn>(name: string, fallback: T, ...args: any[]): ReturnType<T> {
  const rt = native();
  if (rt && typeof rt[name] === 'function') {
    return rt[name](...args);
  }
  return fallback(...args);
}

// ---------------------------------------------------------------------------
// Events
// ---------------------------------------------------------------------------

export function EventsEmit(eventName: string, ...data: any[]): void {
  const rt = native();
  if (rt && typeof rt.EventsEmit === 'function') {
    rt.EventsEmit(eventName, ...data);
    return;
  }
  bridgeEmit(eventName, ...data);
}

export function EventsOn(
  eventName: string,
  callback: (...data: any[]) => void,
): () => void {
  const rt = native();
  if (rt && typeof rt.EventsOn === 'function') {
    return rt.EventsOn(eventName, callback);
  }
  return onEvent(eventName, callback);
}

export function EventsOnMultiple(
  eventName: string,
  callback: (...data: any[]) => void,
  maxCallbacks: number,
): () => void {
  const rt = native();
  if (rt && typeof rt.EventsOnMultiple === 'function') {
    return rt.EventsOnMultiple(eventName, callback, maxCallbacks);
  }
  let count = 0;
  const unsub = onEvent(eventName, (...args) => {
    if (count >= maxCallbacks) return;
    count++;
    callback(...args);
    if (count >= maxCallbacks) unsub();
  });
  return unsub;
}

export function EventsOnce(
  eventName: string,
  callback: (...data: any[]) => void,
): () => void {
  return EventsOnMultiple(eventName, callback, 1);
}

export function EventsOff(eventName: string, ..._rest: string[]): void {
  const rt = native();
  if (rt && typeof rt.EventsOff === 'function') {
    rt.EventsOff(eventName, ..._rest);
    return;
  }
  offEvent(eventName);
  for (const n of _rest) offEvent(n);
}

export function EventsOffAll(): void {
  const rt = native();
  if (rt && typeof rt.EventsOffAll === 'function') {
    rt.EventsOffAll();
  }
  // No browser-side equivalent; offEvent needs a name.
}

// ---------------------------------------------------------------------------
// Logging
// ---------------------------------------------------------------------------

const mkLog = (level: string, fn: (m: string) => void) => (message: string) => {
  const rt = native();
  if (rt && typeof rt[level] === 'function') {
    rt[level](message);
    return;
  }
  fn(`[wails-shim] ${message}`);
};

export const LogPrint = mkLog('LogPrint', (m) => console.log(m));
export const LogTrace = mkLog('LogTrace', (m) => console.debug(m));
export const LogDebug = mkLog('LogDebug', (m) => console.debug(m));
export const LogInfo = mkLog('LogInfo', (m) => console.info(m));
export const LogWarning = mkLog('LogWarning', (m) => console.warn(m));
export const LogError = mkLog('LogError', (m) => console.error(m));
export const LogFatal = mkLog('LogFatal', (m) => console.error(m));

// ---------------------------------------------------------------------------
// Window
// ---------------------------------------------------------------------------

const noop = () => { /* no-op in browser */ };

export function WindowReload(): void { call('WindowReload', () => window.location.reload()); }
export function WindowReloadApp(): void { call('WindowReloadApp', () => window.location.reload()); }
export function WindowSetAlwaysOnTop(b: boolean): void { call('WindowSetAlwaysOnTop', noop, b); }
export function WindowSetSystemDefaultTheme(): void { call('WindowSetSystemDefaultTheme', noop); }
export function WindowSetLightTheme(): void { call('WindowSetLightTheme', noop); }
export function WindowSetDarkTheme(): void { call('WindowSetDarkTheme', noop); }
export function WindowCenter(): void { call('WindowCenter', noop); }
export function WindowSetTitle(title: string): void {
  call('WindowSetTitle', (t: string) => { document.title = t; }, title);
}
export function WindowFullscreen(): void { call('WindowFullscreen', noop); }
export function WindowUnfullscreen(): void { call('WindowUnfullscreen', noop); }
export function WindowIsFullscreen(): Promise<boolean> {
  return call('WindowIsFullscreen', async () => false);
}
export function WindowSetSize(width: number, height: number): void {
  call('WindowSetSize', noop, width, height);
}
export function WindowGetSize(): Promise<{ w: number; h: number }> {
  return call('WindowGetSize', async () => ({ w: window.innerWidth, h: window.innerHeight }));
}
export function WindowSetMaxSize(width: number, height: number): void {
  call('WindowSetMaxSize', noop, width, height);
}
export function WindowSetMinSize(width: number, height: number): void {
  call('WindowSetMinSize', noop, width, height);
}
export function WindowSetPosition(x: number, y: number): void {
  call('WindowSetPosition', noop, x, y);
}
export function WindowGetPosition(): Promise<{ x: number; y: number }> {
  return call('WindowGetPosition', async () => ({ x: 0, y: 0 }));
}
export function WindowHide(): void { call('WindowHide', noop); }
export function WindowShow(): void { call('WindowShow', noop); }
export function WindowMaximise(): void { call('WindowMaximise', noop); }
export function WindowToggleMaximise(): void { call('WindowToggleMaximise', noop); }
export function WindowUnmaximise(): void { call('WindowUnmaximise', noop); }
export function WindowIsMaximised(): Promise<boolean> {
  return call('WindowIsMaximised', async () => false);
}
export function WindowMinimise(): void { call('WindowMinimise', noop); }
export function WindowUnminimise(): void { call('WindowUnminimise', noop); }
export function WindowIsMinimised(): Promise<boolean> {
  return call('WindowIsMinimised', async () => false);
}
export function WindowIsNormal(): Promise<boolean> {
  return call('WindowIsNormal', async () => true);
}
export function WindowSetBackgroundColour(R: number, G: number, B: number, A: number): void {
  call(
    'WindowSetBackgroundColour',
    (r: number, g: number, b: number, a: number) => {
      document.body.style.backgroundColor = `rgba(${r}, ${g}, ${b}, ${a / 255})`;
    },
    R,
    G,
    B,
    A,
  );
}

// ---------------------------------------------------------------------------
// Screen
// ---------------------------------------------------------------------------

export function ScreenGetAll(): Promise<any[]> {
  return call('ScreenGetAll', async () => [
    {
      isCurrent: true,
      isPrimary: true,
      width: window.screen?.width ?? window.innerWidth,
      height: window.screen?.height ?? window.innerHeight,
    },
  ]);
}

// ---------------------------------------------------------------------------
// Browser / App lifecycle
// ---------------------------------------------------------------------------

export function BrowserOpenURL(url: string): void {
  call(
    'BrowserOpenURL',
    (u: string) => { window.open(u, '_blank', 'noopener'); },
    url,
  );
}

export function Environment(): Promise<any> {
  return call('Environment', async () => ({
    buildType: 'browser',
    platform: 'browser',
    arch: '',
  }));
}

export function Quit(): void { call('Quit', noop); }
export function Hide(): void { call('Hide', noop); }
export function Show(): void { call('Show', noop); }

// ---------------------------------------------------------------------------
// Clipboard
// ---------------------------------------------------------------------------

export function ClipboardGetText(): Promise<string> {
  return call('ClipboardGetText', async () => {
    try { return await navigator.clipboard.readText(); } catch { return ''; }
  });
}

export function ClipboardSetText(text: string): Promise<boolean> {
  return call(
    'ClipboardSetText',
    async (t: string) => {
      try {
        await navigator.clipboard.writeText(t);
        return true;
      } catch {
        return false;
      }
    },
    text,
  );
}

// ---------------------------------------------------------------------------
// File drop
// ---------------------------------------------------------------------------

export function OnFileDrop(
  callback: (x: number, y: number, paths: string[]) => void,
  useDropTarget?: boolean,
): void {
  call('OnFileDrop', noop, callback, useDropTarget);
}

export function OnFileDropOff(): void {
  call('OnFileDropOff', noop);
}

export function CanResolveFilePaths(): boolean {
  const rt = native();
  if (rt && typeof rt.CanResolveFilePaths === 'function') {
    return rt.CanResolveFilePaths();
  }
  return false;
}

export function ResolveFilePaths(files: File[]): void {
  call('ResolveFilePaths', noop, files);
}
