import { useCallback, useEffect, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
    ConfirmEmailVerification,
    GetEmailChannelStatus,
    StartEmailVerification,
    StopEmailChannel,
    type EmailChannelStatus,
} from "@/lib/wails-shim-app";
import { SettingsCard } from "./settings/settings-primitives";
import { NUMBER_INPUT_CLASS } from "./text-messages-settings";
import { Button } from "./ui/button";

// A plain address; the Mac and the relay check it again (email_channel.go).
const ADDRESS_PATTERN = /^[^\s@<>,;"]+@[^\s@<>,;"]+\.[^\s@<>,;"]+$/;
const CODE_PATTERN = /^[0-9]{6}$/;

// The codes a failed call rejects with (wails-gui/email_channel.go and
// PROTOCOL.md section 19.2), each with its own sentence.
const EMAIL_ERROR_KEYS = {
    offline: "emailResults.errors.offline",
    not_configured: "emailResults.errors.notConfigured",
    invalid_address: "emailResults.errors.invalidAddress",
    invalid_code: "emailResults.errors.invalidCode",
    code_wrong: "emailResults.errors.codeWrong",
    code_spent: "emailResults.errors.codeSpent",
    no_pending: "emailResults.errors.noPending",
    too_many: "emailResults.errors.tooMany",
    busy: "emailResults.errors.busy",
    send_failed: "emailResults.errors.sendFailed",
    quota: "emailResults.errors.quota",
} as const;

/** Turns an email-subscription error code into a sentence in the user's language. */
export function describeEmailError(raw: string, t: TFunction<"settings">): string {
    const code = raw.trim() as keyof typeof EMAIL_ERROR_KEYS;
    return Object.prototype.hasOwnProperty.call(EMAIL_ERROR_KEYS, code)
        ? t(EMAIL_ERROR_KEYS[code])
        : t("emailResults.errors.unknown");
}

type BusyAction = "start" | "confirm" | "stop";

function codeOut(status: EmailChannelStatus): boolean {
    if (!status.codeExpiresAt) return false;
    const expires = Date.parse(status.codeExpiresAt);
    return Number.isFinite(expires) && expires > Date.now();
}

/**
 * Results by email: the user's own address, the code that proves it is
 * theirs, and how to stop. Only playbook results the user chose to have
 * emailed go there, and only to this address.
 */
export function EmailSettings() {
    const { t } = useTranslation("settings");
    const [status, setStatus] = useState<EmailChannelStatus | null>(null);
    const [loadFailed, setLoadFailed] = useState(false);
    const [addressDraft, setAddressDraft] = useState("");
    const [codeDraft, setCodeDraft] = useState("");
    const [editing, setEditing] = useState(false);
    const [busy, setBusy] = useState<BusyAction | null>(null);
    const [error, setError] = useState<string | null>(null);

    const mounted = useRef(false);
    const busyRef = useRef(false);
    const readVersion = useRef(0);

    const refresh = useCallback(async () => {
        if (busyRef.current) return;
        const version = ++readVersion.current;
        try {
            const next = await GetEmailChannelStatus();
            if (mounted.current && version === readVersion.current) {
                setStatus(next);
                setLoadFailed(false);
            }
        } catch {
            if (mounted.current && version === readVersion.current) setLoadFailed(true);
        }
    }, []);

    useEffect(() => {
        mounted.current = true;
        void refresh();
        return () => {
            mounted.current = false;
            readVersion.current += 1;
        };
    }, [refresh]);

    const run = async (action: BusyAction, call: () => Promise<EmailChannelStatus>, onDone?: () => void) => {
        if (busyRef.current) return;
        busyRef.current = true;
        readVersion.current += 1;
        setBusy(action);
        setError(null);
        let failed = false;
        try {
            const next = await call();
            if (mounted.current) {
                setStatus(next);
                onDone?.();
            }
        } catch (err) {
            failed = true;
            if (mounted.current) setError(describeEmailError(err instanceof Error ? err.message : String(err), t));
        } finally {
            busyRef.current = false;
            if (mounted.current) setBusy(null);
        }
        if (failed && mounted.current) await refresh();
    };

    const address = addressDraft.trim();
    const addressValid = ADDRESS_PATTERN.test(address);
    const addressHint = address !== "" && !addressValid;
    const codeValid = CODE_PATTERN.test(codeDraft.trim());

    const sendCode = () => {
        if (!addressValid) return;
        void run("start", () => StartEmailVerification(address), () => {
            setEditing(false);
            setCodeDraft("");
        });
    };
    const confirmCode = () => {
        if (!codeValid) return;
        void run("confirm", () => ConfirmEmailVerification(codeDraft.trim()), () => {
            setCodeDraft("");
            setAddressDraft("");
            setEditing(false);
        });
    };
    const stop = () => {
        void run("stop", () => StopEmailChannel(), () => {
            setEditing(false);
            setAddressDraft("");
            setCodeDraft("");
        });
    };

    const errorLine = error && (
        <p role="alert" className="text-xs text-error">
            {error}
        </p>
    );

    const addressForm = (showCancel: boolean) => (
        <div className="flex flex-col gap-2">
            <label htmlFor="email-results-address" className="settings-row-title">
                {t("emailResults.addressLabel")}
            </label>
            <input
                id="email-results-address"
                type="email"
                inputMode="email"
                autoComplete="email"
                spellCheck={false}
                placeholder={t("emailResults.addressPlaceholder")}
                value={addressDraft}
                disabled={busy !== null}
                aria-invalid={addressHint}
                onChange={(event) => setAddressDraft(event.target.value)}
                onKeyDown={(event) => {
                    if (event.key === "Enter") sendCode();
                }}
                className={NUMBER_INPUT_CLASS}
            />
            {addressHint && (
                <p role="alert" className="text-xs text-error">
                    {t("emailResults.addressHint")}
                </p>
            )}
            <p className="settings-row-desc">{t("emailResults.privacy")}</p>
            {errorLine}
            <div className="flex flex-wrap items-center justify-end gap-2">
                {showCancel && (
                    <Button
                        type="button"
                        variant="outline"
                        size="sm"
                        disabled={busy !== null}
                        onClick={() => {
                            setEditing(false);
                            setError(null);
                        }}
                    >
                        {t("emailResults.cancel")}
                    </Button>
                )}
                <Button type="button" size="sm" disabled={busy !== null || !addressValid} onClick={sendCode}>
                    {busy === "start" && <Loader2 className="size-3.5 animate-spin" />}
                    {t("emailResults.sendCode")}
                </Button>
            </div>
        </div>
    );

    const body = (() => {
        if (status === null) {
            return loadFailed ? (
                <div className="flex flex-wrap items-center gap-3 py-2">
                    <p role="alert" className="text-xs text-error">
                        {t("emailResults.loadFailed")}
                    </p>
                    <Button type="button" variant="outline" size="sm" onClick={() => void refresh()}>
                        {t("emailResults.retry")}
                    </Button>
                </div>
            ) : (
                <p role="status" className="settings-row-desc py-2">
                    {t("emailResults.loading")}
                </p>
            );
        }
        if (!status.available) {
            return <p className="settings-row-desc py-2">{t("emailResults.unavailableOff")}</p>;
        }
        if (status.lastError === "not_configured") {
            return <p className="settings-row-desc py-2">{t("emailResults.unavailableServer")}</p>;
        }
        if (codeOut(status) && !editing) {
            return (
                <div className="flex flex-col gap-2">
                    {status.state === "active" && (
                        <p className="settings-row-desc">{t("emailResults.stillActive", { address: status.address })}</p>
                    )}
                    <p className="text-sm text-ink">
                        {t("emailResults.codeSentTo", { address: status.pendingAddress || t("emailResults.yourAddress") })}
                    </p>
                    <label htmlFor="email-results-code" className="settings-row-title">
                        {t("emailResults.codeLabel")}
                    </label>
                    <input
                        id="email-results-code"
                        type="text"
                        inputMode="numeric"
                        autoComplete="one-time-code"
                        spellCheck={false}
                        maxLength={6}
                        placeholder={t("emailResults.codePlaceholder")}
                        value={codeDraft}
                        disabled={busy !== null}
                        onChange={(event) => setCodeDraft(event.target.value.replace(/[^0-9]/g, ""))}
                        onKeyDown={(event) => {
                            if (event.key === "Enter") confirmCode();
                        }}
                        className={NUMBER_INPUT_CLASS}
                    />
                    {errorLine}
                    <div className="flex flex-wrap items-center justify-end gap-2">
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={busy !== null}
                            onClick={() => {
                                setEditing(true);
                                setError(null);
                                setAddressDraft(status.pendingAddress);
                            }}
                        >
                            {t("emailResults.useAnotherAddress")}
                        </Button>
                        <Button type="button" size="sm" disabled={busy !== null || !codeValid} onClick={confirmCode}>
                            {busy === "confirm" && <Loader2 className="size-3.5 animate-spin" />}
                            {t("emailResults.confirm")}
                        </Button>
                    </div>
                </div>
            );
        }
        if (status.state === "active" && !editing) {
            return (
                <div className="flex flex-col gap-2">
                    <p className="text-sm text-ink">{t("emailResults.activeOn", { address: status.address })}</p>
                    <p className="settings-row-desc">
                        {t("emailResults.usage", { sent: status.sentToday, daily: status.dailyCap })}
                    </p>
                    {errorLine}
                    <div className="flex flex-wrap items-center justify-end gap-2">
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={busy !== null}
                            onClick={() => {
                                setEditing(true);
                                setError(null);
                                setAddressDraft("");
                            }}
                        >
                            {t("emailResults.changeAddress")}
                        </Button>
                        <Button type="button" variant="outline" size="sm" disabled={busy !== null} onClick={stop}>
                            {busy === "stop" && <Loader2 className="size-3.5 animate-spin" />}
                            {t("emailResults.stop")}
                        </Button>
                    </div>
                </div>
            );
        }
        return addressForm(editing && (status.state === "active" || codeOut(status)));
    })();

    return (
        <SettingsCard title={t("emailResults.title")} description={t("emailResults.description")}>
            <div className="flex flex-col gap-3">{body}</div>
        </SettingsCard>
    );
}
