import type { ReactNode } from "react";

/** A Tasks section heading: the name and an action at the right (Find more,
 *  Show all). No count beside it (tasks-review v1.0.3 #5). */
export function TaskSectionHeader({ title, action }: { title: string; action?: ReactNode }) {
    return (
        <header className="mb-2.5 flex min-h-7 items-center gap-2">
            <h2 className="text-[14px] font-semibold text-ink">{title}</h2>
            {action && <div className="ml-auto">{action}</div>}
        </header>
    );
}
