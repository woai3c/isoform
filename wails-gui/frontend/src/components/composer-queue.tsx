import { useTranslation } from "react-i18next";
import { Clock, Pencil, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { QueuedMessage } from "@/hooks/useComposerInput";

/**
 * Messages waiting to send after the reply that is running. The main chat's
 * box shows them, and so does the topic box while a topic is attached, so a
 * filter never hides one.
 */
export function QueuedMessageList({
    messages,
    onEdit,
    onRemove,
    className,
}: {
    messages: readonly QueuedMessage[];
    onEdit: (index: number) => void;
    onRemove: (index: number) => void;
    className?: string;
}) {
    const { t } = useTranslation("app");
    const { t: tChat } = useTranslation("chat");
    if (messages.length === 0) return null;
    return (
        <div className={cn("flex flex-col gap-1.5", className)} data-testid="queued-messages">
            {messages.map((msg, i) => (
                <div
                    key={i}
                    className="group/queued flex items-start gap-2 rounded-lg bg-ink/[0.03] px-3 py-2"
                >
                    <Clock className="size-3.5 shrink-0 mt-0.5 text-muted" />
                    <div className="flex-1 min-w-0">
                        <p className="text-xs text-ink/60 line-clamp-2 break-all">
                            {msg.prompt}
                        </p>
                        {msg.awaitingReview && (
                            <p className="mt-1 text-xs text-muted/70">{tChat("todayContext.reviewBeforeSending")}</p>
                        )}
                    </div>
                    <div className="flex items-center gap-0.5 shrink-0 opacity-0 group-hover/queued:opacity-100 focus-within:opacity-100 transition-opacity">
                        <Button
                            size="icon"
                            variant="quiet"
                            className="size-5 rounded [&_svg]:translate-y-0"
                            onClick={() => onEdit(i)}
                            title={t("composer.queue.editMessage")}
                            aria-label={t("composer.queue.editMessage")}
                        >
                            <Pencil className="size-3" />
                        </Button>
                        <Button
                            size="icon"
                            variant="quiet"
                            className="size-5 rounded hover:bg-error-bg hover:text-error [&_svg]:translate-y-0"
                            onClick={() => onRemove(i)}
                            title={t("composer.queue.removeMessage")}
                            aria-label={t("composer.queue.removeMessage")}
                        >
                            <X className="size-3" />
                        </Button>
                    </div>
                </div>
            ))}
        </div>
    );
}
