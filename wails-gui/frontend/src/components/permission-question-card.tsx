import { useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, MessageCircleQuestion, Send } from 'lucide-react';
import { ChatFailureLine } from '@/components/chat-failure-line';
import { cn } from '@/lib/utils';
import type { PermissionOption } from '@/types';

interface PermissionQuestionCardProps {
    question: string;
    options: PermissionOption[];
    /** Resolves false when the answer didn't go through (Chat K3). */
    onSelect: (optionId: string) => unknown;
    onSkip: () => void;
    wideMode?: boolean;
}

export function PermissionQuestionCard({
    question,
    options,
    onSelect,
    onSkip,
    wideMode = false,
}: PermissionQuestionCardProps) {
    const { t } = useTranslation('chat');
    const [responding, setResponding] = useState(false);
    const [chosen, setChosen] = useState<string | null>(null);
    const [failed, setFailed] = useState(false);
    const [showOtherInput, setShowOtherInput] = useState(false);
    const [otherText, setOtherText] = useState('');
    const inputRef = useRef<HTMLInputElement>(null);

    // Separate the "Other" option from regular options.
    const regularOptions = options.filter((o) => !o.id.endsWith('_other'));
    const otherOption = options.find((o) => o.id.endsWith('_other'));

    // Chat K3: a ring on the chosen button while it goes, the others dimmed;
    // an answer that didn't go through says so under the buttons.
    const send = async (optionId: string): Promise<boolean> => {
        setResponding(true);
        setChosen(optionId);
        setFailed(false);
        let ok = true;
        try {
            ok = (await onSelect(optionId)) !== false;
        } catch {
            ok = false;
        }
        if (!ok) {
            setResponding(false);
            setFailed(true);
        }
        return ok;
    };

    const handleSelect = (optionId: string) => {
        if (responding) return;
        void send(optionId);
    };

    const handleSkip = () => {
        if (responding) return;
        setResponding(true);
        onSkip();
    };

    const handleOtherClick = () => {
        if (responding) return;
        setShowOtherInput(true);
        // Focus the input after React renders it.
        requestAnimationFrame(() => inputRef.current?.focus());
    };

    const handleOtherSubmit = () => {
        if (responding || !otherOption) return;
        const text = otherText.trim();
        if (!text) return;
        // Encode the free-text into the optionId: "q0_other:user text"
        void send(`${otherOption.id}:${text}`);
    };

    const handleOtherKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === 'Enter') {
            e.preventDefault();
            handleOtherSubmit();
        } else if (e.key === 'Escape') {
            setShowOtherInput(false);
            setOtherText('');
        }
    };

    return (
        <div className={`mx-6 mb-4 self-center w-full rounded-xl border border-accent-2/30 bg-panel overflow-hidden${wideMode ? "" : " max-w-3xl"}`}>
            <div className="px-4 py-2.5 border-b border-accent-2/20 flex items-center gap-2">
                <MessageCircleQuestion className="size-3.5 text-accent-2" />
                <span className="text-xs font-semibold text-accent-2">
                    {t('permissionQuestion.title')}
                </span>
            </div>

            <div className="px-4 py-3">
                <p className="text-sm text-ink mb-3">{question}</p>
                <div className="flex flex-wrap items-center gap-2">
                    {regularOptions.map((opt) => {
                        const isReject = opt.kind === 'reject_once' || opt.kind === 'reject_always';
                        return (
                            <button
                                key={opt.id}
                                className={cn(
                                    'px-3 py-1.5 rounded-lg text-xs font-semibold transition-colors cursor-pointer',
                                    isReject
                                        ? 'border border-border text-muted hover:text-ink hover:bg-ink/10'
                                        : 'patina-verdigris bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] hover:opacity-90',
                                    responding && 'cursor-not-allowed',
                                    responding && chosen !== opt.id && 'opacity-60',
                                    'inline-flex items-center gap-1.5',
                                )}
                                disabled={responding}
                                aria-busy={responding && chosen === opt.id}
                                data-chosen={chosen === opt.id || undefined}
                                onClick={() => handleSelect(opt.id)}
                            >
                                {responding && chosen === opt.id && <Loader2 aria-hidden className="size-3 animate-spin" data-testid="answer-ring" />}
                                {opt.name}
                            </button>
                        );
                    })}
                    {otherOption && !showOtherInput && (
                        <button
                            className={cn(
                                'px-3 py-1.5 rounded-lg text-xs font-semibold transition-colors cursor-pointer',
                                'border border-accent-2/30 text-accent-2 hover:bg-accent-2/10',
                                responding && 'opacity-60 cursor-not-allowed',
                            )}
                            disabled={responding}
                            onClick={handleOtherClick}
                        >
                            {t('permissionQuestion.other')}
                        </button>
                    )}
                    <button
                        className={cn(
                            'px-3 py-1.5 rounded-lg text-xs font-semibold transition-colors cursor-pointer',
                            'text-muted hover:text-ink hover:bg-ink/10',
                            responding && 'opacity-60 cursor-not-allowed',
                        )}
                        disabled={responding}
                        onClick={handleSkip}
                    >
                        {t('permissionQuestion.skip')}
                    </button>
                </div>
                {showOtherInput && (
                    <div className="mt-2 flex items-center gap-2">
                        <input
                            ref={inputRef}
                            type="text"
                            value={otherText}
                            onChange={(e) => setOtherText(e.target.value)}
                            onKeyDown={handleOtherKeyDown}
                            placeholder={t('permissionQuestion.placeholder')}
                            disabled={responding}
                            className={cn(
                                'flex-1 rounded-lg border border-accent-2/30 bg-panel px-3 py-1.5 text-xs text-ink',
                                'placeholder:text-muted/60 outline-none transition-colors',
                                'focus:border-accent-2/60',
                                responding && 'opacity-60 cursor-not-allowed',
                            )}
                        />
                        <button
                            aria-label={t('permissionQuestion.send')}
                            onClick={handleOtherSubmit}
                            disabled={responding || !otherText.trim()}
                            className={cn(
                                'p-1.5 rounded-lg transition-colors cursor-pointer',
                                'text-accent-2 hover:bg-accent-2/10',
                                (responding || !otherText.trim()) && 'opacity-40 cursor-not-allowed',
                            )}
                        >
                            <Send className="size-3.5" />
                        </button>
                    </div>
                )}
                {failed && chosen && (
                    <ChatFailureLine testId="answer" text={t('failure.answer', { ns: 'chat' })} onRetry={() => send(chosen)} />
                )}
            </div>
        </div>
    );
}
