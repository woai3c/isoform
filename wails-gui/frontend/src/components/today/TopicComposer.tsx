import { useId, useRef, type ReactNode, type KeyboardEvent, type Ref } from "react";
import { ArrowUp, Loader2, Mic, Plus, Square, X } from "lucide-react";
import { JumpToNewest } from "@/components/jump-to-newest";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

/**
 * The box that sends to the main chat with a topic attached: the topic panel
 * on Today and the main chat's topic filter (RC 2 D1) share it. Enter sends,
 * Shift+Enter breaks the line. A chip names the attached topic when the box
 * can drop it.
 */
export function TopicComposer({
    draft,
    prepared,
    onDraftChange,
    onSend,
    sending,
    inputLabel,
    placeholder,
    sendLabel,
    note,
    chip,
    stop,
    disabled = false,
    noteTestId = "topic-panel-note",
    className,
    replyNavigation,
    contextPreview,
    inputRef,
    attach,
    voice,
}: {
    draft: string;
    /** The words were put there by a click, not typed. */
    prepared: boolean;
    onDraftChange: (draft: string, prepared: boolean) => void;
    onSend: () => void;
    sending: boolean;
    inputLabel: string;
    placeholder: string;
    sendLabel: string;
    note?: string;
    chip?: { name: string; removeLabel: string; onRemove: () => void };
    /** A reply is running in the main chat: Stop takes Send's place and
     *  the words wait in the box. */
    stop?: { label: string; onStop: () => void };
    /** Send waits: the words' Today item is still being prepared. */
    disabled?: boolean;
    noteTestId?: string;
    className?: string;
    replyNavigation?: { count: number; visible: boolean; jumpToLatest: () => void };
    /** Draft context belongs with the note, below the input surface. */
    contextPreview?: ReactNode;
    inputRef?: Ref<HTMLTextAreaElement>;
    /** The + of the Chat page's box (K9): attached files show as chips. */
    attach?: {
        label: string;
        accept: string;
        onFiles: (files: FileList) => void;
        items: { key: string; name: string; removeLabel: string; onRemove: () => void }[];
        error?: string;
    };
    /** The Chat page's mic: words heard go into the box. */
    voice?: { label: string; stopLabel: string; state: "idle" | "initializing" | "recording" | "transcribing" | "error"; onToggle: () => void };
}) {
    const inputId = useId();
    const onKeyDown = (event: KeyboardEvent<HTMLTextAreaElement>) => {
        if (event.key === "Enter" && !event.shiftKey && !event.nativeEvent.isComposing) {
            event.preventDefault();
            onSend();
        }
    };
    const fileInput = useRef<HTMLInputElement>(null);
    const canSend = Boolean(draft.trim()) && !sending && !disabled;
    return (
        <div className={cn("flex min-w-0 shrink-0 flex-col gap-1.5 [overflow-wrap:anywhere]", className)}>
            <div
                data-topic-composer-surface
                className={cn(
                    "relative flex flex-col gap-2 rounded-xl border bg-composer-bg py-2 pl-3.5 pr-2 transition-colors",
                    prepared ? "border-border-focus shadow-[var(--shadow-composer-focus)]" : "border-composer-border shadow-[var(--shadow-composer)] focus-within:border-border-focus",
                )}
            >
                {replyNavigation && <JumpToNewest count={replyNavigation.count} visible={replyNavigation.visible} onJump={replyNavigation.jumpToLatest} compact aboveComposer />}
                <div className="flex w-full">
                    <label htmlFor={inputId} className="sr-only">{inputLabel}</label>
                    <textarea
                        ref={inputRef}
                        id={inputId}
                        rows={1}
                        value={draft}
                        onChange={(event) => onDraftChange(event.target.value, false)}
                        onKeyDown={onKeyDown}
                        placeholder={placeholder}
                        className="max-h-40 min-h-8 min-w-0 flex-1 resize-none bg-transparent py-1.5 text-[14px] leading-snug text-ink outline-none placeholder:text-muted"
                    />
                </div>
                {attach && attach.items.length > 0 && (
                    <div className="flex flex-wrap gap-1.5" data-testid="topic-composer-attachments">
                        {attach.items.map((item) => (
                            <span key={item.key} className="inline-flex min-h-6 max-w-full items-center gap-1 rounded-md border border-border bg-panel-warm pl-2.5 pr-0.5 text-[12px] text-ink">
                                <span className="min-w-0 max-w-[220px] truncate">{item.name}</span>
                                <Button size="icon" variant="quiet" className="size-5 rounded-md" aria-label={item.removeLabel} onClick={item.onRemove}>
                                    <X className="size-3" />
                                </Button>
                            </span>
                        ))}
                    </div>
                )}
                <div className="flex w-full items-center justify-between gap-2" data-testid="topic-composer-toolbar">
                    {attach && (
                        <>
                            <input
                                ref={fileInput}
                                type="file"
                                multiple
                                accept={attach.accept}
                                className="hidden"
                                data-testid="topic-composer-file-input"
                                onChange={(event) => {
                                    if (event.target.files) attach.onFiles(event.target.files);
                                    event.target.value = "";
                                }}
                            />
                            <Button size="icon" variant="quiet" className="-ml-1.5 size-8 shrink-0 text-ink" aria-label={attach.label} title={attach.label} disabled={disabled} onClick={() => fileInput.current?.click()}>
                                <Plus className="size-4" />
                            </Button>
                        </>
                    )}
                    {chip && (
                        <div className="min-w-0">
                            <span className="inline-flex min-h-6 max-w-full items-center gap-1 rounded-md border border-border bg-panel-warm pl-2.5 pr-0.5 text-[12px] text-ink" data-testid="topic-composer-chip">
                                <span className="min-w-0 max-w-[260px] whitespace-normal [overflow-wrap:anywhere]">{chip.name}</span>
                                <Button size="icon" variant="quiet" className="size-5 rounded-md" aria-label={chip.removeLabel} onClick={chip.onRemove}>
                                    <X className="size-3" />
                                </Button>
                            </span>
                        </div>
                    )}
                    <div className="ml-auto flex shrink-0 items-center gap-1.5">
                        {voice && (
                            <Button
                                size="icon"
                                variant="quiet"
                                aria-label={voice.state === "recording" ? voice.stopLabel : voice.label}
                                title={voice.state === "recording" ? voice.stopLabel : voice.label}
                                aria-pressed={voice.state === "recording"}
                                disabled={disabled || voice.state === "initializing" || voice.state === "transcribing"}
                                onClick={voice.onToggle}
                                className={cn("size-8 shrink-0", voice.state === "recording" ? "bg-accent/10 text-accent" : "text-muted hover:text-ink")}
                                data-testid="topic-composer-voice"
                            >
                                {voice.state === "transcribing" || voice.state === "initializing" ? <Loader2 className="size-4 animate-spin" /> : <Mic className="size-4" />}
                            </Button>
                        )}
                        {stop ? (
                            <Button
                                size="icon"
                                variant="quiet"
                                aria-label={stop.label}
                                title={stop.label}
                                onClick={stop.onStop}
                                className="size-8 shrink-0 bg-solid text-solid-fg hover:bg-solid/80 hover:text-solid-fg hover:opacity-100 [&_svg]:translate-y-0"
                                data-testid="topic-composer-stop"
                            >
                                <Square className="size-3.5 fill-current" />
                            </Button>
                        ) : (
                            <Button
                                size="icon"
                                variant="quiet"
                                aria-label={sendLabel}
                                disabled={!canSend}
                                onClick={onSend}
                                className={cn(
                                    // The topic panel's look before it moved here: the
                                    // arrow sits centred, it never dims, and an empty
                                    // box shows "not allowed".
                                    "size-8 shrink-0 disabled:opacity-100 [&_svg]:translate-y-0",
                                    draft.trim()
                                        ? "patina-verdigris bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] hover:bg-solid-btn-bg/80 hover:text-solid-btn-fg hover:opacity-100"
                                        : "cursor-not-allowed bg-border text-muted shadow-none disabled:pointer-events-auto",
                                )}
                            >
                                <ArrowUp className="size-4" />
                            </Button>
                        )}
                    </div>
                </div>
            </div>
            {attach?.error && <p role="alert" className="text-[12px] text-error">{attach.error}</p>}
            {contextPreview}
            {note && <p aria-live="polite" className="text-[12px] text-muted" data-testid={noteTestId}>{note}</p>}
        </div>
    );
}
