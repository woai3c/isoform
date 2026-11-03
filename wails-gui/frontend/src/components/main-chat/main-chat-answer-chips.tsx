import { useState } from 'react';
import { CHAT_FAILURE_LINK_CLASS } from '@/components/chat-failure-line';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { useMainChatStreamView } from './main-chat-view-context';

/** Up to two answers under Isoform's question (RC 2 C2). A chip sends at
 *  once (Chat K4) and folds to "You answered: … · Change". */
export function MainChatAnswerChips({ itemId }: { itemId: string }) {
    const { t } = useTranslation('chat');
    const view = useMainChatStreamView();
    const [answered, setAnswered] = useState<string | null>(null);
    const chips = view?.chips[itemId];
    if (!view?.onFillChip || !chips?.length) return null;
    const answer = view.onFillChip;
    // Chat K4: an answer posts at once, and the chips fold to what was said.
    if (answered !== null) {
        return (
            <p role="status" className="m-0 text-[12.5px] text-muted" data-main-chat-answered={itemId}>
                <span>{t('mainChat.answered', { answer: answered })}</span>
                <span aria-hidden="true">{t('errors.cause.separator')}</span>
                <button type="button" className={CHAT_FAILURE_LINK_CLASS} onClick={() => setAnswered(null)}>{t('mainChat.change')}</button>
            </p>
        );
    }
    return (
        <div role="group" aria-label={t('mainChat.view.chipsLabel')} className="flex min-w-0 flex-wrap gap-1.5" data-main-chat-chips={itemId}>
            {chips.slice(0, 2).map((chip, index) => (
                <Button
                    key={`${index}:${chip}`}
                    variant="outline"
                    size="sm"
                    className="h-auto min-h-8 min-w-0 max-w-full whitespace-normal [overflow-wrap:anywhere] leading-snug py-1.5 text-left text-[12.5px] font-medium text-ink"
                    onClick={() => {
                        if (view.chipsSend) setAnswered(chip);
                        try {
                            const result = answer(chip);
                            if (result === false) setAnswered(null);
                            else if (result instanceof Promise) void result.then((sent) => {
                                if (!sent) setAnswered(null);
                            }, () => setAnswered(null));
                        } catch {
                            setAnswered(null);
                        }
                    }}
                >
                    {chip}
                </Button>
            ))}
        </div>
    );
}
