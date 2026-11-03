import { useTranslation } from "react-i18next";
import { CircleAlert } from "lucide-react";
import { cn } from "@/lib/utils";

/** A failure said in place (tasks-review 1.0.0, tasks-05): gray for a read
 *  or an open, copper for a save that didn't go through. Never red, never a
 *  toast. */
/** A failure said in place: gray for a read, copper for a write; never red, never a toast. */
export function FailureLine({ text, tone = "gray", onRetry, joiner = " ", className, testId }: { text: string; tone?: "gray" | "copper"; onRetry?: () => void; joiner?: string; className?: string; testId?: string }) {
    const { t, i18n } = useTranslation("app");
    // Chinese runs the sentence and 重试 together, as the handoff writes it.
    const between = joiner === " " && i18n.language.startsWith("zh") ? "" : joiner;
    return (
        <p role="alert" data-failure-line={tone} data-testid={testId} className={cn("flex items-start gap-1.5 text-[13px] leading-5", tone === "copper" ? "text-copper" : "text-muted", className)}>
            <CircleAlert aria-hidden="true" className="mt-0.5 size-4 shrink-0" />
            <span>{text}{onRetry && <>{between}<button type="button" className="font-medium text-accent hover:underline" onClick={onRetry}>{t("tasks.failure.tryAgain")}</button></>}</span>
        </p>
    );
}
