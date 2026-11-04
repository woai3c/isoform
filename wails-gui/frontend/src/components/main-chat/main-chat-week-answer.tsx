import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { GetTodayBriefByID } from '@/lib/wails-shim-app';
import { PageEventsOn as EventsOn } from '@/lib/page-queries';
import { MorningWeekQuestion } from '@/components/today/MorningWeekQuestion';

/** The answer keeps the explicit Today question that produced this row. */
export function MainChatWeekAnswer({ briefId, answer }: { briefId: string; answer: string }) {
    const { t } = useTranslation('chat');
    const [current, setCurrent] = useState(answer);
    useEffect(() => {
        let live = true;
        const refresh = () => void GetTodayBriefByID(briefId).then((view) => {
            if (live && view.brief?.briefId === briefId && view.weekAnswer?.answer) setCurrent(view.weekAnswer.answer);
        }).catch(() => {});
        refresh();
        const off = EventsOn('brief:stored', refresh);
        return () => { live = false; off(); };
    }, [briefId]);
    return <div data-main-chat-week-answer className="flex flex-col gap-1.5">
        <span className="text-[12px] text-accent">{t('mainChat.view.isoform')}</span>
        <MorningWeekQuestion briefID={briefId} initialAnswer={current} variant="chat" />
    </div>;
}
