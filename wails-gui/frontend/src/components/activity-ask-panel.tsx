import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { todayDraftContext, todayDraftText } from "@/lib/today-draft-preparation";
import { prepareTodayChatDraftNow } from "@/lib/today-chat-prepared";
import type { TodayChatContextRef } from "@/lib/today-chat-navigation";
import { MainChatPanel } from "@/components/today/MainChatPanel";
import type { TodayActionHandlers } from "@/components/today/types";

export interface ActivityAskRequest {
    id: number;
    text: string;
    context?: TodayChatContextRef;
}

/** Keep Activity visible while the same main chat answers beside it. */
export function ActivityAskPanel({ request, handlers, onClose }: {
    request: ActivityAskRequest;
    handlers: TodayActionHandlers;
    onClose: () => void;
}) {
    const { t, i18n } = useTranslation("today");
    const [draft, setDraft] = useState("");
    const [prepared, setPrepared] = useState(false);
    const [ask, setAsk] = useState<ActivityAskRequest | null>(null);
    const [failed, setFailed] = useState(false);
    const [retry, setRetry] = useState(0);
    useEffect(() => {
        let current = true;
        setAsk(null);
        setFailed(false);
        if (!request.context) {
            setAsk(request);
        } else {
            void prepareTodayChatDraftNow(request.context).then((result) => {
                if (!current) return;
                if (result.status !== "ready") {
                    setFailed(true);
                    return;
                }
                if (request.context?.sourceKind === "brief_card" && request.context.generatedMessage?.trim() !== result.draft.message?.trim()) {
                    setFailed(true);
                    return;
                }
                const context = todayDraftContext(request.context!, result.draft);
                if (request.context?.sourceKind === "brief_card") context.generatedMessage = request.context.generatedMessage;
                const text = request.context?.sourceKind === "summary" || request.context?.sourceKind === "summary_week" ? request.text : todayDraftText(result.draft, t, i18n.resolvedLanguage ?? i18n.language);
                setAsk({ id: request.id, text, context });
            });
        }
        return () => { current = false; };
    }, [request, retry, t, i18n]);

    return <div className="relative flex h-full w-[420px] max-w-[50%] shrink-0 flex-col" data-testid="activity-ask-panel">
        {failed && <div role="alert" className="absolute inset-x-0 top-20 z-10 flex items-center gap-2 bg-panel px-4 py-2 text-sm text-error">
            {t("actions.chatOpenFailed")}
            <Button variant="quiet" size="sm" onClick={() => setRetry((value) => value + 1)}>{t("todayBrief.retry")}</Button>
        </div>}
        <MainChatPanel topicsFailed={false} onRetryTopics={() => {}} draft={draft} prepared={prepared}
            onDraftChange={(text, isPrepared) => { setDraft(text); setPrepared(isPrepared); }} handlers={handlers}
            starterRequest={ask} onStarterConsumed={() => setAsk(null)} onClose={onClose} />
    </div>;
}
