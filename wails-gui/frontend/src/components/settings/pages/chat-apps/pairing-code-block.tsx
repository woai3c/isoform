// PairingCodeBlock shows a chat connector's pairing code until its owner sends
// it, then the idle note (for example, that the connector is paired). Telegram,
// Slack, Discord, Feishu and WeChat all use it, so the owner step reads the
// same in every connector.
export function PairingCodeBlock({
    pairingCode,
    title,
    hint,
    idleNote,
}: {
    pairingCode?: string;
    title: string;
    hint: string;
    idleNote?: string | null;
}) {
    if (pairingCode) {
        return (
            <div className="flex flex-col gap-1.5 rounded-md border border-border bg-panel/30 px-3 py-2.5">
                <p className="text-xs font-medium text-ink">{title}</p>
                <p className="text-lg font-semibold tracking-widest text-ink select-all">{pairingCode}</p>
                <p className="text-xs text-muted leading-relaxed">{hint}</p>
            </div>
        );
    }
    if (!idleNote) return null;
    return (
        <div className="rounded-md border border-border bg-panel/30 px-3 py-2.5 text-xs text-muted leading-relaxed">
            {idleNote}
        </div>
    );
}
