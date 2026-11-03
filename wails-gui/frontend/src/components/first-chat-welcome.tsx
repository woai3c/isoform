import { Activity, ArrowRight, MessageSquare } from 'lucide-react';

interface ConnectedProviders {
    slack?: string;
    feishu?: string;
}

interface FirstChatWelcomeProps {
    connectedProviders: ConnectedProviders;
    onFillComposer?: (prompt: string) => void;
}

const SUGGESTIONS = [
    {
        icon: MessageSquare,
        label: "I spend too much time on...",
        prompt: "I spend too much time on ",
    },
    {
        icon: ArrowRight,
        label: "Take over this task...",
        prompt: "Take over this task: ",
    },
    {
        icon: Activity,
        label: "Every week I manually...",
        prompt: "Every week I manually ",
    },
];

/** Build the markdown content for the synthetic welcome assistant message. */
export function buildWelcomeContent(
    connectedProviders: ConnectedProviders,
    firstName?: string,
): string {
    const providerNames = Object.entries(connectedProviders)
        .filter(([, v]) => !!v)
        .map(([key, name]) => {
            if (key === 'slack') return name === 'Slack' ? 'Slack' : `Slack (${name})`;
            if (key === 'feishu') return 'Feishu';
            return name;
        });

    const greeting = firstName ? `Hey ${firstName}!` : 'Hey!';
    const lines = [
        `**${greeting}**`,
        '',
        'I can help with the work in front of you, take over a bounded task, or set up recurring follow-through.',
    ];

    if (providerNames.length > 0) {
        lines.push('');
        lines.push(`Already learning from your ${providerNames.join(' and ')} messages.`);
    }

    lines.push('');
    lines.push('What slows you down?');

    return lines.join('\n');
}

/** Suggestion chips shown below the welcome message. */
export function WelcomeSuggestions({ onFillComposer }: Pick<FirstChatWelcomeProps, 'onFillComposer'>) {
    return (
        <div className="flex flex-wrap gap-2 pl-9">
            {SUGGESTIONS.map((s) => (
                <button
                    key={s.label}
                    type="button"
                    onClick={() => onFillComposer?.(s.prompt)}
                    className="flex items-center gap-2 px-3 py-2 rounded-xl border border-border bg-panel hover:bg-surface-hover hover:border-border/80 transition-all text-left group cursor-pointer"
                >
                    <s.icon className="h-3.5 w-3.5 text-muted group-hover:text-accent shrink-0 transition-colors" />
                    <span className="text-xs text-muted group-hover:text-ink transition-colors">{s.label}</span>
                </button>
            ))}
        </div>
    );
}
