import { useState } from "react";
import { useTranslation } from "react-i18next";
import { OpenAssistantSignIn } from "@/lib/wails-shim-app";
import { Button } from "@/components/ui/button";

/** Opens the same assistant sign-in as Settings and keeps launch errors here. */
export function BriefSignIn({ assistant, size = "13px" }: { assistant: string; size?: string }) {
    const { t } = useTranslation("today");
    const [failed, setFailed] = useState(false);
    const open = () => {
        setFailed(false);
        void OpenAssistantSignIn(assistant).catch(() => setFailed(true));
    };
    return <span><Button variant="link" size="sm" className="h-auto p-0 font-medium" style={{ fontSize: size }} onClick={open}>{t("todayBrief.pending.signIn")}</Button>{failed && <> <span role="alert" className="text-copper">{t("todayBrief.pending.signInFailed")}</span> <Button variant="link" size="sm" className="h-auto p-0 font-medium" style={{ fontSize: size }} onClick={open}>{t("todayBrief.pending.signInTryAgain")}</Button></>}</span>;
}
