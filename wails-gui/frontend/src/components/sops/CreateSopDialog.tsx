import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ArrowUp, Check, Loader2 } from "lucide-react";
import { ActivityInlineNotice, errorNotice, type ActivityNotice } from "@/components/activity-inline-notice";
import {
    GenerateSOPFromDescription,
    type SopAuthorStatus,
} from "@/lib/wails-shim-app";
import {
    Dialog,
    DialogCloseButton,
    DialogContent,
    DialogHeader,
    DialogTitle,
} from "@/components/ui/dialog";

interface CreateSopDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    /** Live author status mirrored from `sop:author-status` by the parent. */
    authorStatus: SopAuthorStatus | null;
}

/**
 * Chat-style SOP creation dialog: the user describes the process in their own
 * words, the backend matches the description against recent activities and
 * drafts the SOP from them. Progress is mirrored from the author status
 * stream; the dialog is safe to close mid-run (authoring continues in the
 * background and the list refreshes on `sop:updated`).
 */
export function CreateSopDialog({
    open,
    onOpenChange,
    authorStatus,
}: CreateSopDialogProps) {
    const { t } = useTranslation("app");
    const [description, setDescription] = useState("");
    const [submittedText, setSubmittedText] = useState<string | null>(null);
    const scrollRef = useRef<HTMLDivElement | null>(null);

    // Reset the conversation whenever the dialog is (re)opened.
    useEffect(() => {
        if (open) {
            setDescription("");
            setSubmittedText(null);
        }
    }, [open]);

    useEffect(() => {
        scrollRef.current?.scrollTo({ top: scrollRef.current.scrollHeight });
    }, [submittedText, authorStatus]);

    const running = Boolean(authorStatus?.running);
    const finished =
        submittedText !== null && !running && authorStatus?.lastEvent === "saved";
    const failed =
        submittedText !== null &&
        !running &&
        authorStatus?.lastEvent === "failed";
    const skipped =
        submittedText !== null &&
        !running &&
        authorStatus?.lastEvent === "skipped";

    const [startFailed, setStartFailed] = useState<ActivityNotice | null>(null);
    const submit = () => {
        const text = description.trim();
        if (!text || submittedText !== null) return;
        setStartFailed(null);
        GenerateSOPFromDescription(text)
            .then(() => {
                setSubmittedText(text);
                setDescription("");
            })
            .catch(() => setStartFailed(errorNotice(t("sopsPage.createFailed"), submit)));
    };
    const retryDraft = () => {
        const text = submittedText;
        if (!text) return;
        GenerateSOPFromDescription(text).catch(() => setStartFailed(errorNotice(t("sopsPage.createFailed"), retryDraft)));
    };

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="w-[560px]">
                <DialogHeader>
                    <DialogTitle>{t("sopsPage.createDialogTitle")}</DialogTitle>
                    <DialogCloseButton />
                </DialogHeader>

                <div
                    ref={scrollRef}
                    className={`flex max-h-[340px] min-h-[160px] flex-col gap-3 overflow-y-auto px-6 pt-4 ${submittedText === null ? "" : "pb-5"}`}
                >
                    {/* Assistant intro */}
                    <div className="max-w-[85%] self-start rounded-2xl rounded-tl-md bg-ink/[0.05] px-3.5 py-2.5 text-[13px] leading-relaxed text-ink">
                        {t("sopsPage.createIntro")}
                    </div>

                    {/* User description */}
                    {submittedText !== null && (
                        <div className="max-w-[85%] self-end whitespace-pre-wrap rounded-2xl rounded-tr-md bg-solid-btn-bg px-3.5 py-2.5 text-[13px] leading-relaxed text-solid-btn-fg">
                            {submittedText}
                        </div>
                    )}

                    {/* Progress / result */}
                    {submittedText !== null && !finished && !failed && !skipped && (
                        <div className="flex max-w-[85%] items-start gap-2 self-start rounded-2xl rounded-tl-md bg-ink/[0.05] px-3.5 py-2.5 text-[13px] leading-relaxed text-ink">
                            <Loader2 className="mt-0.5 size-3.5 shrink-0 animate-spin text-muted" />
                            <span>{t("sopsPage.createSearching")}</span>
                        </div>
                    )}
                    {finished && (
                        <div className="flex max-w-[85%] items-start gap-2 self-start rounded-2xl rounded-tl-md bg-ink/[0.05] px-3.5 py-2.5 text-[13px] leading-relaxed text-ink">
                            <Check className="mt-0.5 size-3.5 shrink-0 text-muted" />
                            <span>{t("sopsPage.createDone")}</span>
                        </div>
                    )}
                    {skipped && (
                        <div className="flex max-w-[85%] items-start gap-2 self-start rounded-2xl rounded-tl-md bg-ink/[0.05] px-3.5 py-2.5 text-[13px] leading-relaxed text-ink">
                            <Check className="mt-0.5 size-3.5 shrink-0 text-muted" />
                            <span>{t("sopsPage.createSkipped")}</span>
                        </div>
                    )}
                    {failed && (
                        <ActivityInlineNotice
                            notice={errorNotice(t("sopsPage.createFailedStatus"), retryDraft)}
                            className="self-start text-[13px]"
                        />
                    )}
                </div>

                <ActivityInlineNotice notice={startFailed} className="px-6 text-[13px]" />
                {/* Composer */}
                {submittedText === null && (
                    <div className="flex items-end gap-2 px-6 pb-5 pt-3">
                        <textarea
                            value={description}
                            onChange={(e) => setDescription(e.target.value)}
                            onKeyDown={(e) => {
                                if (e.key === "Enter" && !e.shiftKey) {
                                    e.preventDefault();
                                    submit();
                                }
                            }}
                            rows={3}
                            autoFocus
                            placeholder={t("sopsPage.createPlaceholder")}
                            className="flex-1 resize-none rounded-xl border border-border-input bg-panel px-3 py-2.5 text-[13px] leading-relaxed text-ink placeholder:text-muted outline-none focus:border-ink/20 focus:ring-1 focus:ring-ink/20 transition-colors"
                        />
                        <button
                            className="flex size-9 shrink-0 items-center justify-center rounded-full patina-verdigris bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] transition-opacity hover:opacity-90 disabled:opacity-40 cursor-pointer"
                            disabled={!description.trim()}
                            onClick={submit}
                            title={t("sopsPage.createSend")}
                        >
                            <ArrowUp className="size-4" />
                        </button>
                    </div>
                )}
            </DialogContent>
        </Dialog>
    );
}
