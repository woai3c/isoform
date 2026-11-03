import { useEffect, useState } from "react";
import { clockTime } from "@/lib/brief-time";
import { useTranslation } from "react-i18next";
import { AlertCircle, Loader2, RefreshCw } from "lucide-react";
import { cn } from "@/lib/utils";
import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";
import type { TopicFind, TopicFindFeedbackAction } from "@/lib/wails-shim-app";
import type { UseTopicFindsResult } from "@/hooks/useTopicFinds";
import { webLink } from "./TodayBriefSection";

type Translate = (key: string, options?: Record<string, unknown>) => string;

/** Today shows at most three finds. */
export const MAX_FINDS = 3;

/** A compact row of up to three titles and sources from the topic search. */
export function TopicFindsSection({
    finds,
    onOpenTopics,
    now = new Date(),
}: {
    finds: UseTopicFindsResult;
    onOpenTopics: () => void;
    now?: Date;
}) {
    const { t, i18n } = useTranslation("today");
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const view = finds.view;
    const finding = view?.status === "finding";
    const items = (view?.items ?? []).slice(0, MAX_FINDS);

    let body: React.ReactNode;
    if (finds.loading && !view) {
        body = <FindsSkeleton />;
    } else if (!view && finds.loadFailed) {
        body = (
            <Notice
                icon={<AlertCircle className="size-4" />}
                action={
                    <button type="button" className="text-[12px] font-medium text-muted transition-colors hover:text-ink" onClick={finds.refresh}>
                        {tr("finds.retry")}
                    </button>
                }
            >
                {tr("finds.loadFailed")}
            </Notice>
        );
    } else if (!view || view.status === "off") {
        body = (
            <Notice
                action={
                    <button type="button" className="text-[12px] font-medium text-muted transition-colors hover:text-ink" onClick={onOpenTopics}>
                        {tr("finds.turnOn")}
                    </button>
                }
            >
                {tr("finds.off")}
            </Notice>
        );
    } else if (items.length > 0) {
        body = (
            <ul className="flex gap-3 rounded-2xl border border-border bg-background p-3" data-testid="topic-finds-row">
                {items.map((find) => (
                    <li key={find.id} className="min-w-0 flex-1" data-testid="topic-find">
                        <FindLink find={find} onAnswer={finds.feedback} />
                    </li>
                ))}
            </ul>
        );
    } else if (finding) {
        body = (
            <div>
                <div className="mb-3 flex items-center gap-2 text-[12px] text-muted" role="status">
                    <Loader2 className="size-3.5 animate-spin" />
                    {tr("finds.finding")}
                </div>
                <FindsSkeleton />
            </div>
        );
    } else if (view.status === "unavailable") {
        body = (
            <Notice
                icon={<AlertCircle className="size-4" />}
                action={
                    <button type="button" className="text-[12px] font-medium text-muted transition-colors hover:text-ink" onClick={() => void finds.findNew()}>
                        {tr("finds.retry")}
                    </button>
                }
            >
                {tr("finds.unavailable")}
            </Notice>
        );
    } else if (view.status === "unconfigured") {
        body = <Notice icon={<AlertCircle className="size-4" />}>{tr("finds.unconfigured")}</Notice>;
    } else if (view.status === "error") {
        body = (
            <Notice
                icon={<AlertCircle className="size-4" />}
                action={
                    <button type="button" className="text-[12px] font-medium text-muted transition-colors hover:text-ink" onClick={() => void finds.findNew()}>
                        {tr("finds.retry")}
                    </button>
                }
            >
                {tr("finds.error")}
            </Notice>
        );
    } else if (view.status === "no_topics") {
        body = <Notice>{tr("finds.noTopics")}</Notice>;
    } else {
        body = (
            <Notice>
                <span className="block">{tr("finds.none")}</span>
                {view.topics.length > 0 && (
                    <span className="mt-1 block text-[12px] text-muted/80">
                        {tr("finds.watching", { topics: view.topics.join(", ") })}
                    </span>
                )}
            </Notice>
        );
    }

    const canFind = view?.enabled && view.status !== "unconfigured";
    const cooldownUntil = view?.cooldownUntil ? new Date(view.cooldownUntil) : null;
    const coolingDown = cooldownUntil !== null && !Number.isNaN(cooldownUntil.getTime()) && cooldownUntil > now;
    // Render again when the cooldown ends, so Find new comes back on its own.
    const [, setCooledDown] = useState(0);
    const cooldownEndsAt = coolingDown && cooldownUntil ? cooldownUntil.getTime() : 0;
    useEffect(() => {
        if (!cooldownEndsAt) return;
        const timer = window.setTimeout(() => setCooledDown((n) => n + 1), Math.max(0, cooldownEndsAt - Date.now()) + 250);
        return () => window.clearTimeout(timer);
    }, [cooldownEndsAt]);

    return (
        <section data-testid="topic-finds">
            <div className="mb-2 flex items-center justify-between gap-3">
                <div className="text-[11px] font-semibold uppercase tracking-wider text-muted/70">{tr("finds.label")}</div>
                <div className="flex items-center gap-3 text-[12px]">
                    <button type="button" className="font-medium text-muted transition-colors hover:text-ink" onClick={onOpenTopics}>
                        {tr("finds.yourTopics")}
                    </button>
                    {canFind && (
                        <button
                            type="button"
                            className="inline-flex items-center gap-1 font-medium text-muted transition-colors hover:text-ink disabled:cursor-wait disabled:hover:text-muted"
                            onClick={() => void finds.findNew()}
                            disabled={finding || coolingDown}
                            aria-busy={finding}
                        >
                            <RefreshCw className={cn("size-3.5", finding && "animate-spin")} />
                            {tr("finds.findNew")}
                        </button>
                    )}
                </div>
            </div>
            {canFind && coolingDown && cooldownUntil && !finding && (
                <p className="mb-2 text-[12px] text-muted" role="status">
                    {tr("finds.cooldown", {
                        time: clockTime(cooldownUntil, i18n.language, tr),
                    })}
                </p>
            )}
            {body}
            {finds.failure && (
                <p role="alert" className="mt-2 text-[12px] text-muted">
                    {tr(finds.failure === "find" ? "finds.findFailed" : "finds.failed")}
                </p>
            )}
        </section>
    );
}

function FindLink({
    find,
    onAnswer,
}: {
    find: TopicFind;
    onAnswer: (findId: string, action: TopicFindFeedbackAction) => Promise<boolean>;
}) {
    const link = webLink(find.url);
    const source = find.source || (link ? new URL(link).hostname.replace(/^www\./, "") : "");
    const content = (
        <>
            <span className="block truncate text-[13px] font-medium text-ink">{find.title}</span>
            {source && <span className="block truncate text-[11px] text-muted">{source}</span>}
        </>
    );
    if (!link) return <div>{content}</div>;
    return (
        <button
            type="button"
            className="block w-full rounded-sm text-left transition-colors hover:bg-ink/[0.03] focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-border-focus"
            title={find.title}
            onClick={() => {
                BrowserOpenURL(link);
                void onAnswer(find.id, "open");
            }}
        >
            {content}
        </button>
    );
}

function FindsSkeleton() {
    return (
        <div className="flex gap-3 rounded-2xl border border-border bg-background p-3">
            {[0, 1, 2].map((i) => (
                <div key={i} className="min-w-0 flex-1">
                    <div className="h-3 w-4/5 animate-pulse rounded bg-ink/[0.06]" />
                    <div className="mt-2 h-2.5 w-2/5 animate-pulse rounded bg-ink/[0.06]" />
                </div>
            ))}
        </div>
    );
}

function Notice({
    children,
    icon,
    action,
}: {
    children: React.ReactNode;
    icon?: React.ReactNode;
    action?: React.ReactNode;
}) {
    return (
        <div className="flex items-center gap-3 rounded-2xl border border-border bg-background px-4 py-3.5 text-[13px] text-muted">
            {icon && <span className="shrink-0 text-muted/70">{icon}</span>}
            <span className="flex-1">{children}</span>
            {action}
        </div>
    );
}
