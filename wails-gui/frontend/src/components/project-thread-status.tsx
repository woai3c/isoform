import { useCallback, useEffect, useState } from "react";
import { useTranslation } from "react-i18next";
import { FileText, Folder } from "lucide-react";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { GetWorkspaceChangedFiles } from "../../wailsjs/go/main/App";
import { GetBackgroundShells, type BgShellStatus } from "@/lib/wails-shim-app";

export type ProjectCommandState = "running" | "finished" | "stopped" | "failed";

export interface ProjectCommand {
    id: string;
    command: string;
    state: ProjectCommandState;
    startedAt?: number;
    exit?: number;
}

interface ShellDone {
    tag?: string;
    shell_id?: string;
    exit?: number;
    status?: string;
    command?: string;
}

const MAX_CHIPS = 8;

/** A finished step reads as how it ended, from the exit the computer saw. */
export function commandStateFromDone(done: ShellDone): ProjectCommandState {
    const status = (done.status ?? "").toLowerCase();
    if (status === "killed" || status === "stopped" || (typeof done.exit === "number" && done.exit < 0)) return "stopped";
    if (status === "failed" || (typeof done.exit === "number" && done.exit !== 0)) return "failed";
    return "finished";
}

function elapsed(startMs: number, now: number): string {
    const seconds = Math.max(0, Math.floor((now - startMs) / 1000));
    if (seconds < 60) return `${seconds}s`;
    const minutes = Math.floor(seconds / 60);
    if (minutes < 60) return `${minutes}m`;
    return `${Math.floor(minutes / 60)}h ${minutes % 60}m`;
}

const basename = (path: string) => path.split("/").filter(Boolean).pop() ?? path;

/** A project's task thread (§7, K7): its folder, the files it changed as
 *  chips, and its running steps, in the thread instead of a second panel. */
export function ProjectThreadStatus({ sessionTag, folder, branch, revision }: {
    sessionTag: string;
    folder?: string;
    branch?: string;
    /** Changes when a turn ends, so the changed files are read again. */
    revision?: unknown;
}) {
    const { t } = useTranslation("chat");
    const [files, setFiles] = useState<string[]>([]);
    const [running, setRunning] = useState<BgShellStatus[]>([]);
    const [ended, setEnded] = useState<ProjectCommand[]>([]);
    const [unreadable, setUnreadable] = useState(false);
    const [now, setNow] = useState(() => Date.now());

    const readFiles = useCallback(() => {
        if (!folder) {
            setFiles([]);
            return;
        }
        GetWorkspaceChangedFiles(folder, branch || "HEAD")
            .then((result) => setFiles((result ?? []).map((file) => file.path).filter(Boolean)))
            .catch(() => setFiles([]));
    }, [folder, branch]);

    const readShells = useCallback(() => {
        if (!sessionTag) return;
        GetBackgroundShells(sessionTag)
            .then((result) => {
                setUnreadable(false);
                setRunning((result ?? []).filter((shell) => shell.running));
            })
            .catch(() => setUnreadable(true));
    }, [sessionTag]);

    useEffect(() => readFiles(), [readFiles, revision]);
    useEffect(() => {
        setEnded([]);
        readShells();
        const offDone = EventsOn("bg:shell:done", (payload: ShellDone) => {
            if (payload?.tag && payload.tag !== sessionTag) return;
            if (payload?.shell_id) {
                const id = payload.shell_id;
                setEnded((current) => [
                    { id, command: payload.command ?? "", state: commandStateFromDone(payload), exit: payload.exit },
                    ...current.filter((entry) => entry.id !== id),
                ].slice(0, 3));
            }
            readShells();
            readFiles();
        });
        const offRegistered = EventsOn("bg:shell:registered", () => readShells());
        return () => {
            offDone();
            offRegistered();
        };
    }, [sessionTag, readShells, readFiles]);
    useEffect(() => {
        if (running.length === 0) return;
        const timer = window.setInterval(() => setNow(Date.now()), 1000);
        return () => window.clearInterval(timer);
    }, [running.length]);

    const commands: ProjectCommand[] = [
        ...running.map((shell) => ({ id: shell.shell_id, command: shell.command, state: "running" as const, startedAt: shell.started_at })),
        ...ended.filter((entry) => !running.some((shell) => shell.shell_id === entry.id)),
    ];
    if (!folder && commands.length === 0 && !unreadable) return null;
    const line = (command: ProjectCommand) => {
        switch (command.state) {
            case "running":
                return t("projectThread.command.running", { command: command.command, elapsed: elapsed(command.startedAt ?? now, now) });
            case "stopped":
                return t("projectThread.command.stopped", { command: command.command });
            case "failed":
                return t("projectThread.command.failed", { command: command.command, code: command.exit ?? "" });
            default:
                return t("projectThread.command.finished", { command: command.command });
        }
    };
    return (
        <div data-testid="project-thread-status" className="flex flex-col gap-1.5 px-3 pb-1.5 text-[12px] text-muted">
            {folder && (
                <p className="flex min-w-0 items-center gap-1.5" title={folder}>
                    <Folder aria-hidden className="size-3.5 shrink-0" />
                    <span className="truncate">{t("projectThread.folder", { path: folder })}</span>
                </p>
            )}
            {files.length > 0 && (
                <ul aria-label={t("projectThread.changedFiles")} className="flex flex-wrap items-center gap-1.5">
                    {files.slice(0, MAX_CHIPS).map((path) => (
                        <li key={path} title={path} className="inline-flex max-w-[220px] items-center gap-1 rounded-md border border-border bg-panel px-2 py-0.5 text-ink">
                            <FileText aria-hidden className="size-3 shrink-0 text-muted" />
                            <span className="truncate">{basename(path)}</span>
                        </li>
                    ))}
                    {files.length > MAX_CHIPS && <li className="text-muted">{t("projectThread.moreFiles", { count: files.length - MAX_CHIPS })}</li>}
                </ul>
            )}
            {commands.length > 0 && (
                <ul aria-label={t("projectThread.steps")} className="flex flex-col gap-0.5">
                    {commands.map((command) => (
                        <li key={command.id} data-command-state={command.state} className="truncate font-mono">{line(command)}</li>
                    ))}
                </ul>
            )}
            {unreadable && <p role="status" data-command-state="unreadable">{t("projectThread.command.unreadable")}</p>}
        </div>
    );
}
