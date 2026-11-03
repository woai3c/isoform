import type { TFunction } from "i18next";
import evaluation from "../../../../asr-eval/RESULTS.md?raw";
import type { SetupModel } from "./setup-model-types";

export function modelEvaluationTable(t: TFunction<"onboarding">): string {
    const tables = [
        ["Models", "models", ["model", "framework", "size", "languages"]],
        [
            "Datasets",
            "datasets",
            ["dataset", "language", "samples", "hours", "source"],
        ],
        [
            "Word Error Rate (lower = better)",
            "wer",
            ["model", "fleurs", "ascend", "clean", "other"],
        ],
        [
            "Character Error Rate (lower = better)",
            "cer",
            ["model", "fleurs", "ascend", "clean", "other"],
        ],
        [
            "Real-Time Factor (lower = faster)",
            "rtf",
            ["model", "fleurs", "ascend", "clean", "other"],
        ],
        [
            "Latency and Resources",
            "resources",
            ["model", "latency", "memory", "load"],
        ],
        ["Tier List", "tiers", ["tier", "model", "bestFor"]],
    ] as const;
    const cellText = (
        cell: string,
        section: string,
        column: number,
        row: number,
    ): string => {
        if (section === "tiers" && column === 2)
            return t(
                `models.evaluationCopy.tierNotes.${["mimo", "qwen3", "sensevoice", "parakeet", "whisper", "paraformer"][row] as "mimo" | "qwen3" | "sensevoice" | "parakeet" | "whisper" | "paraformer"}`,
                {
                    english: tableRow(
                        "Word Error Rate (lower = better)",
                        "Parakeet TDT v2",
                    )?.[2],
                    rtf: tableRow(
                        "Real-Time Factor (lower = faster)",
                        "Paraformer-zh",
                    )?.[0],
                },
            );
        if (cell === "N/A") return t("models.evaluationCopy.notAvailable");
        if (cell === "SenseVoice (current)")
            return t("models.evaluationCopy.current", { name: "SenseVoice" });
        if (cell === "English only")
            return t("models.evaluationCopy.englishOnly");
        if (cell === "Chinese") return t("models.evaluationCopy.chinese");
        if (cell === "English") return t("models.evaluationCopy.english");
        if (cell === "zh-en mixed") return t("models.evaluationCopy.mixed");
        if (cell.endsWith(" languages"))
            return t("models.evaluationCopy.languageCount", {
                count: Number.parseInt(cell),
            });
        if (cell === "zh/en/dialects")
            return t("models.evaluationCopy.dialects");
        return cell;
    };
    const date = evaluation.match(/Last updated: (\S+)/)?.[1] ?? "";
    const samples = Number(
        evaluation.match(/(\d+) samples per dataset per model/)?.[1],
    );
    const result = [
        t("models.evaluationCopy.updated", { date }),
        t("models.evaluationCopy.method", { samples }),
    ];
    for (const [source, key, columns] of tables) {
        const rows = evaluationSection(source)
            .split("\n")
            .filter((line) => line.startsWith("| "))
            .slice(1);
        result.push(`## ${t(`models.evaluationCopy.headings.${key}`)}`);
        if (key === "rtf")
            result.push(
                t("models.evaluationCopy.rtfNote", {
                    threshold: evaluation.match(/RTF < ([\d.]+)/)?.[1],
                    factor: evaluation.match(/RTF ([\d.]+) =/)?.[1],
                    speed: evaluation.match(/= (\d+)x real-time/)?.[1],
                }),
            );
        const table = [
            `| ${columns.map((column) => t(`models.evaluationCopy.columns.${column}`)).join(" | ")} |`,
            `| ${columns.map(() => "---").join(" | ")} |`,
        ];
        rows.forEach((line, row) =>
            table.push(
                `| ${line
                    .split("|")
                    .slice(1, -1)
                    .map((cell, column) =>
                        cellText(cell.trim(), key, column, row),
                    )
                    .join(" | ")} |`,
            ),
        );
        result.push(table.join("\n"));
        if (key === "wer")
            result.push(
                t("models.evaluationCopy.whisperNote", {
                    percent: evaluation.match(/WER >(\d+)%/)?.[1],
                }),
            );
    }
    result.push(
        `## ${t("models.evaluationCopy.headings.recommendation")}`,
        t("models.evaluationCopy.recommendation"),
    );
    result.push(
        t("models.evaluationCopy.speedNote", {
            sensevoice: tableRow(
                "Real-Time Factor (lower = faster)",
                "SenseVoice",
            )?.[3],
            mimo: tableRow(
                "Real-Time Factor (lower = faster)",
                "MiMo-V2.5-ASR",
            )?.[3],
            qwen3: tableRow(
                "Real-Time Factor (lower = faster)",
                "Qwen3-ASR-1.7B",
            )?.[3],
        }),
        t("models.evaluationCopy.runtimeNote"),
        t("models.evaluationCopy.accuracyNote", {
            chinese: tableRow(
                "Word Error Rate (lower = better)",
                "SenseVoice",
            )?.[0],
        }),
        t("models.evaluationCopy.future"),
    );
    return result.join("\n\n");
}

function evaluationSection(heading: string) {
    return evaluation.split(`## ${heading}\n`)[1]?.split("\n## ")[0] ?? "";
}

/** Product phrases are translated; quantities stay in the manifest and the
 * evaluation file so changing a recommended model never duplicates results. */
export function modelCopyKey(model: SetupModel) {
    const name = model.name.toLowerCase();
    if (name.includes("sensevoice")) return "sensevoice";
    if (name.includes("parakeet")) return "parakeet";
    if (name.includes("whisper")) return "whisper";
    if (name.includes("qwen3")) return "qwen3";
    if (name.includes("mimo")) return "mimo";
    if (name.includes("kokoro")) return "kokoro";
    if (name.includes("melotts")) return "melotts";
    if (model.job === "privacy") return "privacy";
    if (model.job === "memory") return "memory";
    if (model.job === "speaker") return "speaker";
    return name === "none" ? "none" : "unknown";
}
export function modelName(model: SetupModel, t: TFunction<"onboarding">) {
    return ["privacy", "memory", "speaker"].includes(model.job)
        ? t(`models.copy.${model.job as "privacy" | "memory" | "speaker"}.name`)
        : t("models.modelName", { name: model.name });
}
export function modelDescription(
    model: SetupModel,
    t: TFunction<"onboarding">,
) {
    const key = modelCopyKey(model);
    return key === "unknown"
        ? t("models.manifestDescription", { description: model.description })
        : t(`models.copy.${key}.description`);
}
function tableRow(heading: string, name: string) {
    const section = evaluationSection(heading);
    return section
        .split("\n")
        .find((line) => line.replaceAll("**", "").startsWith(`| ${name} |`))
        ?.split("|")
        .slice(2, -1)
        .map((cell) => Number.parseFloat(cell.replaceAll("*", "").trim()));
}
export function modelEvidence(model: SetupModel, t: TFunction<"onboarding">) {
    const key = modelCopyKey(model);
    const tableName =
        key === "sensevoice"
            ? "SenseVoice"
            : key === "parakeet"
              ? "Parakeet TDT v2"
              : key === "whisper"
                ? "Whisper v3-turbo"
                : key === "mimo"
                  ? "MiMo-V2.5-ASR"
                  : key === "qwen3" && model.name.includes("1.7")
                    ? "Qwen3-ASR-1.7B"
                    : null;
    if (!tableName) return "";
    const wer = tableRow("Word Error Rate (lower = better)", tableName);
    const rtf = tableRow("Real-Time Factor (lower = faster)", tableName);
    if (!wer || !rtf) return "";
    return t(
        key === "parakeet"
            ? "models.evidenceEnglishRange"
            : "models.evidenceBilingual",
        {
            english: wer[2].toFixed(1),
            chinese: wer[0].toFixed(1),
            speed: Math.round(1 / rtf[3]),
            speedLow: Math.round(1 / rtf[2]),
        },
    );
}
export function modelSourceName(
    host: string,
    name: string,
    t: TFunction<"onboarding">,
) {
    if (name === "mirror" && !host.includes("yansu.app"))
        return t("models.customMirror");
    return host.includes("yansu.app")
        ? t("models.sourceMirror")
        : host === "huggingface.co"
          ? t("models.sourceHuggingFace")
          : host === "github.com"
            ? t("models.sourceGitHub")
            : t("models.modelName", { name });
}
