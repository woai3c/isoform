import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { GetScreenRecordingRelaunch, RelaunchApp } from '@/lib/wails-shim-app';
import { EventsOn } from '../../wailsjs/runtime/runtime';

// Screen Recording turned on while Isoform runs works only after Isoform
// quits and opens again. macOS offers its own Quit & Reopen, which reopens
// the app by bundle id and can open another copy or none; this notice
// reopens this exact app (RelaunchApp in relaunch.go). The setup sequence and
// the onboarding step are saved, so both resume where they were.

/**
 * The product name while Screen Recording was turned on after this launch
 * (the app has to reopen), otherwise null.
 */
export function useScreenRecordingRelaunch(enabled = true): string | null {
    const [app, setApp] = useState<string | null>(null);
    useEffect(() => {
        if (!enabled) {
            setApp(null);
            return;
        }
        let live = true;
        const refresh = () => {
            void GetScreenRecordingRelaunch()
                .then((next) => {
                    if (live) setApp(next?.needed && next.app ? next.app : null);
                })
                .catch(() => {});
        };
        const onVisible = () => {
            if (document.visibilityState === 'visible') refresh();
        };
        refresh();
        const off = EventsOn('permissions:changed', refresh);
        window.addEventListener('focus', refresh);
        document.addEventListener('visibilitychange', onVisible);
        return () => {
            live = false;
            off();
            window.removeEventListener('focus', refresh);
            document.removeEventListener('visibilitychange', onVisible);
        };
    }, [enabled]);
    return app;
}

/** Says the app (its product name) has to reopen for Screen Recording, with Quit and reopen. */
export function RelaunchNotice({ app, className }: { app: string; className?: string }) {
    const { t } = useTranslation('common');
    const [state, setState] = useState<'idle' | 'reopening' | 'failed'>('idle');
    // A quit that stalls past the wait reopens nothing (relaunch.go).
    useEffect(() => EventsOn('relaunch:failed', () => setState('failed')), []);
    const reopen = () => {
        setState('reopening');
        RelaunchApp().catch((err) => {
            console.error('[relaunch] quit and reopen failed:', err);
            setState('failed');
        });
    };
    const reopening = state === 'reopening';
    return (
        <div
            data-relaunch-notice
            className={cn('animate-rise-in rounded-2xl border border-border bg-background p-4 flex flex-col gap-2', className)}
        >
            <div className="text-[15px] font-semibold text-ink">{t('relaunch.title', { app })}</div>
            <p className="text-[13px] leading-relaxed text-muted">{t('relaunch.body', { app })}</p>
            {state === 'failed' ? (
                <p data-relaunch-failed className="text-[12px] text-error">
                    {t('relaunch.failed', { app })}
                </p>
            ) : null}
            <div className="flex items-center gap-2 pt-1">
                <Button size="sm" data-relaunch-action disabled={reopening} onClick={reopen}>
                    {reopening ? <Loader2 className="size-3.5 animate-spin" /> : null}
                    {reopening ? t('relaunch.reopening') : t('relaunch.action')}
                </Button>
            </div>
        </div>
    );
}
