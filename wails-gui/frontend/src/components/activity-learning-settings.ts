export const ACTIVITY_LEARNING_FEATURE_KEYS = {
    events: "triage_enable_events",
    memory: "triage_enable_memory",
    knowledge: "triage_enable_knowledge",
    shadow: "triage_enable_shadow",
    sop: "triage_enable_sop",
} as const;

export type ActivityLearningFeatureKey =
    keyof typeof ACTIVITY_LEARNING_FEATURE_KEYS;

export interface ActivityLearningFeatureRow {
    key: ActivityLearningFeatureKey;
    titleKey: string;
    descriptionKey: string;
}

export const ACTIVITY_LEARNING_FEATURE_ROWS: readonly ActivityLearningFeatureRow[] = [
    {
        key: "events",
        titleKey: "activityTriage.eventsTitle",
        descriptionKey: "activityTriage.eventsDescription",
    },
    {
        key: "memory",
        titleKey: "activityTriage.memoryTitle",
        descriptionKey: "activityTriage.memoryDescription",
    },
    {
        key: "knowledge",
        titleKey: "activityTriage.knowledgeTitle",
        descriptionKey: "activityTriage.knowledgeDescription",
    },
    {
        key: "shadow",
        titleKey: "activityTriage.shadowTitle",
        descriptionKey: "activityTriage.shadowDescription",
    },
    {
        key: "sop",
        titleKey: "activityTriage.sopTitle",
        descriptionKey: "activityTriage.sopDescription",
    },
];
