import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { toast } from "sonner";
import { Switch } from "@/components/ui/switch";
import { LoadUIState, SaveUIState } from "../../../../wailsjs/go/main/App";
import {
    ACTIVITY_LEARNING_FEATURE_KEYS,
    ACTIVITY_LEARNING_FEATURE_ROWS,
} from "@/components/activity-learning-settings";
import { SettingsCard, SettingsRow } from "@/components/settings/settings-primitives";
import { SettingReadError } from "./setting-read-error";
import { SettingsControlCard } from "./capture-shared";

// ── What Isoform learns ──
//
// The activity-learning switches, moved here from the Observation page. Each
// switch is a ui_state key the Go side reads (activity_triage_prompt.go);
// turning one off trims the matching model work and skips what it would have
// created. Observing never starts work on its own.
//
// "Learn by comparison" (the shadow observer) and its "Prediction confidence"
// follow-up are less common, so they sit under the page's Advanced section.

const SHADOW_CONFIDENCE_ADJUSTMENT_KEY = "shadow_confidence_adjustment_enabled";

const EVERYDAY_ROWS = ACTIVITY_LEARNING_FEATURE_ROWS.filter((row) => row.key !== "shadow");

/**
 * One persisted learning switch. A missing or empty value keeps the default
 * of on, the same default the Go side uses (loadTriageFeatureFlags), so
 * installs that never touched the switch keep learning. A failed save puts the
 * switch back and says so.
 */
function useLearningSwitch(stateKey: string) {
    const { t } = useTranslation("settings");
    const [enabled, setEnabled] = useState<boolean | null>(null);
    const [readFailed, setReadFailed] = useState(false);
    const read = useCallback(() => {
        setEnabled(null);
        setReadFailed(false);
        LoadUIState(stateKey).then((value) => {
            if (typeof value !== "string") throw new Error("Invalid setting");
            setEnabled(value !== "false");
        }).catch(() => setReadFailed(true));
    }, [stateKey]);
    useEffect(() => { read(); }, [read]);
    const update = useCallback((next: boolean) => {
        setEnabled(next);
        SaveUIState(stateKey, next ? "true" : "false").catch(() => {
            read();
            toast.error(t("activityTriage.saveFailed"));
        });
    }, [stateKey, t, read]);
    return { enabled, update, readFailed, read };
}

function LearningControl({ setting, title, disabled = false }: {
    setting: ReturnType<typeof useLearningSwitch>; title: string; disabled?: boolean;
}) {
    if (setting.readFailed) return <SettingReadError retry={setting.read} />;
    if (setting.enabled === null) return null;
    return <Switch aria-label={title} checked={setting.enabled} disabled={disabled} onCheckedChange={setting.update} />;
}

function LearningSwitchRow({
    stateKey,
    title,
    description,
}: {
    stateKey: string;
    title: string;
    description: string;
}) {
    const setting = useLearningSwitch(stateKey);
    return (
        <SettingsRow
            title={title}
            description={description}
            control={<LearningControl setting={setting} title={title} />}
        />
    );
}

/** Everyday card: what Isoform may learn from what you do on this computer. */
export function LearningFeaturesSettings() {
    const { t } = useTranslation("settings");
    const translateSetting = (key: string) => String(t(key as never));

    return (
        <SettingsCard
            title={t("learningMemory.learns.title")}
            description={t("activityTriage.cardDescription")}
        >
            {EVERYDAY_ROWS.map((row) => (
                <LearningSwitchRow
                    key={row.key}
                    stateKey={ACTIVITY_LEARNING_FEATURE_KEYS[row.key]}
                    title={translateSetting(row.titleKey)}
                    description={translateSetting(row.descriptionKey)}
                />
            ))}
        </SettingsCard>
    );
}

/**
 * Advanced: "Learn by comparison", with its switch beside the title, and
 * "Prediction confidence" as a row under it. The follow-up only has an effect
 * while comparisons run, so it is disabled with a reason while the parent is
 * off. Same layout as Listen with Record meetings automatically on the
 * Observation page: the dependent row sits in the parent's card, not indented.
 */
export function LearnByComparisonSettings() {
    const { t } = useTranslation("settings");
    const comparison = useLearningSwitch(ACTIVITY_LEARNING_FEATURE_KEYS.shadow);
    const confidence = useLearningSwitch(SHADOW_CONFIDENCE_ADJUSTMENT_KEY);
    const comparisonTitle = t("activityTriage.shadowTitle");
    const confidenceTitle = t("activityTriage.confidenceAdjustmentTitle");

    return (
        <SettingsControlCard
            title={comparisonTitle}
            description={t("activityTriage.shadowDescription")}
            control={<LearningControl setting={comparison} title={comparisonTitle} />}
        >
            <SettingsRow
                title={confidenceTitle}
                description={
                    comparison.enabled !== false
                        ? t("activityTriage.confidenceAdjustmentDescription")
                        : t("learningMemory.comparison.confidenceNeedsComparison")
                }
                control={<LearningControl setting={confidence} title={confidenceTitle} disabled={comparison.enabled !== true} />}
            />
        </SettingsControlCard>
    );
}
