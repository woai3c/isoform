import { useCallback, useEffect, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import {
    ConfirmSMSVerification,
    StartSMSVerification,
    StopSMSChannel,
    type SMSChannelStatus,
} from "@/lib/wails-shim-app";
import { GetSMSChannelStatus } from "@/lib/page-queries";
import { pageQueryCache } from "@/lib/query-cache";
import { SettingsCard } from "./settings/settings-primitives";
import { NUMBER_INPUT_CLASS, normalizeNumber } from "./text-messages-settings";
import { Button } from "./ui/button";

// Mirrors the relay's E.164 check (relay/internal/sms/text.go).
const NUMBER_PATTERN = /^\+[1-9][0-9]{7,14}$/;
const CODE_PATTERN = /^[0-9]{6}$/;

// The codes a failed call rejects with (wails-gui/sms_channel.go and
// PROTOCOL.md section 18.2), each with its own sentence.
const SMS_ERROR_KEYS = {
    offline: "smsTexts.errors.offline",
    not_configured: "smsTexts.errors.notConfigured",
    invalid_number: "smsTexts.errors.invalidNumber",
    unsupported_country: "smsTexts.errors.unsupportedCountry",
    invalid_code: "smsTexts.errors.invalidCode",
    code_wrong: "smsTexts.errors.codeWrong",
    code_spent: "smsTexts.errors.codeSpent",
    no_pending: "smsTexts.errors.noPending",
    opted_out: "smsTexts.errors.optedOut",
    too_many: "smsTexts.errors.tooMany",
    busy: "smsTexts.errors.busy",
    send_failed: "smsTexts.errors.sendFailed",
    quota: "smsTexts.errors.quota",
    not_entitled: "smsTexts.errors.notEntitled",
} as const;

/** Turns a text-subscription error code into a sentence in the user's language. */
export function describeSMSError(raw: string, t: TFunction<"settings">): string {
    const code = raw.trim() as keyof typeof SMS_ERROR_KEYS;
    return Object.prototype.hasOwnProperty.call(SMS_ERROR_KEYS, code)
        ? t(SMS_ERROR_KEYS[code])
        : t("smsTexts.errors.unknown");
}

type BusyAction = "start" | "confirm" | "stop";

/** Whether a code is out and still good. */
function codeOut(status: SMSChannelStatus): boolean {
    if (!status.codeExpiresAt) return false;
    const expires = Date.parse(status.codeExpiresAt);
    return Number.isFinite(expires) && expires > Date.now();
}

/**
 * Get Isoform by text message: the user's number, the code that proves it is
 * theirs, what Isoform texts and how to stop it. The relay sends the texts;
 * the $2 plan that pays for them is not live yet.
 */
export function SmsTextSettings({ existingOnly = false }: { existingOnly?: boolean } = {}) {
    const { t } = useTranslation("settings");
    const [status, setStatus] = useState<SMSChannelStatus | null>(
        // A reopen paints the saved status at once; refresh() revalidates it.
        () => pageQueryCache.peek<SMSChannelStatus>("GetSMSChannelStatus") ?? null,
    );
    const [, setLoadFailed] = useState(false);
    const [numberDraft, setNumberDraft] = useState("");
    const [codeDraft, setCodeDraft] = useState("");
    // True while the user enters a different number beside an active one, or
    // leaves a code behind to use another number.
    const [editing, setEditing] = useState(false);
    // True when the user set a code for a new number aside to see the active one.
    const [codeSetAside, setCodeSetAside] = useState(false);
    const [busy, setBusy] = useState<BusyAction | null>(null);
    const [error, setError] = useState<string | null>(null);

    const mounted = useRef(false);
    const busyRef = useRef(false);
    const readVersion = useRef(0);

    const refresh = useCallback(async () => {
        if (busyRef.current) return;
        const version = ++readVersion.current;
        try {
            const next = await GetSMSChannelStatus();
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
        // A STOP or START texted from the phone changes the state, so re-read on focus.
        const onFocus = () => {
            pageQueryCache.invalidate(["GetSMSChannelStatus"]);
            void refresh();
        };
        window.addEventListener("focus", onFocus);
        return () => {
            mounted.current = false;
            readVersion.current += 1;
            window.removeEventListener("focus", onFocus);
        };
    }, [refresh]);

    // One call at a time; the status the binding returns replaces the card's.
    const run = async (action: BusyAction, call: () => Promise<SMSChannelStatus>, onDone?: () => void) => {
        if (busyRef.current) return;
        busyRef.current = true;
        readVersion.current += 1;
        setBusy(action);
        setError(null);
        let failed = false;
        try {
            const next = await call();
            pageQueryCache.set("GetSMSChannelStatus", [], next);
            if (mounted.current) {
                setStatus(next);
                onDone?.();
            }
        } catch (err) {
            failed = true;
            if (mounted.current) setError(describeSMSError(err instanceof Error ? err.message : String(err), t));
        } finally {
            busyRef.current = false;
            if (mounted.current) setBusy(null);
        }
        // A refusal returns no status, so read the current one back.
        if (failed && mounted.current) {
            pageQueryCache.invalidate(["GetSMSChannelStatus"]);
            await refresh();
        }
    };

    const normalized = normalizeNumber(numberDraft);
    const numberValid = NUMBER_PATTERN.test(normalized);
    const numberHint = numberDraft.trim() !== "" && !numberValid;
    const codeValid = CODE_PATTERN.test(codeDraft.trim());

    const sendCode = () => {
        if (!numberValid) return;
        void run("start", () => StartSMSVerification(normalized), () => {
            setEditing(false);
            setCodeSetAside(false);
            setCodeDraft("");
        });
    };
    const confirmCode = () => {
        if (!codeValid) return;
        void run("confirm", () => ConfirmSMSVerification(codeDraft.trim()), () => {
            setCodeDraft("");
            setNumberDraft("");
            setEditing(false);
        });
    };
    const stopTexts = () => {
        void run("stop", () => StopSMSChannel(), () => {
            setEditing(false);
            setNumberDraft("");
            setCodeDraft("");
        });
    };

    const errorLine = error && (
        <p role="alert" className="text-xs text-error">
            {error}
        </p>
    );

    const numberForm = (showCancel: boolean) => (
        <div className="flex flex-col gap-2">
            <label htmlFor="sms-text-number" className="settings-row-title">
                {t("smsTexts.numberLabel")}
            </label>
            <input
                id="sms-text-number"
                type="tel"
                inputMode="tel"
                autoComplete="tel"
                spellCheck={false}
                placeholder={t("smsTexts.numberPlaceholder")}
                value={numberDraft}
                disabled={busy !== null}
                aria-invalid={numberHint}
                onChange={(event) => setNumberDraft(event.target.value)}
                onKeyDown={(event) => {
                    if (event.key === "Enter") sendCode();
                }}
                className={NUMBER_INPUT_CLASS}
            />
            {numberHint && (
                <p role="alert" className="text-xs text-error">
                    {t("smsTexts.numberHint")}
                </p>
            )}
            <p className="settings-row-desc">{t("smsTexts.privacy")}</p>
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
                        {t("smsTexts.cancel")}
                    </Button>
                )}
                <Button type="button" size="sm" disabled={busy !== null || !numberValid} onClick={sendCode}>
                    {busy === "start" && <Loader2 className="size-3.5 animate-spin" />}
                    {t("smsTexts.sendCode")}
                </Button>
            </div>
        </div>
    );

    const body = (() => {
        if (status === null) return null;
        if (existingOnly && !status.number) return null;
        if (!existingOnly && !status.available) {
            return <p className="settings-row-desc py-2">{t("smsTexts.unavailableOff")}</p>;
        }
        if (!existingOnly && status.lastError === "not_configured") {
            return <p className="settings-row-desc py-2">{t("smsTexts.unavailableServer")}</p>;
        }
        if (!existingOnly && codeOut(status) && !editing && !(codeSetAside && status.state === "active")) {
            return (
                <div className="flex flex-col gap-2">
                    {status.state === "active" && (
                        <p className="settings-row-desc">{t("smsTexts.stillActive", { number: status.number })}</p>
                    )}
                    <p className="text-sm text-ink">
                        {t("smsTexts.codeSentTo", { number: status.pendingNumber || t("smsTexts.yourNumber") })}
                    </p>
                    <label htmlFor="sms-text-code" className="settings-row-title">
                        {t("smsTexts.codeLabel")}
                    </label>
                    <input
                        id="sms-text-code"
                        type="text"
                        inputMode="numeric"
                        autoComplete="one-time-code"
                        spellCheck={false}
                        maxLength={6}
                        placeholder={t("smsTexts.codePlaceholder")}
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
                        {status.state === "active" && (
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                disabled={busy !== null}
                                onClick={() => {
                                    setCodeSetAside(true);
                                    setError(null);
                                }}
                            >
                                {t("smsTexts.cancel")}
                            </Button>
                        )}
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={busy !== null}
                            onClick={() => {
                                setEditing(true);
                                setError(null);
                                setNumberDraft(status.pendingNumber);
                            }}
                        >
                            {t("smsTexts.useAnotherNumber")}
                        </Button>
                        <Button type="button" size="sm" disabled={busy !== null || !codeValid} onClick={confirmCode}>
                            {busy === "confirm" && <Loader2 className="size-3.5 animate-spin" />}
                            {t("smsTexts.confirm")}
                        </Button>
                    </div>
                </div>
            );
        }
        if (status.state === "active" && !editing) {
            return (
                <div className="flex flex-col gap-2">
                    <p className="text-sm text-ink">{t("smsTexts.activeOn", { number: status.number })}</p>
                    <p className="settings-row-desc">
                        {t("smsTexts.usage", {
                            sent: status.sentToday,
                            daily: status.dailyCap,
                            month: status.sentThisMonth,
                        })}
                    </p>
                    {!existingOnly && <p className="settings-row-desc">{t("smsTexts.replyHint")}</p>}
                    {existingOnly && status.lastError && <p role="alert" className="text-xs text-error">{describeSMSError(status.lastError, t)}</p>}
                    {errorLine}
                    <div className="flex flex-wrap items-center justify-end gap-2">
                        {!existingOnly && <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={busy !== null}
                            onClick={() => {
                                setEditing(true);
                                setError(null);
                                setNumberDraft("");
                            }}
                        >
                            {t("smsTexts.changeNumber")}
                        </Button>}
                        <Button type="button" variant="outline" size="sm" disabled={busy !== null} onClick={stopTexts}>
                            {busy === "stop" && <Loader2 className="size-3.5 animate-spin" />}
                            {t("smsTexts.stop")}
                        </Button>
                    </div>
                </div>
            );
        }
        if ((status.state === "stopped" || existingOnly) && !editing) {
            return (
                <div className="flex flex-col gap-2">
                    <p className="text-sm text-ink">{t(status.state === "stopped" ? "smsTexts.stoppedBody" : "comingSoon.savedNumber", { number: status.number })}</p>
                    {errorLine}
                    <div className="flex flex-wrap items-center justify-end gap-2">
                        <Button type="button" variant="outline" size="sm" disabled={busy !== null} onClick={stopTexts}>
                            {busy === "stop" && <Loader2 className="size-3.5 animate-spin" />}
                            {t("smsTexts.remove")}
                        </Button>
                    </div>
                </div>
            );
        }
        return numberForm(editing && (status.state === "active" || status.state === "stopped" || codeOut(status)));
    })();

    if (existingOnly && status && !status.number) return null;

    if (body === null) return null;
    return (
        <SettingsCard title={t("smsTexts.title")} description={existingOnly ? undefined : t("smsTexts.description")}>
            <div className="flex flex-col gap-3">
                {body}
                {!existingOnly && <p className="settings-row-desc border-t border-border pt-3">
                    {t("smsTexts.plan", { daily: status?.dailyCap || 10 })}
                </p>}
            </div>
        </SettingsCard>
    );
}
