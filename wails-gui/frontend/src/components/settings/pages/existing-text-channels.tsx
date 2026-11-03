import { TextMessagesManagement, useTextMessages } from "@/components/text-messages-settings";
import { SmsTextSettings } from "@/components/sms-text-settings";

/** Existing subscriptions keep their stop/pause controls; no new setup is offered. */
export function ExistingTextChannels() {
    const texts = useTextMessages();
    return <><SmsTextSettings existingOnly /><TextMessagesManagement controller={texts} /></>;
}
