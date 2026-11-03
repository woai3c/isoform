import { useEffect } from "react";
import { useTranslation } from "react-i18next";

import { EventsOn } from "../../wailsjs/runtime/runtime";
import { speakOnce } from "../lib/tts-player";

interface TtsAnnounceEvent {
    category: string;
    name: string;
}

/**
 * Listens for backend "tts:announce" events and reads a short, localized
 * announcement aloud. Currently used for automation completion ("<job>
 * finished"). The backend only emits when the relevant auto-speak category is
 * enabled, so this hook just composes the localized sentence and speaks it —
 * it does not re-check the setting.
 */
export function useTtsAnnounce() {
    const { t } = useTranslation("settings");

    useEffect(() => {
        const cleanup = EventsOn("tts:announce", (event: TtsAnnounceEvent) => {
            if (event?.category !== "automation") return;
            const name = event.name?.trim();
            if (!name) return;
            const text = t("autoSpeak.automationDoneSpeech", { name });
            // speakOnce (unique id) so back-to-back completions of the same
            // job — whose announcement text is identical — interrupt and play
            // rather than toggling each other off.
            void speakOnce(text);
        });
        return cleanup;
    }, [t]);
}
