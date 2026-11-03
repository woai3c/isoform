import { ReportLaunchStage } from './wails-shim-app';

const reported = new Set<string>();
export function markLaunchStage(stage: string) {
    if (reported.has(stage)) return;
    reported.add(stage);
    try { void ReportLaunchStage(stage).catch(() => {}); } catch { /* Older browser bridge. */ }
    if (!reported.has('first_screen_rendered') && ['first_data', 'chat_rendered', 'counts_rendered', 'overview_rendered'].every((name) => reported.has(name))) {
        markLaunchPaint('first_screen_rendered');
    }
}

export function markLaunchPaint(stage: string) {
    requestAnimationFrame(() => requestAnimationFrame(() => markLaunchStage(stage)));
}
