import { useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { TaskThreadPane } from "@/components/tasks/TaskThreadPane";
import { closeMainChatSidePanel, type MainChatSidePanel } from "@/lib/main-chat-side-panel";

function ReplyDraftCard({ panel }: { panel: Extract<MainChatSidePanel, { kind: "draft" }> }) {
    const { t } = useTranslation("chat");
    const [text, setText] = useState(panel.text);
    const [editing, setEditing] = useState(false);
    useEffect(() => { setText(panel.text); setEditing(false); }, [panel]);
    const send = async () => {
        const words = text.trim();
        if (!words) return;
        if ((await panel.send(words)) === false) return;
        closeMainChatSidePanel();
    };
    return (
        <section data-testid="main-chat-reply-draft" className="flex flex-col gap-2.5 rounded-lg border border-border bg-panel px-3.5 py-3">
            <p className="m-0 text-[12px] font-medium text-muted">{t("mainChat.replyDraft.label")}</p>
            {editing ? (
                <textarea
                    aria-label={t("mainChat.replyDraft.label")}
                    className="min-h-24 w-full resize-y rounded-md border border-border bg-background px-2.5 py-2 text-[13.5px] leading-relaxed text-ink outline-none"
                    value={text}
                    onChange={(event) => setText(event.target.value)}
                    autoFocus
                />
            ) : (
                <p className="m-0 whitespace-pre-wrap break-words text-[13.5px] leading-relaxed text-ink">{text}</p>
            )}
            <div className="flex items-center gap-2">
                <Button size="sm" disabled={!text.trim()} onClick={() => void send()}>{t("mainChat.replyDraft.send")}</Button>
                {!editing && <Button size="sm" variant="quiet" onClick={() => setEditing(true)}>{t("mainChat.replyDraft.edit")}</Button>}
            </div>
        </section>
    );
}

/** Chat's 420 pt panel for a running task's thread or a draft reply (Chat K4). Chat stays. */
export function MainChatSidePanelView({ panel }: { panel: MainChatSidePanel }) {
    const { t } = useTranslation("chat");
    useEffect(() => {
        const onKey = (event: KeyboardEvent) => {
            if (event.key === "Escape" && !event.defaultPrevented) closeMainChatSidePanel();
        };
        window.addEventListener("keydown", onKey);
        return () => window.removeEventListener("keydown", onKey);
    }, []);
    return (
        <aside data-testid="main-chat-side-panel" data-side-panel={panel.kind} className="flex min-h-0 shrink-0 flex-col border-l border-border-subtle bg-background-accent" style={{ width: 420 }}>
            <div className="flex justify-end px-3 pt-2">
                <Button size="icon" variant="quiet" aria-label={t("mainChat.proactive.closePanel")} onClick={closeMainChatSidePanel}><X className="size-4" /></Button>
            </div>
            {panel.kind === "task"
                ? <TaskThreadPane id={panel.id} onClose={closeMainChatSidePanel} />
                : <div className="px-4 pb-4"><ReplyDraftCard panel={panel} /></div>}
        </aside>
    );
}
