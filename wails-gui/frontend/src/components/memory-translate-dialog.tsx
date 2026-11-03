import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Languages } from 'lucide-react';
import {
    Dialog,
    DialogCloseButton,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { StartTranslateAllCurrentMemoryEntries } from '../../wailsjs/go/main/App';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { notifyToast } from '@/lib/notify';

type GlobalTranslateTarget = 'follow-ui' | 'zh-CN' | 'en' | 'other';
// The batch summary arrives only as an event, so it has no generated model.
type GlobalTranslationDoneEvent = {
    scope: string;
    summary?: { translated: number; failed: number; total: number; targetLanguage?: string };
    error?: string;
};

function normalizeUILanguage(lang: string | undefined | null): 'zh-CN' | 'en' {
    return (lang ?? '').toLowerCase().trim().startsWith('zh') ? 'zh-CN' : 'en';
}

function uiLanguageLabel(lang: string): string {
    return normalizeUILanguage(lang) === 'zh-CN' ? '简体中文' : 'English';
}

/**
 * Translate every current memory in the background, from the page's More
 * menu. Stays mounted so the finished batch still reports while closed.
 */
export function MemoryTranslateAllDialog({
    open,
    onOpenChange,
    entryCount,
    onBusyChange,
    onDone,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    entryCount: number;
    onBusyChange: (busy: boolean) => void;
    onDone: () => void;
}) {
    const { t, i18n } = useTranslation(['memory', 'common']);
    const [globalTranslating, setGlobalTranslatingState] = useState(false);
    const [globalTranslateTarget, setGlobalTranslateTarget] = useState<GlobalTranslateTarget>('follow-ui');
    const [globalTranslateCustomLanguage, setGlobalTranslateCustomLanguage] = useState('');
    const [globalTranslateProgress, setGlobalTranslateProgress] = useState<{ done: number; total: number } | null>(null);
    const currentUILanguage = normalizeUILanguage(i18n.resolvedLanguage ?? i18n.language);
    const globalTranslateTargetLabel =
        globalTranslateTarget === 'follow-ui'
            ? t('globalTranslate.targets.currentWithLanguage', { language: uiLanguageLabel(currentUILanguage) }) as string
            : globalTranslateTarget === 'other'
                ? globalTranslateCustomLanguage.trim() || t('globalTranslate.targets.other') as string
            : t(`globalTranslate.targets.${globalTranslateTarget}` as never) as string;
    const setGlobalTranslating = useCallback((busy: boolean) => {
        setGlobalTranslatingState(busy);
        onBusyChange(busy);
    }, [onBusyChange]);

    useEffect(() => {
        const cleanup = EventsOn('global-translation:done', (event: GlobalTranslationDoneEvent) => {
            if (event.scope !== 'memory') return;

            setGlobalTranslating(false);
            setGlobalTranslateProgress(null);
            onDone();

            if (event.error) {
                notifyToast(
                    t('toasts.translateFailed.title') as string,
                    event.error,
                    'error',
                );
                return;
            }

            const summary = event.summary;
            if (!summary) return;
            notifyToast(
                summary.failed > 0 ? t('toasts.translateFailed.title') as string : t('toasts.globalTranslated.title') as string,
                t('toasts.globalTranslated.body', {
                    translated: summary.translated,
                    failed: summary.failed,
                    total: summary.total,
                    target: summary.targetLanguage || globalTranslateTargetLabel,
                }) as string,
                summary.failed > 0 ? 'error' : 'success',
            );
        });
        return cleanup;
    }, [globalTranslateTargetLabel, onDone, setGlobalTranslating, t]);

    const handleGlobalTranslate = useCallback(async () => {
        if (globalTranslating) return;
        if (globalTranslateTarget === 'other' && globalTranslateCustomLanguage.trim() === '') {
            notifyToast(
                t('toasts.translateFailed.title') as string,
                t('globalTranslate.customRequired') as string,
                'error',
            );
            return;
        }

        if (entryCount === 0) return;

        setGlobalTranslating(true);
        setGlobalTranslateProgress({ done: 0, total: entryCount });
        try {
            const start = await StartTranslateAllCurrentMemoryEntries({
                target: globalTranslateTarget,
                language: globalTranslateCustomLanguage.trim(),
            });
            setGlobalTranslateProgress({ done: 0, total: start.total });
            onOpenChange(false);
            notifyToast(
                t('toasts.globalTranslateStarted.title') as string,
                t('toasts.globalTranslateStarted.body', {
                    total: start.total,
                    target: start.targetLanguage || globalTranslateTargetLabel,
                }) as string,
                'info',
            );
        } catch (err: unknown) {
            setGlobalTranslating(false);
            setGlobalTranslateProgress(null);
            notifyToast(
                t('toasts.translateFailed.title') as string,
                err instanceof Error ? err.message : String(err),
                'error',
            );
        }
    }, [
        entryCount,
        globalTranslateCustomLanguage,
        globalTranslateTarget,
        globalTranslateTargetLabel,
        globalTranslating,
        onOpenChange,
        setGlobalTranslating,
        t,
    ]);

    return (
        <Dialog
            open={open}
            onOpenChange={(open) => {
                if (!globalTranslating) onOpenChange(open);
            }}
        >
            <DialogContent className="w-[460px] max-w-[90vw]">
                <DialogHeader>
                    <div>
                        <DialogTitle>{t('globalTranslate.title')}</DialogTitle>
                        <DialogDescription className="mt-1">
                            {t('globalTranslate.description')}
                        </DialogDescription>
                    </div>
                    <DialogCloseButton />
                </DialogHeader>
                <div className="px-6 py-5 space-y-5">
                    <div>
                        <p className="text-[11px] font-medium text-muted uppercase tracking-wide mb-2">
                            {t('globalTranslate.targetLabel')}
                        </p>
                        <div role="radiogroup" aria-label={t('globalTranslate.targetLabel')} className="grid grid-cols-2 gap-2">
                            {(['follow-ui', 'zh-CN', 'en', 'other'] as GlobalTranslateTarget[]).map((target) => {
                                const selectedTarget = globalTranslateTarget === target;
                                const label = target === 'follow-ui'
                                    ? t('globalTranslate.targets.currentWithLanguage', { language: uiLanguageLabel(currentUILanguage) }) as string
                                    : t(`globalTranslate.targets.${target}` as never) as string;
                                return (
                                    <button
                                        key={target}
                                        type="button"
                                        role="radio"
                                        aria-checked={selectedTarget}
                                        className={cn(
                                            'h-10 rounded-md border px-3 text-left text-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus',
                                            selectedTarget
                                                ? 'border-border-strong bg-surface-active text-ink'
                                                : 'border-border bg-panel text-muted hover:bg-surface-hover hover:text-ink',
                                        )}
                                        onClick={() => {
                                            setGlobalTranslateTarget(target);
                                        }}
                                        disabled={globalTranslating}
                                    >
                                        {label}
                                    </button>
                                );
                            })}
                        </div>
                        {globalTranslateTarget === 'other' && (
                            <input
                                type="text"
                                className="mt-3 w-full px-3 py-1.5 text-xs rounded-md bg-panel border border-border text-ink placeholder:text-muted focus:outline-none focus:border-border-focus"
                                value={globalTranslateCustomLanguage}
                                onChange={(e) => setGlobalTranslateCustomLanguage(e.target.value)}
                                placeholder={t('globalTranslate.customPlaceholder') as string}
                                disabled={globalTranslating}
                            />
                        )}
                        <p className="mt-2 text-[11px] leading-relaxed text-muted">
                            {t('globalTranslate.targetNote', { target: globalTranslateTargetLabel })}
                        </p>
                    </div>

                    <div>
                        <p className="text-[11px] font-medium text-muted uppercase tracking-wide mb-2">
                            {t('globalTranslate.scopeLabel')}
                        </p>
                        <div className="rounded-md border border-border-strong bg-surface-active px-3 py-2">
                            <span className="block text-xs font-medium text-ink">
                                {t('globalTranslate.scopes.currentEntries.label', { count: entryCount }) as string}
                            </span>
                            <span className="block mt-0.5 text-[11px] leading-relaxed text-muted">
                                {t('globalTranslate.scopes.currentEntries.description') as string}
                            </span>
                        </div>
                    </div>

                    {globalTranslateProgress && (
                        <div className="rounded-md border border-border bg-panel-warm px-3 py-2">
                            <div className="flex items-center justify-between text-[11px] text-muted mb-1.5">
                                <span>{t('globalTranslate.progress')}</span>
                                <span>{globalTranslateProgress.done}/{globalTranslateProgress.total}</span>
                            </div>
                            <div className="h-1.5 overflow-hidden rounded-full bg-border">
                                <div
                                    className="h-full rounded-full bg-accent-2 transition-[width]"
                                    style={{
                                        width: `${Math.round((globalTranslateProgress.done / Math.max(globalTranslateProgress.total, 1)) * 100)}%`,
                                    }}
                                />
                            </div>
                        </div>
                    )}

                    <div className="flex items-center justify-end gap-2 pt-1">
                        <Button size="sm" variant="outline" onClick={() => onOpenChange(false)} disabled={globalTranslating}>
                            {t('globalTranslate.cancel')}
                        </Button>
                        <Button
                            size="sm"
                            aria-busy={globalTranslating}
                            onClick={() => void handleGlobalTranslate()}
                            disabled={
                                globalTranslating ||
                                entryCount === 0 ||
                                (globalTranslateTarget === 'other' && globalTranslateCustomLanguage.trim() === '')
                            }
                        >
                            <Languages className={cn('h-3 w-3', globalTranslating && 'motion-safe:animate-pulse')} />
                            {globalTranslating ? t('globalTranslate.translating') : t('globalTranslate.confirm')}
                        </Button>
                    </div>
                </div>
            </DialogContent>
        </Dialog>
    );
}
