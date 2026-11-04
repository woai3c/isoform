import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import {
    ConnectMCPReadAssistant,
    GetMCPReadConnections,
    RevokeMCPReadAssistant,
    type MCPReadConnection,
} from "@/lib/wails-shim-app";
import { SettingsCard } from "./settings/settings-primitives";
import { Button } from "./ui/button";
import { SettingsFailureLine, SettingsPlaceholderRows } from "./settings/settings-failure";
import { useSettingsDevTools } from "./settings/settings-dev-tools";

type ConnectionAction = "connect" | "revoke";
type ConnectionError = "connectFailed" | "revokeFailed";

export function AssistantMCPSettings() {
    const { t } = useTranslation("settings");
    const devTools = useSettingsDevTools();
    const [connections, setConnections] = useState<MCPReadConnection[]>([]);
    const [loading, setLoading] = useState(true);
    const [loadFailed, setLoadFailed] = useState(false);
    const [reload, setReload] = useState(0);
    const [pending, setPending] = useState<{ client: string; action: ConnectionAction } | null>(null);
    const [errors, setErrors] = useState<Record<string, ConnectionError>>({});
    const actionPending = useRef(false);

    useEffect(() => {
        let active = true;
        setLoading(true);
        setLoadFailed(false);
        GetMCPReadConnections()
            .then((result) => {
                if (active) setConnections(result ?? []);
            })
            .catch(() => {
                if (active) setLoadFailed(true);
            })
            .finally(() => {
                if (active) setLoading(false);
            });
        return () => { active = false; };
    }, [reload]);

    const changeConnection = useCallback(async (client: string, action: ConnectionAction) => {
        if (actionPending.current) return;
        actionPending.current = true;
        setPending({ client, action });
        setErrors((current) => {
            const next = { ...current };
            delete next[client];
            return next;
        });
        try {
            const updated = await (action === "connect"
                ? ConnectMCPReadAssistant(client)
                : RevokeMCPReadAssistant(client));
            setConnections((current) => current.map((entry) => entry.client === client ? updated : entry));
        } catch {
            try {
                // Token revocation can succeed while registration cleanup fails.
                // Refresh safe status without exposing raw CLI output.
                setConnections(await GetMCPReadConnections() ?? []);
            } catch {
                // Preserve the displayed state when the status read fails.
            }
            setErrors((current) => ({
                ...current,
                [client]: action === "connect" ? "connectFailed" : "revokeFailed",
            }));
        } finally {
            actionPending.current = false;
            setPending(null);
        }
    }, []);

    return (
        <SettingsCard
            title={t("assistantMcp.title")}
            description={t("assistantMcp.description")}
        >
            {loading ? (
                <SettingsPlaceholderRows rows={2} />
            ) : loadFailed ? (
                <SettingsFailureLine failure={{ text: t("assistantMcp.loadFailed"), retry: () => setReload((value) => value + 1) }} />
            ) : (
                <div className="space-y-4">
                    {connections.map((connection) => {
                        const name = connection.client === "claude-code"
                            ? t("assistantMcp.claudeCode")
                            : t("assistantMcp.codex");
                        const busy = pending?.client === connection.client;
                        const error = errors[connection.client];
                        return (
                            <article key={connection.client} aria-label={name} className="space-y-2">
                                <div className="flex items-center justify-between gap-3">
                                    <div className="flex flex-wrap items-center gap-2">
                                        <h3 className="settings-row-title">{name}</h3>
                                        <span className={`inline-flex rounded-full px-2 py-0.5 text-[10px] font-medium ${connection.connected ? "bg-success-bg text-success" : "bg-ink/[0.05] text-ink/55"}`} role="status">
                                            {connection.connected ? t("assistantMcp.connected") : t("assistantMcp.notConnected")}
                                        </span>
                                    </div>
                                    <div className="flex shrink-0 items-center gap-2">
                                        {!connection.connected && (
                                            <Button
                                                type="button"
                                                variant="connect"
                                                size="sm"
                                                disabled={pending !== null || !connection.available || connection.issue === "name_conflict"}
                                                onClick={() => void changeConnection(connection.client, "connect")}
                                            >
                                                {busy && pending.action === "connect" ? t("assistantMcp.connecting") : t("assistantMcp.connect")}
                                            </Button>
                                        )}
                                        {(connection.authorized || connection.connected || error === "revokeFailed" || (busy && pending.action === "revoke")) && (
                                            <Button
                                                type="button"
                                                variant="outline"
                                                size="sm"
                                                disabled={pending !== null}
                                                onClick={() => void changeConnection(connection.client, "revoke")}
                                            >
                                                {busy && pending.action === "revoke" ? t("assistantMcp.revoking") : t("assistantMcp.revoke")}
                                            </Button>
                                        )}
                                    </div>
                                </div>
                                {devTools && <div className="space-y-1">
                                    <p className="text-[11px] text-muted">{t("assistantMcp.command")}</p>
                                    <pre className="select-text whitespace-pre-wrap break-all rounded-md border border-border bg-panel p-2 font-mono text-[11px] text-ink">
                                        <code>{connection.command}</code>
                                    </pre>
                                </div>}
                                {!connection.available && (
                                    <p className="text-xs text-muted">
                                        {connection.availabilityReason === "isoform_missing"
                                            ? t("assistantMcp.isoformMissing")
                                            : t("assistantMcp.assistantMissing", { assistant: name })}
                                    </p>
                                )}
                                {connection.issue === "name_conflict" && (
                                    <p className="text-[12px] text-copper" role="alert">
                                        {t("assistantMcp.nameConflict", { command: connection.conflictCommand || t("assistantMcp.otherCommand") })}
                                    </p>
                                )}
                                <SettingsFailureLine failure={error ? { text: t(`assistantMcp.${error}`), retry: () => void changeConnection(connection.client, error === "connectFailed" ? "connect" : "revoke") } : null} />
                            </article>
                        );
                    })}
                    <p className="text-[11px] leading-relaxed text-muted">{t("assistantMcp.privacy")}</p>
                </div>
            )}
        </SettingsCard>
    );
}
