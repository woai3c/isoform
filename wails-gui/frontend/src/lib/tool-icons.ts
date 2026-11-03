import {
    FileText,
    FilePen,
    Terminal,
    Search,
    FolderSearch,
    Pencil,
    Globe,
    GitBranch,
    Bot,
    Wrench,
    Sparkles,
    ListTodo,
    Code2,
    Clock,
    HelpCircle,
    BookOpen,
    Compass,
    CalendarClock,
    MessageSquare,
    Zap,
    Plug,
    type LucideIcon,
} from 'lucide-react';

/**
 * Identifies which i18n key under `chat.tools.running` / `chat.tools.done`
 * provides the verb phrase for this tool group. Multiple raw tool names
 * (e.g. `Read`, `ReadFile`, `read_file`) share the same key.
 */
export type ToolPhraseKey =
    | 'read'
    | 'write'
    | 'edit'
    | 'bash'
    | 'grep'
    | 'glob'
    | 'webFetch'
    | 'webSearch'
    | 'applyPatch'
    | 'repl'
    | 'agent'
    | 'toolSearch'
    | 'skill'
    | 'todoWrite'
    | 'taskCreate'
    | 'taskGet'
    | 'taskList'
    | 'taskOutput'
    | 'taskStop'
    | 'taskUpdate'
    | 'lsp'
    | 'sleep'
    | 'askUserQuestion'
    | 'notebookEdit'
    | 'enterPlanMode'
    | 'exitPlanMode'
    | 'enterWorktree'
    | 'exitWorktree'
    | 'sendMessage'
    | 'brief'
    | 'cronCreate'
    | 'cronDelete'
    | 'cronList'
    | 'remoteTrigger'
    | 'default'
    | 'mcp';

interface ToolMeta {
    icon: LucideIcon;
    label: string;
    color: string;
    /** Key into `chat.tools.running` / `chat.tools.done` for the group verb phrase. */
    phraseKey: ToolPhraseKey;
}

const TOOL_META: Record<string, ToolMeta> = {
    // File reading
    Read: { icon: FileText, label: 'Read', color: 'text-accent', phraseKey: 'read' },
    ReadFile: { icon: FileText, label: 'Read', color: 'text-accent', phraseKey: 'read' },
    read_file: { icon: FileText, label: 'Read', color: 'text-accent', phraseKey: 'read' },

    // File writing
    Write: { icon: FilePen, label: 'Write', color: 'text-accent', phraseKey: 'write' },
    WriteFile: { icon: FilePen, label: 'Write', color: 'text-accent', phraseKey: 'write' },
    write_file: { icon: FilePen, label: 'Write', color: 'text-accent', phraseKey: 'write' },

    // File editing
    Edit: { icon: Pencil, label: 'Edit', color: 'text-accent', phraseKey: 'edit' },
    EditFile: { icon: Pencil, label: 'Edit', color: 'text-accent', phraseKey: 'edit' },
    edit_file: { icon: Pencil, label: 'Edit', color: 'text-accent', phraseKey: 'edit' },

    // Shell commands
    Bash: { icon: Terminal, label: 'Bash', color: 'text-accent', phraseKey: 'bash' },
    bash: { icon: Terminal, label: 'Bash', color: 'text-accent', phraseKey: 'bash' },
    execute: { icon: Terminal, label: 'Execute', color: 'text-accent', phraseKey: 'bash' },

    // Search
    Grep: { icon: Search, label: 'Grep', color: 'text-accent', phraseKey: 'grep' },
    grep: { icon: Search, label: 'Grep', color: 'text-accent', phraseKey: 'grep' },
    search: { icon: Search, label: 'Search', color: 'text-accent', phraseKey: 'grep' },

    // Glob
    Glob: { icon: FolderSearch, label: 'Glob', color: 'text-accent', phraseKey: 'glob' },
    glob: { icon: FolderSearch, label: 'Glob', color: 'text-accent', phraseKey: 'glob' },

    // Web
    WebFetch: { icon: Globe, label: 'Web', color: 'text-accent-2', phraseKey: 'webFetch' },
    WebSearch: { icon: Globe, label: 'Web', color: 'text-accent-2', phraseKey: 'webSearch' },

    // Codex: patch-based file editing
    apply_patch: { icon: GitBranch, label: 'Patch', color: 'text-accent', phraseKey: 'applyPatch' },

    // Codex: shell execution
    shell: { icon: Terminal, label: 'Shell', color: 'text-accent', phraseKey: 'bash' },
    shell_command: { icon: Terminal, label: 'Shell', color: 'text-accent', phraseKey: 'bash' },
    'container.exec': { icon: Terminal, label: 'Exec', color: 'text-accent', phraseKey: 'bash' },

    // Windows shell / REPL
    PowerShell: { icon: Terminal, label: 'PowerShell', color: 'text-accent', phraseKey: 'bash' },
    REPL: { icon: Terminal, label: 'REPL', color: 'text-accent', phraseKey: 'repl' },

    // Claude Code: agent / skill / tool orchestration
    Agent: { icon: Bot, label: 'Agent', color: 'text-muted', phraseKey: 'agent' },
    Task: { icon: Bot, label: 'Task', color: 'text-muted', phraseKey: 'agent' },
    ToolSearch: { icon: Wrench, label: 'Tools', color: 'text-muted', phraseKey: 'toolSearch' },
    Skill: { icon: Sparkles, label: 'Skill', color: 'text-accent', phraseKey: 'skill' },

    // Claude Code: todo / task tracking
    TodoWrite: { icon: ListTodo, label: 'Todos', color: 'text-muted', phraseKey: 'todoWrite' },
    TaskCreate: { icon: ListTodo, label: 'Task', color: 'text-muted', phraseKey: 'taskCreate' },
    TaskGet: { icon: ListTodo, label: 'Task', color: 'text-muted', phraseKey: 'taskGet' },
    TaskList: { icon: ListTodo, label: 'Task', color: 'text-muted', phraseKey: 'taskList' },
    TaskOutput: { icon: ListTodo, label: 'Task', color: 'text-muted', phraseKey: 'taskOutput' },
    TaskStop: { icon: ListTodo, label: 'Task', color: 'text-muted', phraseKey: 'taskStop' },
    TaskUpdate: { icon: ListTodo, label: 'Task', color: 'text-muted', phraseKey: 'taskUpdate' },

    // Claude Code: code intelligence
    LSP: { icon: Code2, label: 'LSP', color: 'text-muted', phraseKey: 'lsp' },

    // Claude Code: misc
    Sleep: { icon: Clock, label: 'Sleep', color: 'text-muted', phraseKey: 'sleep' },
    AskUserQuestion: { icon: HelpCircle, label: 'Ask', color: 'text-muted', phraseKey: 'askUserQuestion' },
    NotebookEdit: { icon: BookOpen, label: 'Notebook', color: 'text-accent', phraseKey: 'notebookEdit' },
    EnterPlanMode: { icon: Compass, label: 'Plan', color: 'text-muted', phraseKey: 'enterPlanMode' },
    ExitPlanMode: { icon: Compass, label: 'Plan', color: 'text-muted', phraseKey: 'exitPlanMode' },
    EnterWorktree: { icon: GitBranch, label: 'Worktree', color: 'text-muted', phraseKey: 'enterWorktree' },
    ExitWorktree: { icon: GitBranch, label: 'Worktree', color: 'text-muted', phraseKey: 'exitWorktree' },
    SendMessage: { icon: MessageSquare, label: 'Message', color: 'text-muted', phraseKey: 'sendMessage' },
    SendUserMessage: { icon: MessageSquare, label: 'Message', color: 'text-muted', phraseKey: 'sendMessage' },
    Brief: { icon: MessageSquare, label: 'Brief', color: 'text-muted', phraseKey: 'brief' },

    // Claude Code: scheduling
    CronCreate: { icon: CalendarClock, label: 'Cron', color: 'text-muted', phraseKey: 'cronCreate' },
    CronDelete: { icon: CalendarClock, label: 'Cron', color: 'text-muted', phraseKey: 'cronDelete' },
    CronList: { icon: CalendarClock, label: 'Cron', color: 'text-muted', phraseKey: 'cronList' },
    RemoteTrigger: { icon: Zap, label: 'Trigger', color: 'text-muted', phraseKey: 'remoteTrigger' },
};

const DEFAULT_META: ToolMeta = {
    icon: Wrench,
    label: 'Tool',
    color: 'text-muted',
    phraseKey: 'default',
};

const MCP_META: ToolMeta = {
    icon: Plug,
    label: 'MCP',
    color: 'text-muted',
    phraseKey: 'mcp',
};

/**
 * Humanize a raw tool name for the group phrase fallback.
 *   snake_case_name   → "Snake Case Name"
 *   CamelCaseName     → "Camel Case Name"
 *   mcp__server__tool → "server · tool"
 */
export function humanizeToolName(name: string): string {
    if (name.startsWith('mcp__')) {
        const parts = name.slice(5).split('__');
        return parts.join(' · ');
    }
    const withSpaces = name
        .replace(/[_.-]+/g, ' ')
        .replace(/([a-z])([A-Z])/g, '$1 $2');
    return withSpaces.charAt(0).toUpperCase() + withSpaces.slice(1);
}

export interface ResolvedToolMeta extends ToolMeta {
    /** True when this is a generic/MCP fallback that needs the tool name appended for clarity. */
    needsToolNameSuffix: boolean;
    /** Humanized version of the raw tool name (used for default/MCP suffixes). */
    humanizedName: string;
}

export function getToolMeta(toolName: string): ResolvedToolMeta {
    const known = TOOL_META[toolName];
    if (known) {
        return { ...known, needsToolNameSuffix: false, humanizedName: humanizeToolName(toolName) };
    }
    if (toolName.startsWith('mcp__')) {
        return { ...MCP_META, needsToolNameSuffix: true, humanizedName: humanizeToolName(toolName) };
    }
    if (!toolName) {
        return { ...DEFAULT_META, needsToolNameSuffix: false, humanizedName: '' };
    }
    return { ...DEFAULT_META, needsToolNameSuffix: true, humanizedName: humanizeToolName(toolName) };
}

/** Last path component, handling both POSIX (`/`) and Windows (`\`) separators. */
function basename(path: string): string {
    const trimmed = path.replace(/[\\/]+$/, '');
    const idx = Math.max(trimmed.lastIndexOf('/'), trimmed.lastIndexOf('\\'));
    return idx >= 0 ? trimmed.slice(idx + 1) : trimmed;
}

const FILE_PATH_KEYS = ['file_path', 'path', 'filepath', 'filename'] as const;

function pickFilePath(input: Record<string, unknown>): string | undefined {
    for (const key of FILE_PATH_KEYS) {
        const v = input[key];
        if (typeof v === 'string' && v.length > 0) return v;
    }
    return undefined;
}

/**
 * Extracts a human-readable summary from tool input.
 * Returns a short string suitable for inline display.
 */
export function getToolSummary(
    _toolName: string,
    toolInput: unknown,
    _index: number
): string {
    if (!toolInput || typeof toolInput !== 'object') return '';
    const input = toolInput as Record<string, unknown>;

    // File path (Read, Write, Edit) — accept multiple key variants (Claude
    // uses `file_path`, Codex/some MCP tools use `path`) so the filename
    // chip renders for every file-touching tool, not just Claude's exact
    // schema. basename() handles Windows-style `\\` paths.
    const filePath = pickFilePath(input);
    if (filePath) {
        return basename(filePath);
    }

    // Pattern (Grep, Glob)
    if (input.pattern && typeof input.pattern === 'string') {
        const p = input.pattern;
        return p.length > 60 ? `${p.slice(0, 60)}…` : p;
    }

    // Command (Bash / codex shell) — prefer the human-readable description
    // when one was provided (e.g. Bash tool's `description` field), otherwise
    // fall back to a truncated preview of the command itself.
    const cmdStr = input.command ?? input.cmd;
    if (cmdStr && typeof cmdStr === 'string') {
        if (typeof input.description === 'string' && input.description.trim().length > 0) {
            return input.description.trim();
        }
        return cmdStr.length > 60 ? `${cmdStr.slice(0, 60)}…` : cmdStr;
    }

    // Query (WebSearch)
    if (input.query && typeof input.query === 'string') {
        const q = input.query;
        return q.length > 60 ? `${q.slice(0, 60)}…` : q;
    }

    // URL (WebFetch)
    if (input.url && typeof input.url === 'string') {
        try {
            return new URL(input.url).hostname;
        } catch {
            return input.url.slice(0, 40);
        }
    }

    // apply_patch: extract filenames from patch text
    const patchText = input.patch ?? input.input;
    if (_toolName === 'apply_patch' && patchText && typeof patchText === 'string') {
        const files: string[] = [];
        for (const line of patchText.split('\n')) {
            const m = line.match(/^\*\*\*\s+(?:Update|Add|Delete)\s+File:\s+(.+)$/);
            if (m) files.push(basename(m[1].trim()));
            if (files.length >= 3) break;
        }
        if (files.length > 0) return files.join(', ');
    }

    // Generic fallbacks for sub-agent / tool delegation calls so the chip
    // is not empty when no recognised field is present.
    if (typeof input.description === 'string' && input.description.trim().length > 0) {
        const d = input.description.trim();
        return d.length > 60 ? `${d.slice(0, 60)}…` : d;
    }
    if (typeof input.subject === 'string' && input.subject.trim().length > 0) {
        const s = input.subject.trim();
        return s.length > 60 ? `${s.slice(0, 60)}…` : s;
    }
    if (typeof input.name === 'string' && input.name.trim().length > 0) {
        return input.name.trim();
    }

    return '';
}

/** True when the tool input contains a shell command (Bash, codex shell, etc.). */
export function isCommandTool(toolInput: unknown): boolean {
    if (!toolInput || typeof toolInput !== 'object') return false;
    const input = toolInput as Record<string, unknown>;
    return typeof input.command === 'string' || typeof input.cmd === 'string';
}

const WRITE_TOOL_NAMES = new Set(['Write', 'WriteFile', 'write_file']);

/** Check if a tool call is a Write operation targeting a markdown file with content. */
export function isWriteMarkdownTool(
    toolName: string | undefined,
    toolInput: unknown
): boolean {
    if (!toolName || !WRITE_TOOL_NAMES.has(toolName)) return false;
    if (!toolInput || typeof toolInput !== 'object') return false;
    const input = toolInput as Record<string, unknown>;
    return (
        typeof input.file_path === 'string' &&
        input.file_path.endsWith('.md') &&
        typeof input.content === 'string'
    );
}
