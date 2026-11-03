import { Bell } from "lucide-react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { notifyToast } from "@/lib/notify";

export function LongToastTestButton() {
    const { t } = useTranslation("settings");
    return (
        <Button
            variant="outline"
            size="sm"
            onClick={() => notifyToast(
                t("notifications.longToast.sampleTitle"),
                t("notifications.longToast.sampleMessage"),
                "error",
            )}
        >
            <Bell className="w-3.5 h-3.5 mr-1.5" />
            {t("notifications.testButton")}
        </Button>
    );
}
