import { useEffect } from "react";
import { useTranslation } from "react-i18next";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { maybeSpeakNotification } from "../lib/tts-player";

interface HandoffCreatedEvent {
    id: string;
    title: string;
}

export function useHandoffNotification() {
    const { t } = useTranslation("app");

    useEffect(() => {
        const cleanup = EventsOn(
            "handoff:created",
            (event: HandoffCreatedEvent) => {
                if (!event?.id) return;
                const title = event.title?.trim() || t("handoffToast.createdFallback");
                void maybeSpeakNotification(title);
            },
        );
        return cleanup;
    }, [t]);
}
