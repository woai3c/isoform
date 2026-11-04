import { useTranslation } from "react-i18next";

/** An unknown setting has no switch state to display or change. */
export function SettingReadError({ retry }: { retry: () => void }) {
    const { t } = useTranslation("settings");
    return <span role="alert" className="flex items-center gap-2 text-xs text-copper">
        {t("settingRead.failed")}
        <button type="button" className="text-accent hover:underline" onClick={retry}>{t("settingRead.retry")}</button>
    </span>;
}
