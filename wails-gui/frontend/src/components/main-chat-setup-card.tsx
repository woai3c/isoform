import { useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { Check, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { cn } from '@/lib/utils';
import { getSetupRecord } from '@/lib/stream-helpers';
import type { MainChatSetupStep } from '@/lib/wails-shim-app';
import type { SaveNameResult } from '@/hooks/useMainChatSetup';
import type { StreamItem } from '@/types';

// One ask at a time, in the main chat: who it is for, what it unlocks in one
// sentence, Turn on and Not now. The first ask is what to call the user, a
// field prefilled from the Mac account. Answered asks become SetupRecordLine
// items.

/** The permission keys the cards have words for. */
export const SETUP_PERMISSION_KEYS = [
    'screen_recording',
    'accessibility',
    'microphone',
    'system_audio',
    'calendars',
    'notifications',
    'contacts',
] as const;
type SetupPermissionKey = (typeof SETUP_PERMISSION_KEYS)[number];

function isKnownPermission(key: string): key is SetupPermissionKey {
    return (SETUP_PERMISSION_KEYS as readonly string[]).includes(key);
}

export interface MainChatSetupCardProps {
    step: MainChatSetupStep;
    position: number;
    total: number;
    busy: boolean;
    waiting: boolean;
    /** The assistant picker shown inside the assistant step. */
    assistantPicker?: ReactNode;
    onGrant: () => void;
    onNotNow: () => void;
    onOpenSettings: () => void;
    /** Saves the name the user typed (the name step); `replace` writes over a stored name. */
    onSaveName?: (name: string, replace?: boolean) => Promise<SaveNameResult> | void;
}

const cardClass = 'animate-rise-in rounded-2xl border border-border bg-background p-4 flex flex-col gap-2';
const kickerClass = 'text-[10px] font-semibold uppercase tracking-wider text-muted';

export function MainChatSetupCard({
    step,
    position,
    total,
    busy,
    waiting,
    assistantPicker,
    onGrant,
    onNotNow,
    onOpenSettings,
    onSaveName,
}: MainChatSetupCardProps) {
    const { t } = useTranslation('chat');
    const kicker = (
        <div className={kickerClass}>
            {t('setup.kicker')}
            {total > 0 ? ` \u00b7 ${t('setup.progress', { done: position, total })}` : ''}
        </div>
    );

    if (step.kind === 'name') {
        return (
            <NameCard
                // A new suggestion (a name stored meanwhile) starts the field over.
                key={step.suggestion ?? ''}
                kicker={kicker}
                suggestion={step.suggestion ?? ''}
                busy={busy}
                onSave={(name, replace) => onSaveName?.(name, replace)}
                onNotNow={onNotNow}
            />
        );
    }

    if (step.kind === 'first_task') return null;

    const isAssistant = step.kind === 'assistant';
    // The permission's name, as onboarding and System Settings call it.
    const name = isKnownPermission(step.key) ? t(`setup.names.${step.key}`) : step.key;
    const title = isAssistant
        ? t('setup.assistant.title')
        : isKnownPermission(step.key)
          ? t(`setup.permissions.${step.key}.title`)
          : step.key;
    const body = isAssistant
        ? t('setup.assistant.body')
        : isKnownPermission(step.key)
          ? t(`setup.permissions.${step.key}.body`)
          : '';

    return (
        <div data-setup-card={step.key} className={cardClass}>
            {kicker}
            <div className="text-[15px] font-semibold text-ink">{title}</div>
            {body ? <p className="text-[13px] leading-relaxed text-muted">{body}</p> : null}
            {isAssistant && assistantPicker ? <div className="pt-1">{assistantPicker}</div> : null}
            {waiting ? (
                <p data-setup-waiting className="text-[12px] text-muted">
                    {isAssistant ? t('setup.assistant.waiting') : t('setup.waitingSettings', { name })}
                </p>
            ) : null}
            <div className="flex items-center gap-2 pt-1">
                {isAssistant ? (
                    assistantPicker ? null : (
                        <Button size="sm" onClick={onOpenSettings}>
                            {t('setup.assistant.openSettings')}
                        </Button>
                    )
                ) : waiting ? (
                    <>
                        <Button size="sm" data-setup-retry disabled={busy} onClick={onGrant}>
                            {busy ? <Loader2 className="size-3.5 animate-spin" /> : null}
                            {t('setup.tryAgain')}
                        </Button>
                        <Button variant="ghost" size="sm" onClick={onOpenSettings}>
                            {t('setup.openSettings')}
                        </Button>
                    </>
                ) : (
                    <Button size="sm" data-setup-grant disabled={busy} onClick={onGrant}>
                        {busy ? <Loader2 className="size-3.5 animate-spin" /> : null}
                        {busy ? t('setup.requesting') : t('setup.grant', { name })}
                    </Button>
                )}
                <Button variant="ghost" size="sm" data-setup-not-now disabled={busy} onClick={onNotNow}>
                    {t('setup.notNow')}
                </Button>
            </div>
        </div>
    );
}

/** What should I call you: one field, prefilled, Save and Not now. */
function NameCard({
    kicker,
    suggestion,
    busy,
    onSave,
    onNotNow,
}: {
    kicker: ReactNode;
    suggestion: string;
    busy: boolean;
    onSave: (name: string, replace?: boolean) => Promise<SaveNameResult> | void;
    onNotNow: () => void;
}) {
    const { t } = useTranslation('chat');
    const [name, setName] = useState(suggestion);
    const [failed, setFailed] = useState(false);
    // Another name was stored while the card was open: which one to keep.
    const [stored, setStored] = useState<string | null>(null);
    const trimmed = name.trim();
    const save = (value: string, replace = false) => {
        setFailed(false);
        void Promise.resolve(onSave(value, replace)).then((result) => {
            if (!result) return;
            if (result.kind === 'failed') setFailed(true);
            setStored(result.kind === 'stored' ? result.name : null);
        });
    };
    return (
        <form
            data-setup-card="name"
            className={cardClass}
            onSubmit={(event) => {
                event.preventDefault();
                if (!trimmed || busy || stored) return;
                save(trimmed);
            }}
        >
            {kicker}
            <label htmlFor="main-chat-setup-name" className="text-[15px] font-semibold text-ink">
                {t('setup.name.title')}
            </label>
            <p className="text-[13px] leading-relaxed text-muted">{t('setup.name.body')}</p>
            <input
                id="main-chat-setup-name"
                data-setup-name-input
                type="text"
                maxLength={80}
                autoComplete="off"
                value={name}
                placeholder={t('setup.name.placeholder')}
                onChange={(event) => setName(event.target.value)}
                className="mt-1 h-9 w-full max-w-sm rounded-lg border border-border bg-background px-3 text-[13px] text-ink outline-none transition-colors focus-visible:border-accent-2/60 focus-visible:ring-2 focus-visible:ring-accent-2/30"
            />
            {failed ? (
                <p data-setup-name-error className="text-[12px] text-error">
                    {t('setup.name.saveError')}
                </p>
            ) : null}
            {stored ? (
                <>
                    <p data-setup-name-stored className="text-[13px] leading-relaxed text-muted">
                        {t('setup.name.alreadyStored', { name: stored })}
                    </p>
                    <div className="flex flex-wrap items-center gap-2 pt-1">
                        <Button type="button" size="sm" data-setup-use-typed disabled={busy || !trimmed} onClick={() => save(trimmed, true)}>
                            {t('setup.name.useTyped', { name: trimmed })}
                        </Button>
                        <Button type="button" variant="ghost" size="sm" data-setup-keep-stored disabled={busy} onClick={() => save(stored)}>
                            {t('setup.name.keepStored', { name: stored })}
                        </Button>
                    </div>
                </>
            ) : (
                <div className="flex items-center gap-2 pt-1">
                    <Button type="submit" size="sm" data-setup-save-name disabled={busy || !trimmed}>
                        {busy ? <Loader2 className="size-3.5 animate-spin" /> : null}
                        {t('setup.name.save')}
                    </Button>
                    <Button type="button" variant="ghost" size="sm" data-setup-not-now disabled={busy} onClick={onNotNow}>
                        {t('setup.notNow')}
                    </Button>
                </div>
            )}
        </form>
    );
}

/** The quiet line an answered ask leaves in the main chat. */
export function SetupRecordLine({ item }: { item: StreamItem }) {
    const { t } = useTranslation('chat');
    const record = getSetupRecord(item);
    if (!record) return null;
    let text: string;
    if (record.step === 'first_task') {
        text = t('setup.record.done');
    } else if (record.step === 'name') {
        text =
            record.answer === 'granted' && record.name
                ? t('setup.record.name', { name: record.name })
                : t('setup.record.nameNotNow');
    } else if (record.step === 'assistant') {
        text = record.answer === 'granted' ? t('setup.record.assistantReady') : t('setup.record.assistantNotNow');
    } else {
        const name = isKnownPermission(record.step) ? t(`setup.names.${record.step}`) : record.step;
        text = record.answer === 'granted' ? t('setup.record.granted', { name }) : t('setup.record.notNow', { name });
    }
    return (
        <div data-setup-record={record.step} className="flex items-center justify-center gap-1.5 text-[12px] text-muted">
            <Check className={cn('size-3', record.answer === 'granted' ? 'text-accent' : 'text-muted')} />
            <span>{text}</span>
        </div>
    );
}
