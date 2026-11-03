import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/confirm-dialog";

/**
 * The Disconnect button every chat-app setup shows once it is connected.
 * Disconnecting stops Isoform answering in that app, so it asks first.
 */
export function DisconnectButton({ appName, onConfirm }: { appName: string; onConfirm: () => void }) {
    const { t } = useTranslation("settings");
    const [open, setOpen] = useState(false);
    return (
        <>
            <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
                {t("connectors.common.disconnectButton")}
            </Button>
            <ConfirmDialog
                open={open}
                onOpenChange={setOpen}
                title={t("chatApps.disconnectConfirm.title", { app: appName })}
                description={t("chatApps.disconnectConfirm.description", { app: appName })}
                confirmLabel={t("connectors.common.disconnectButton")}
                variant="danger"
                onConfirm={onConfirm}
            />
        </>
    );
}
