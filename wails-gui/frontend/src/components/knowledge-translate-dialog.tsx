import { useTranslation } from 'react-i18next';
import { Languages } from 'lucide-react';
import {
    Dialog,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogDescription,
    DialogCloseButton,
} from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

export type GlobalTranslateTarget = 'follow-ui' | 'zh-CN' | 'en' | 'other';

/**
 * Translates every live know-how entry in one pass. Reached from the
 * How you do things page's More menu.
 */
export function KnowledgeTranslateDialog({
    open,
    onOpenChange,
    target,
    customLanguage,
    entriesCount,
    translating,
    error,
    onTargetChange,
    onCustomLanguageChange,
    onSubmit,
}: {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    target: GlobalTranslateTarget;
    customLanguage: string;
    entriesCount: number;
    translating: boolean;
    /** Why the last start failed, shown in the dialog. */
    error?: string | null;
    onTargetChange: (target: GlobalTranslateTarget) => void;
    onCustomLanguageChange: (language: string) => void;
    onSubmit: () => void;
}) {
    const { t, i18n } = useTranslation('knowledge');
    const currentLanguage = normalizeSupportedUILanguage(i18n.resolvedLanguage || i18n.language);
    const currentLanguageLabel = t(`translate.global.languageNames.${currentLanguage}`);
    const targetOptions: Array<{ value: GlobalTranslateTarget; label: string; description: string }> = [
        {
            value: 'follow-ui',
            label: t('translate.global.targets.ui', { language: currentLanguageLabel }),
            description: t('translate.global.targetDescriptions.ui'),
        },
        {
            value: 'zh-CN',
            label: t('translate.global.targets.zh'),
            description: t('translate.global.targetDescriptions.zh'),
        },
        {
            value: 'en',
            label: t('translate.global.targets.en'),
            description: t('translate.global.targetDescriptions.en'),
        },
        {
            value: 'other',
            label: t('translate.global.targets.custom'),
            description: t('translate.global.targetDescriptions.custom'),
        },
    ];
    const submitDisabled = translating || entriesCount === 0 || (target === 'other' && customLanguage.trim() === '');

    return (
        <Dialog
            open={open}
            onOpenChange={(nextOpen) => {
                if (!translating) onOpenChange(nextOpen);
            }}
        >
            <DialogContent className="w-[460px] max-w-[92vw]">
                <DialogHeader>
                    <div className="min-w-0">
                        <DialogTitle className="text-sm">{t('translate.global.title')}</DialogTitle>
                        <DialogDescription className="mt-1 text-xs">
                            {t('translate.global.description')}
                        </DialogDescription>
                    </div>
                    <DialogCloseButton />
                </DialogHeader>

                <div className="px-6 py-5 space-y-5">
                    <div>
                        <p className="text-xs font-medium text-muted mb-2">
                            {t('translate.global.targetLabel')}
                        </p>
                        <div className="grid grid-cols-2 gap-2">
                            {targetOptions.map((option) => (
                                <button
                                    key={option.value}
                                    type="button"
                                    aria-pressed={target === option.value}
                                    className={cn(
                                        'rounded-lg border px-3 py-2 text-left transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-2/40',
                                        target === option.value
                                            ? 'border-border-strong bg-active-row-bg text-ink'
                                            : 'border-border bg-panel text-muted hover:text-ink hover:bg-surface-hover',
                                    )}
                                    onClick={() => onTargetChange(option.value)}
                                    disabled={translating}
                                >
                                    <span className="block text-xs font-medium">{option.label}</span>
                                    <span className="mt-1 block text-[10px] leading-relaxed text-muted">
                                        {option.description}
                                    </span>
                                </button>
                            ))}
                        </div>
                        {target === 'other' && (
                            <div className="mt-3 space-y-1.5">
                                <input
                                    type="text"
                                    className="w-full px-3 py-1.5 text-xs rounded-md bg-background border border-border text-ink placeholder:text-muted focus:outline-none focus:border-border-focus"
                                    value={customLanguage}
                                    onChange={(e) => onCustomLanguageChange(e.target.value)}
                                    placeholder={t('translate.global.customPlaceholder')}
                                    disabled={translating}
                                />
                                <p className="text-[10px] leading-relaxed text-muted">
                                    {t('translate.global.customHint')}
                                </p>
                            </div>
                        )}
                    </div>

                    <div>
                        <p className="text-xs font-medium text-muted mb-2">
                            {t('translate.global.scopeLabel')}
                        </p>
                        <div className="rounded-lg border border-border-strong bg-active-row-bg px-3 py-2 text-left">
                            <span className="block text-xs font-medium text-ink">
                                {t('translate.global.scopes.entries')}
                            </span>
                            <span className="mt-1 block text-[10px] leading-relaxed text-muted">
                                {t('translate.global.scopeDescriptions.entries', { count: entriesCount })}
                            </span>
                        </div>
                    </div>

                    {error && (
                        <p role="alert" className="m-0 rounded-lg border border-error-border bg-error-bg px-3 py-2 text-xs text-error">
                            {error}
                        </p>
                    )}
                </div>

                <div className="flex items-center justify-between gap-3 px-6 py-4 border-t border-border">
                    <span className="text-[11px] text-muted">
                        {t('translate.global.entriesCount', { count: entriesCount })}
                    </span>
                    <div className="flex items-center gap-2">
                        <Button variant="outline" size="sm" onClick={() => onOpenChange(false)} disabled={translating}>
                            {t('translate.global.cancel')}
                        </Button>
                        <Button size="sm" onClick={onSubmit} disabled={submitDisabled} aria-busy={translating}>
                            <Languages className={cn('size-3.5', translating && 'animate-pulse')} />
                            {translating ? t('translate.global.translating') : t('translate.global.submit')}
                        </Button>
                    </div>
                </div>
            </DialogContent>
        </Dialog>
    );
}

function normalizeSupportedUILanguage(language: string): 'en' | 'zh-CN' {
    return language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en';
}
