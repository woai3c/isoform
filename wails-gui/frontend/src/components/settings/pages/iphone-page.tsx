import { MobileConnectionSettings } from "@/components/mobile-connection-settings";
import { openSettingsPage, type OpenSettingsPage } from "./capture-shared";

/**
 * Phone: the connection status, pairing, paired phones and the daily brief.
 * Text-message setup has its own Coming soon page. The server address and
 * what each phone may do sit under Advanced. Notification preferences
 * live on the Notifications page. The Written summary note links to
 * Assistant with Advanced open, where its switch lives.
 */
export function IPhonePage({ onOpenPage }: { onOpenPage?: OpenSettingsPage } = {}) {
    return (
        <MobileConnectionSettings
            onOpenAssistant={() => openSettingsPage("ai-provider", onOpenPage, { advanced: true })}
        />
    );
}
