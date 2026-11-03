import {
    LoadStreamItemsPage,
    ListMainChatTopicItems,
    ListMainChatWaitingItems,
} from "@/lib/wails-shim-app";
import {
    beginOpenAnswerDelivery,
    registerDeliveredOpenAnswers,
} from "@/hooks/useOpenAnswers";

export async function loadStreamItemsPageWithOpenAnswerDelivery(
    ...args: Parameters<typeof LoadStreamItemsPage>
) {
    const startedGeneration = beginOpenAnswerDelivery();
    const page = await LoadStreamItemsPage(...args);
    const items = (page?.items ?? []) as Array<{ openAnswer?: unknown }>;
    registerDeliveredOpenAnswers(
        items.flatMap((item) => item.openAnswer === undefined ? [] : [item.openAnswer]),
        startedGeneration,
    );
    return page;
}

export async function listMainChatTopicItemsWithOpenAnswerDelivery(
    ...args: Parameters<typeof ListMainChatTopicItems>
) {
    const startedGeneration = beginOpenAnswerDelivery();
    const page = await ListMainChatTopicItems(...args);
    const items = (page as unknown as {
        items?: Array<{ message?: { openAnswer?: unknown } | null }>;
    } | null)?.items ?? [];
    registerDeliveredOpenAnswers(
        items.flatMap((item) => item.message?.openAnswer === undefined ? [] : [item.message.openAnswer]),
        startedGeneration,
    );
    return page;
}

export async function listMainChatWaitingItemsWithOpenAnswerDelivery(
    ...args: Parameters<typeof ListMainChatWaitingItems>
) {
    const startedGeneration = beginOpenAnswerDelivery();
    const page = await ListMainChatWaitingItems(...args);
    const items = (page?.items ?? []) as Array<{ openAnswer?: unknown }>;
    registerDeliveredOpenAnswers(
        items.flatMap((item) => item.openAnswer === undefined ? [] : [item.openAnswer]),
        startedGeneration,
    );
    return page;
}
