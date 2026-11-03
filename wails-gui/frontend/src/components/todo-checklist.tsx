import { Circle, CircleCheck, CircleDot } from 'lucide-react';

/** One entry of a TodoWrite `todos` input array. */
export interface TodoEntry {
    content: string;
    status: 'pending' | 'in_progress' | 'completed';
    activeForm?: string;
}

const TODO_STATUSES = new Set(['pending', 'in_progress', 'completed']);

/**
 * Extract the todo list from a TodoWrite tool input. Tolerant of malformed
 * input (streaming can surface partial JSON): anything that isn't a
 * well-formed entry is dropped instead of breaking the row render.
 */
export function parseTodoInput(toolInput: unknown): TodoEntry[] {
    if (!toolInput || typeof toolInput !== 'object') return [];
    const todos = (toolInput as Record<string, unknown>).todos;
    if (!Array.isArray(todos)) return [];
    const valid: TodoEntry[] = [];
    for (const raw of todos) {
        if (!raw || typeof raw !== 'object') continue;
        const entry = raw as Record<string, unknown>;
        if (typeof entry.content !== 'string' || entry.content.length === 0) continue;
        if (typeof entry.status !== 'string' || !TODO_STATUSES.has(entry.status)) continue;
        valid.push({
            content: entry.content,
            status: entry.status as TodoEntry['status'],
            activeForm:
                typeof entry.activeForm === 'string' && entry.activeForm.length > 0
                    ? entry.activeForm
                    : undefined,
        });
    }
    return valid;
}

/**
 * Renders a TodoWrite snapshot as a read-only checklist under the tool
 * group header. Each TodoWrite call carries the FULL list with updated
 * statuses, so during streaming the newest group always shows the current
 * state of the plan.
 */
export function TodoChecklist({ todos }: { todos: TodoEntry[] }) {
    return (
        <ul className="m-0 flex list-none flex-col gap-1 p-0 pl-5">
            {todos.map((todo, idx) => {
                const isDone = todo.status === 'completed';
                const isActive = todo.status === 'in_progress';
                // in_progress entries carry a present-continuous label
                // ("Fixing the bug") — prefer it over the imperative content.
                const label = isActive && todo.activeForm ? todo.activeForm : todo.content;
                const Icon = isDone ? CircleCheck : isActive ? CircleDot : Circle;
                return (
                    <li
                        key={`${idx}-${todo.content}`}
                        className="flex items-start gap-1.5 text-xs leading-relaxed"
                    >
                        <Icon
                            aria-hidden
                            className={`mt-0.5 size-3.5 shrink-0 ${
                                isDone
                                    ? 'text-muted/70'
                                    : isActive
                                      ? 'text-ink'
                                      : 'text-muted/50'
                            }`}
                        />
                        <span
                            className={
                                isDone
                                    ? 'text-muted line-through'
                                    : isActive
                                      ? 'font-medium text-ink'
                                      : 'text-muted'
                            }
                        >
                            {label}
                        </span>
                    </li>
                );
            })}
        </ul>
    );
}
