import { createRoot } from "react-dom/client";
import i18n from "@/i18n";
import "@/index.css";
import { ThemeProvider } from "@/hooks/useTheme";
import { TaskThreadPane } from "@/components/tasks/TaskThreadPane";
import { TaskSuggestionDeck } from "@/components/tasks/TaskSuggestionDeck";

const params = new URLSearchParams(location.search);
localStorage.setItem("yansu.theme", params.get("theme") === "dark" ? "onedark" : "paper");
const zh = params.get("lang") === "zh-CN";
const detail = {
    id: "draft.sample", title: zh ? "把签好的 SOW 发给 Priya" : "Send Priya the signed SOW", group: "needs_you", status: "draft_ready", updatedAt: "2026-10-30T17:12:00Z", actions: ["send", "not_now"], steps: [],
    origin: { kind: "up_next", at: "2026-10-30T16:02:00Z" },
    events: [{ id: "question", kind: "isoform", at: "2026-10-30T16:32:00Z", text: zh ? "Priya 在等签好的 SOW。现在发送吗？" : "Priya is waiting for the signed SOW. Send it now?", choices: [{ id: "send", label: zh ? "现在发送" : "Send now" }, { id: "wait", label: zh ? "等一下" : "Wait" }] }],
    draft: { channel: "mail", to: "Priya", subject: "Re: Halcyon SOW", body: zh ? "Priya，\n\n签好的 SOW 已附上。" : "Priya,\n\nThe signed SOW is attached.", state: "ready", recipients: { to: ["Priya"], notIncluded: ["Dan"] }, why: { text: "Priya asked for the signed SOW today.", textLocalized: { "zh-CN": "Priya 说今天需要签好的 SOW。" }, source: "mail", at: "2026-10-30T15:40:00Z" } },
};
const offer = { id: "sample", version: "1", kind: "suggestion", deckKind: "reply", title: zh ? "感谢 Maya 介绍 Lena" : "Thank Maya for the Lena intro", why: "", prompt: "", evidence: { text: "Maya introduced you to Lena on Tuesday. Your mail shows no reply yet.", textLocalized: { "zh-CN": "Maya 周二把 Lena 介绍给你。邮件里还没有你的回复。" }, source: "mail", at: "2026-10-30T15:40:00Z" } };
const values: Record<string, unknown> = { GetDesktopTaskThread: detail, ActOnDesktopTask: detail, ListTaskSuggestions: [offer], FindMoreTaskSuggestions: { offers: [offer] } };
(window as unknown as { go: unknown }).go = { main: { App: new Proxy({}, { get: (_, method: string) => async () => structuredClone(values[method] ?? null) }) } };
void i18n.changeLanguage(params.get("lang") ?? "en").then(() => createRoot(document.getElementById("root")!).render(
    <ThemeProvider><div className="h-screen bg-background p-5"><div className="flex h-full overflow-hidden rounded-xl border border-border bg-panel">{params.get("view") === "offer" ? <div className="w-full p-6"><TaskSuggestionDeck /></div> : <TaskThreadPane id="draft.sample" />}</div></div></ThemeProvider>,
));
