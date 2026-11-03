import type { TFunction } from "i18next";
import type { ModelGroupView } from "./wails-shim-app";

/** Keep routing IDs and user-chosen names intact; only the original app labels change. */
export function modelGroupDisplayName(
    group: Pick<ModelGroupView, "id" | "name">,
    t: TFunction<"onboarding">,
): string {
    switch (group.id) {
        case "yansu-chat":
            if (group.name === "yansu chat") return t("agent.groupNames.chat");
            break;
        case "yansu-background":
            if (group.name === "yansu background") return t("agent.groupNames.background");
            break;
        case "yansu-light":
            if (group.name === "yansu light") return t("agent.groupNames.light");
            break;
    }
    return group.name || group.id;
}
