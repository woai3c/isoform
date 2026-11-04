import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2 } from 'lucide-react';
import {
    Dialog,
    DialogCloseButton,
    DialogContent,
    DialogDescription,
    DialogHeader,
    DialogTitle,
} from '@/components/ui/dialog';
import type { PrincipalDTO } from '@/lib/wails-shim-app';
import { PrincipalPicker } from './contacts/principal-picker';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';

// Preset reasons let users describe attribution corrections without typing.
// Rejection reasons stay transient; reassignment retains its own provenance.
//
// `reassignToContact` is a special case: it doesn't delete the memory but
// reattributes it to the named principal. The dialog reveals a picker when
// this preset is selected and routes submission through onReassign instead
// of onReject.
const REJECT_PRESET_KEYS = [
    'reassignToContact',
    'aboutOtherPerson',
    'observedMedia',
    'chatMisattribution',
    'never_me',
] as const;
type RejectPresetKey = (typeof REJECT_PRESET_KEYS)[number];

interface RejectAttributionDialogProps {
    target: { id: string; title: string } | null;
    onOpenChange: (open: boolean) => void;
    onReject: (reason: string) => Promise<void>;
    onReassign: (principal: PrincipalDTO, reason: string) => Promise<void>;
}

export function RejectAttributionDialog({
    target,
    onOpenChange,
    onReject,
    onReassign,
}: RejectAttributionDialogProps) {
    const { t } = useTranslation('memory');
    const [preset, setPreset] = useState<RejectPresetKey | 'custom'>('reassignToContact');
    const [customReason, setCustomReason] = useState('');
    const [reassignTarget, setReassignTarget] = useState<PrincipalDTO | null>(null);
    const [submitting, setSubmitting] = useState(false);
    const [reassignFailed, setReassignFailed] = useState<string | null>(null);

    // Reset state whenever the dialog opens against a new target so
    // a previously-typed reason doesn't leak into the next rejection.
    useEffect(() => {
        if (target) {
            setPreset('reassignToContact');
            setCustomReason('');
            setReassignTarget(null);
            setSubmitting(false);
            setReassignFailed(null);
        }
    }, [target?.id]);

    const open = target !== null;
    const isReassign = preset === 'reassignToContact';

    const resolveReason = useCallback((): string => {
        if (preset === 'custom') {
            return customReason.trim();
        }
        const presetText = t(`rejectAttribution.presets.${preset}`) as string;
        if (customReason.trim()) {
            return `${presetText} — ${customReason.trim()}`;
        }
        return presetText;
    }, [preset, customReason, t]);

    const handleSubmit = useCallback(async () => {
        const reason = resolveReason();
        if (!reason) return;
        setSubmitting(true);
        setReassignFailed(null);
        try {
            if (isReassign) {
                if (!reassignTarget) {
                    setSubmitting(false);
                    return;
                }
                await onReassign(reassignTarget, reason);
            } else {
                await onReject(reason);
            }
        } catch {
            if (isReassign && reassignTarget) setReassignFailed(reassignTarget.displayName);
            setSubmitting(false);
        }
    }, [isReassign, onReassign, onReject, reassignTarget, resolveReason]);

    const submitDisabled =
        submitting ||
        (preset === 'custom' && !customReason.trim()) ||
        (isReassign && !reassignTarget);

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="w-[560px] max-h-[80vh]">
                <DialogHeader>
                    <div>
                        <DialogTitle>{t('rejectAttribution.title')}</DialogTitle>
                        <DialogDescription className="mt-1">
                            {t('rejectAttribution.description', { title: target?.title ?? '' })}
                        </DialogDescription>
                    </div>
                    <DialogCloseButton />
                </DialogHeader>
                <div className="px-6 py-4 flex flex-col gap-4 overflow-y-auto">
                    <div className="flex flex-col gap-2">
                        <p className="text-[12px] font-medium uppercase tracking-wide text-muted">
                            {t('rejectAttribution.reasonLabel')}
                        </p>
                        <div className="flex flex-col gap-1.5">
                            {REJECT_PRESET_KEYS.map((key) => (
                                <label
                                    key={key}
                                    className={cn(
                                        'flex items-start gap-2 cursor-pointer rounded-lg border px-3 py-2 transition-colors',
                                        preset === key
                                            ? 'border-border-strong bg-surface-active'
                                            : 'border-border hover:bg-surface-hover',
                                    )}
                                >
                                    <input
                                        type="radio"
                                        name="reject-preset"
                                        value={key}
                                        checked={preset === key}
                                        onChange={() => setPreset(key)}
                                        className="mt-1 accent-accent-2"
                                    />
                                    <span className="text-[13px] text-ink leading-snug">
                                        {t(`rejectAttribution.presets.${key}`)}
                                    </span>
                                </label>
                            ))}
                            <label
                                className={cn(
                                    'flex items-start gap-2 cursor-pointer rounded-lg border px-3 py-2 transition-colors',
                                    preset === 'custom'
                                        ? 'border-border-strong bg-surface-active'
                                        : 'border-border hover:bg-surface-hover',
                                )}
                            >
                                <input
                                    type="radio"
                                    name="reject-preset"
                                    value="custom"
                                    checked={preset === 'custom'}
                                    onChange={() => setPreset('custom')}
                                    className="mt-1 accent-accent-2"
                                />
                                <span className="text-[13px] text-ink leading-snug">
                                    {t('rejectAttribution.presets.custom')}
                                </span>
                            </label>
                        </div>
                    </div>

                    {isReassign && (
                        <div className="flex flex-col gap-1.5">
                            <label className="text-[12px] font-medium uppercase tracking-wide text-muted">
                                {t('rejectAttribution.reassignPickerLabel')}
                            </label>
                            <PrincipalPicker
                                selectedID={reassignTarget?.id ?? null}
                                onSelect={setReassignTarget}
                                autoFocusSearch={false}
                            />
                        </div>
                    )}

                    <div className="flex flex-col gap-1.5">
                        <label className="text-[12px] font-medium uppercase tracking-wide text-muted">
                            {preset === 'custom'
                                ? t('rejectAttribution.customLabel')
                                : t('rejectAttribution.additionalLabel')}
                        </label>
                        <textarea
                            value={customReason}
                            onChange={(e) => setCustomReason(e.target.value)}
                            placeholder={
                                preset === 'custom'
                                    ? (t('rejectAttribution.customPlaceholder') as string)
                                    : (t('rejectAttribution.additionalPlaceholder') as string)
                            }
                            rows={3}
                            className="rounded-lg border border-border bg-panel px-3 py-2 text-[13px] text-ink outline-none resize-none focus-visible:border-border-focus"
                        />
                    </div>
                </div>
                {reassignFailed !== null && (
                    <p role="alert" className="m-0 flex flex-wrap items-center gap-2 px-6 pb-3 text-[12.5px] text-copper">
                        <span>{t('rejectAttribution.reassignFailed', { name: reassignFailed })}</span>
                        <button type="button" className="text-accent hover:underline" disabled={submitting} onClick={() => void handleSubmit()}>{t('aboutYou.retry')}</button>
                    </p>
                )}
                <div className="flex items-center justify-end gap-2 px-6 pb-5">
                    <Button size="sm" variant="outline" onClick={() => onOpenChange(false)} disabled={submitting}>
                        {t('rejectAttribution.cancel')}
                    </Button>
                    <Button
                        size="sm"
                        variant={isReassign ? 'default' : 'destructive'}
                        onClick={handleSubmit}
                        disabled={submitDisabled}
                        aria-busy={submitting}
                    >
                        {submitting && <Loader2 className="h-3 w-3 motion-safe:animate-spin" />}
                        {isReassign
                            ? t('rejectAttribution.reassignSubmit')
                            : t('rejectAttribution.submit')}
                    </Button>
                </div>
            </DialogContent>
        </Dialog>
    );
}
