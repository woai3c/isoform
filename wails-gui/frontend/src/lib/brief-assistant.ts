import type { TFunction } from "i18next";

/** The words for each assistant, and for when the assistant is not known. */
export interface AssistantNames {
    claudeCode: string;
    codex: string;
    yourAI: string;
}

/**
 * The name a signed-out line gives its assistant (R75): the chosen assistant's
 * product name, or "Your AI" only when the run does not say which assistant it was.
 */
export function assistantLabel(assistant: string | undefined, names: AssistantNames): string {
    switch (assistant) {
        case "claude-code":
        case "claude":
            return names.claudeCode;
        case "codex":
            return names.codex;
        default:
            return names.yourAI;
    }
}

/** The Tasks › brief detail names (app namespace). */
export function briefAssistantName(assistant: string | undefined, t: TFunction<"app">): string {
    return assistantLabel(assistant, {
        claudeCode: t("briefDetail.assistantClaudeCode"),
        codex: t("briefDetail.assistantCodex"),
        yourAI: t("briefDetail.yourAI"),
    });
}
