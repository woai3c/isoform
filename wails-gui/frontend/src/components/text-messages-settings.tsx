import { useCallback, useEffect, useRef, useState } from "react";
import type { TFunction } from "i18next";
import { Loader2 } from "lucide-react";
import { useTranslation } from "react-i18next";
import { OpenPermissionSettings, RequestPermission } from "../../wailsjs/go/main/App";
import {
    SendTestText,
    SetTextChannel,
    StartTextVerification,
    ConfirmTextVerification,
    type TextChannelStatus,
} from "@/lib/wails-shim-app";
import { GetTextChannelStatus } from "@/lib/page-queries";
import { pageQueryCache } from "@/lib/query-cache";
import { SettingsCard, SettingsRow } from "./settings/settings-primitives";
import { Button } from "./ui/button";
import { Switch } from "./ui/switch";

// Mirrors textChannelDailyLimit in wails-gui/text_channel.go.
const TEXT_DAILY_LIMIT = 3;
// Mirrors textChannelNumberPattern in wails-gui/text_channel.go: E.164, 8 to 15 digits.
const NUMBER_PATTERN = /^\+[1-9][0-9]{7,14}$/;
// The Messages item in GetPlatformPermissions (permissions_darwin.go).
const MESSAGES_PERMISSION_KEY = "automation_messages";
export const NUMBER_INPUT_CLASS =
    "w-full rounded-md border border-border bg-panel px-3 py-1.5 text-xs tabular-nums text-ink placeholder:text-muted focus:outline-none focus:ring-1 focus:ring-accent disabled:opacity-50";

type ErrorScope = "consent" | "enable" | "names" | "grant" | "verify";
type BusyAction = "consent" | "enable" | "names" | "grant" | "test" | "verify";
type TestOutcome = { kind: "sent" } | { kind: "failed"; reason: string };

/** Removes the spaces, dashes, dots and brackets people type around a phone number. */
export function normalizeNumber(value: string): string {
    return value.replace(/[\s\-().]/g, "");
}

// The text channel reports failures as English phrases (errTextChannel* in
// wails-gui/text_channel.go, messagesFailure in text_channel_darwin.go). Each
// known phrase maps to a translated sentence; anything else gets a generic one.
const TEXT_ERRORS = [
    { match: /verify your phone number/i, key: "textMessages.errorUnverified" },
    { match: /verification code is not right/i, key: "textMessages.errorCodeWrong" },
    { match: /verification code expired/i, key: "textMessages.errorCodeSpent" },
    { match: /requesting another text verification/i, key: "textMessages.errorCodeSoon" },
    { match: /Messages is not signed in/i, key: "textMessages.errorSignedOut" },
    { match: /text messages are paused/i, key: "textMessages.errorPaused" },
    { match: /daily text limit/i, key: "textMessages.errorDailyLimit" },
    { match: /text was sent recently/i, key: "textMessages.errorCooldown" },
    { match: /before sending another test text/i, key: "textMessages.errorTestSoon" },
    { match: /automation for messages is not allowed/i, key: "textMessages.errorNotAllowed" },
    { match: /did not answer within/i, key: "textMessages.errorTimedOut" },
    { match: /international form/i, key: "textMessages.errorNumber" },
    { match: /text messages are off/i, key: "textMessages.errorOff" },
    { match: /enter a phone number before/i, key: "textMessages.errorNoNumber" },
    { match: /accept the text message terms/i, key: "textMessages.errorNoConsent" },
    { match: /not available on this computer/i, key: "textMessages.errorUnsupported" },
    { match: /text message (settings|state)/i, key: "textMessages.errorStorage" },
] as const;

// "Messages could not send the text (error -1728)": the code is the only detail kept.
const SEND_FAILED_PATTERN = /could not send the text(?: \(error (-?\d+)\))?/i;

/** Turns a text-channel error into a sentence in the user's language. */
export function describeTextError(raw: string, t: TFunction<"settings">): string {
    for (const { match, key } of TEXT_ERRORS) {
        if (match.test(raw)) return t(key, { limit: TEXT_DAILY_LIMIT });
    }
    const sendFailed = SEND_FAILED_PATTERN.exec(raw);
    if (sendFailed) {
        return sendFailed[1]
            ? t("textMessages.errorNotSentCode", { code: sendFailed[1] })
            : t("textMessages.errorNotSent");
    }
    return t("textMessages.errorUnknown");
}

/** State and actions shared by the Text messages card and its Advanced details. */
export interface TextMessagesController {
    status: TextChannelStatus | null;
    loadFailed: boolean;
    busy: BusyAction | null;
    errors: Partial<Record<ErrorScope, string>>;
    testOutcome: TestOutcome | null;
    shownNumber: string;
    numberValid: boolean;
    numberChanged: boolean;
    canEnable: boolean;
    showConsent: boolean;
    refresh: () => Promise<void>;
    editNumber: (value: string) => void;
    dismissConsent: () => void;
    allowTexts: () => void;
    setEnabled: (enabled: boolean) => void;
    setIncludeNames: (includeNames: boolean) => void;
    sendTest: () => Promise<void>;
    grantMessages: () => Promise<void>;
    code: string;
    editCode: (value: string) => void;
    startVerification: () => Promise<void>;
    confirmVerification: () => Promise<void>;
}

/**
 * Shares saved text-message state and serialized actions across Settings views.
 */
export function useTextMessages(): TextMessagesController {
    const { t } = useTranslation("settings");

    const [status, setStatus] = useState<TextChannelStatus | null>(
        // A reopen paints the saved status at once; refresh() revalidates it.
        () => pageQueryCache.peek<TextChannelStatus>("GetTextChannelStatus") ?? null,
    );
    const [loadFailed, setLoadFailed] = useState(false);
    // null shows the saved number; a string is the user's unsaved edit.
    const [draft, setDraft] = useState<string | null>(null);
    const [consentDismissed, setConsentDismissed] = useState(false);
    const [busy, setBusy] = useState<BusyAction | null>(null);
    const [errors, setErrors] = useState<Partial<Record<ErrorScope, string>>>({});
    const [testOutcome, setTestOutcome] = useState<TestOutcome | null>(null);
    const [code, editCode] = useState("");

    const mounted = useRef(false);
    const busyRef = useRef(false);
    // Bumped by every read and write; a read whose version is stale when it resolves is dropped.
    const readVersion = useRef(0);

    const setError = useCallback((scope: ErrorScope, message?: string) => {
        setErrors(message ? { [scope]: message } : {});
    }, []);

    const refresh = useCallback(async () => {
        if (busyRef.current) return;
        const version = ++readVersion.current;
        try {
            const next = await GetTextChannelStatus();
            if (mounted.current && version === readVersion.current) {
                setStatus(next);
                setLoadFailed(false);
                if (next.permissionGranted) setErrors((current) => ({ ...current, grant: undefined }));
            }
        } catch {
            if (mounted.current && version === readVersion.current) setLoadFailed(true);
        }
    }, []);

    useEffect(() => {
        mounted.current = true;
        void refresh();
        // The Messages permission is often granted in System Settings, so re-read when the window regains focus.
        const onFocus = () => {
            pageQueryCache.invalidate(["GetTextChannelStatus"]);
            void refresh();
        };
        window.addEventListener("focus", onFocus);
        return () => {
            mounted.current = false;
            readVersion.current += 1;
            window.removeEventListener("focus", onFocus);
        };
    }, [refresh]);

    // One write at a time. The status the binding returns replaces the card's status.
    const runWrite = async (
        action: BusyAction,
        scope: ErrorScope,
        failure: string,
        write: () => Promise<TextChannelStatus>,
        onSaved?: () => void,
    ) => {
        if (busyRef.current) return;
        busyRef.current = true;
        readVersion.current += 1;
        setBusy(action);
        setError(scope);
        setTestOutcome(null);
        try {
            const next = await write();
            pageQueryCache.set("GetTextChannelStatus", [], next);
            if (mounted.current) {
                setStatus(next);
                onSaved?.();
            }
        } catch (err) {
            if (scope === "verify") {
                // A rejected code can have expired or used its final attempt.
                // Read the state once; never resend a code automatically.
                pageQueryCache.invalidate(["GetTextChannelStatus"]);
                const next = await GetTextChannelStatus().catch(() => null);
                if (mounted.current && next) setStatus(next);
            }
            if (mounted.current) setError(scope, scope === "verify"
                ? describeTextError(err instanceof Error ? err.message : String(err), t) : failure);
        } finally {
            busyRef.current = false;
            if (mounted.current) setBusy(null);
        }
    };

    const saved = status?.number ?? "";
    const shownNumber = draft ?? saved;
    const normalized = normalizeNumber(shownNumber);
    const numberValid = NUMBER_PATTERN.test(normalized);
    const numberChanged = normalized !== saved;
    // Consent belongs to the saved number. The backend clears it when the number changes.
    const canEnable = numberValid && !numberChanged && Boolean(status?.consentAccepted && status?.verified);
    const showConsent =
        Boolean(status?.supported) &&
        numberValid &&
        !consentDismissed &&
        (numberChanged || !status?.consentAccepted);

    const editNumber = (value: string) => {
        setDraft(value);
        setConsentDismissed(false);
    };

    const allowTexts = () => {
        if (!status || !numberValid) return;
        void runWrite(
            "consent",
            "consent",
            t("textMessages.saveFailed"),
            () =>
                SetTextChannel({
                    enabled: false,
                    number: normalized,
                    includeNames: status.includeNames,
                    acceptConsent: true,
                }),
            () => {
                setDraft(null);
                setConsentDismissed(false);
            },
        );
    };

    const setEnabled = (enabled: boolean) => {
        if (!status) return;
        void runWrite("enable", "enable", t("textMessages.enableFailed"), () =>
            SetTextChannel({
                enabled,
                number: status.number,
                includeNames: status.includeNames,
                acceptConsent: false,
            }),
        );
    };

    const setIncludeNames = (includeNames: boolean) => {
        if (!status) return;
        void runWrite("names", "names", t("textMessages.namesFailed"), () =>
            SetTextChannel({
                enabled: status.enabled,
                number: status.number,
                includeNames,
                acceptConsent: false,
            }),
        );
    };

    const sendTest = async () => {
        if (busyRef.current || !status) return;
        busyRef.current = true;
        readVersion.current += 1;
        setBusy("test");
        setErrors({});
        setTestOutcome(null);
        let outcome: TestOutcome;
        try {
            const next = await SendTestText();
            pageQueryCache.set("GetTextChannelStatus", [], next);
            if (mounted.current) setStatus(next);
            outcome = { kind: "sent" };
        } catch (err) {
            outcome = {
                kind: "failed",
                reason: describeTextError(err instanceof Error ? err.message : String(err), t),
            };
        } finally {
            busyRef.current = false;
            if (mounted.current) setBusy(null);
        }
        if (!mounted.current) return;
        // A failed test binding returns no status, so read the last error and the day's count back.
        if (outcome.kind === "failed") {
            pageQueryCache.invalidate(["GetTextChannelStatus"]);
            await refresh();
        }
        if (mounted.current) setTestOutcome(outcome);
    };

    const grantMessages = async () => {
        if (busyRef.current) return;
        busyRef.current = true;
        readVersion.current += 1;
        setBusy("grant");
        setError("grant");
        setTestOutcome(null);
        try {
            // Messages access is requested only from this Settings page.
            const granted = await RequestPermission(MESSAGES_PERMISSION_KEY);
            pageQueryCache.invalidate(["GetTextChannelStatus"]);
            const next = await GetTextChannelStatus();
            pageQueryCache.set("GetTextChannelStatus", [], next);
            if (mounted.current) setStatus(next);
            if (!granted || !next.permissionGranted) {
                await OpenPermissionSettings(MESSAGES_PERMISSION_KEY).catch(() => undefined);
                if (mounted.current) setError("grant", t("textMessages.grantNotGranted"));
            }
        } catch {
            if (mounted.current) setError("grant", t("textMessages.grantFailed"));
        } finally {
            busyRef.current = false;
            if (mounted.current) setBusy(null);
        }
    };

    const startVerification = async () => {
        await runWrite("verify", "verify", t("textMessages.errorUnknown"), StartTextVerification, () => editCode(""));
    };
    const confirmVerification = async () => {
        await runWrite("verify", "verify", t("textMessages.errorUnknown"), () => ConfirmTextVerification(code.trim()), () => editCode(""));
    };

    return {
        status,
        loadFailed,
        busy,
        errors,
        testOutcome,
        shownNumber,
        numberValid,
        numberChanged,
        canEnable,
        showConsent,
        refresh,
        editNumber,
        dismissConsent: () => setConsentDismissed(true),
        allowTexts,
        setEnabled,
        setIncludeNames,
        sendTest,
        grantMessages,
        code,
        editCode,
        startVerification,
        confirmVerification,
    };
}

/** Existing numbers keep their stop, privacy and recovery controls while new setup is paused. */
export function TextMessagesManagement({ controller }: { controller: TextMessagesController }) {
    const { t } = useTranslation("settings");
    const { status, busy, errors, testOutcome } = controller;
    if (!status) {
        return (
            <SettingsCard title={t("textMessages.title")}>
                {controller.loadFailed ? (
                    <div className="flex items-center justify-between gap-3">
                        <p role="alert" className="settings-row-desc">{t("textMessages.loadFailed")}</p>
                        <Button size="sm" variant="outline" onClick={() => void controller.refresh()}>{t("textMessages.retry")}</Button>
                    </div>
                ) : <p role="status" className="settings-row-desc">{t("textMessages.loading")}</p>}
            </SettingsCard>
        );
    }
    if (!status.number || !(status.consentAccepted || status.verified || status.enabled || status.verifying)) return null;

    const needsAttention = !status.verified || !status.permissionGranted || !status.supported || Boolean(status.lastError);
    const statusKey = status.verifying ? "textMessages.stateVerifying"
        : !status.enabled ? "comingSoon.nativeOff"
        : needsAttention ? "comingSoon.nativePaused" : "textMessages.enableOn";
    const canVerify = status.supported && status.permissionGranted && status.consentAccepted;
    return (
        <SettingsCard title={t("textMessages.title")}>
            <p role="status" className="settings-row-desc pb-3">{t(statusKey, { number: status.number })}</p>
            <div className="flex flex-wrap justify-end gap-2 pb-2">
                <Button size="sm" variant="outline" disabled={busy !== null || (!status.enabled && !controller.canEnable)}
                    onClick={() => controller.setEnabled(!status.enabled)}>
                    {status.enabled ? t("comingSoon.turnOff") : t("textMessages.enableLabel")}
                </Button>
                <Button size="sm" variant="outline" disabled={busy !== null || !status.enabled || !status.verified || !status.permissionGranted || !status.supported}
                    onClick={() => void controller.sendTest()}>{t("textMessages.testButton")}</Button>
                <Button size="sm" variant="outline" disabled={busy !== null || !canVerify}
                    onClick={() => void controller.startVerification()}>{t("comingSoon.verifyNumber")}</Button>
            </div>
            <SettingsRow title={t("textMessages.namesLabel")} description={t("textMessages.namesDescription")}
                control={<Switch checked={status.includeNames} disabled={busy !== null}
                    aria-label={t("textMessages.namesLabel")} onCheckedChange={controller.setIncludeNames} />} />
            {!status.permissionGranted && (
                <SettingsRow title={t("textMessages.permissionLabel")} description={t("textMessages.permissionNeeded")}
                    control={<Button size="sm" variant="outline" disabled={busy !== null || !status.supported}
                        onClick={() => void controller.grantMessages()}>{t("textMessages.grantButton")}</Button>} />
            )}
            {status.verifying && (
                <div className="flex flex-col gap-2 py-3">
                    <label htmlFor="managed-text-verification-code" className="settings-row-title">{t("textMessages.codeLabel")}</label>
                    <input id="managed-text-verification-code" inputMode="numeric" autoComplete="one-time-code" maxLength={6}
                        className={NUMBER_INPUT_CLASS} value={controller.code} disabled={busy !== null}
                        onChange={(event) => controller.editCode(event.target.value)} />
                    <Button size="sm" disabled={busy !== null || !canVerify || !/^[0-9]{6}$/.test(controller.code)}
                        onClick={() => void controller.confirmVerification()}>{t("textMessages.confirmCode")}</Button>
                </div>
            )}
            {Object.entries(errors).map(([scope, error]) => error && <p key={scope} role="alert" className="settings-row-desc">{error}</p>)}
            {testOutcome?.kind === "sent" && <p role="status" className="settings-row-desc">{t("textMessages.testSent")}</p>}
            {testOutcome?.kind === "failed" && <p role="alert" className="settings-row-desc">{testOutcome.reason}</p>}
            {status.lastError && !testOutcome && !Object.values(errors).some(Boolean) && (
                <p role="alert" className="settings-row-desc">{describeTextError(status.lastError, t)}</p>
            )}
        </SettingsCard>
    );
}

/**
 * The everyday Text messages card: your number, the consent to text it, the
 * on/off switch and, only while it is missing, the Messages permission.
 */
export function TextMessagesSettings({ controller }: { controller: TextMessagesController }) {
    const { t } = useTranslation("settings");
    const {
        status,
        loadFailed,
        busy,
        errors,
        shownNumber,
        numberValid,
        canEnable,
        showConsent,
    } = controller;

    const numberHint = shownNumber.trim() !== "" && !numberValid;
    const enableDisabled = busy !== null || (!status?.enabled && !canEnable);
    const enableDescription = !status
        ? ""
        : status.verifying
          ? t("textMessages.stateVerifying", { number: status.number })
          : status.enabled && status.verified && status.permissionGranted && !status.lastError
          ? t("textMessages.enableOn", { number: status.number })
          : t("textMessages.enableOff");
    // The permission is a prerequisite: shown once texts are set up, and only while it is missing.
    const showGrant =
        status !== null && !status.permissionGranted && status.consentAccepted && !controller.numberChanged;

    const body = (() => {
        if (status === null) {
            return loadFailed ? (
                <div className="flex flex-wrap items-center gap-3 py-2">
                    <p role="alert" className="text-xs text-error">
                        {t("textMessages.loadFailed")}
                    </p>
                    <Button type="button" variant="outline" size="sm" onClick={() => void controller.refresh()}>
                        {t("textMessages.retry")}
                    </Button>
                </div>
            ) : (
                <p role="status" className="settings-row-desc py-2">
                    {t("textMessages.loading")}
                </p>
            );
        }
        if (!status.supported) {
            return <p className="settings-row-desc py-2">{t("textMessages.unsupported")}</p>;
        }
        return (
            <div className="flex flex-col">
                <p role="status" className="settings-row-desc pb-3">{enableDescription}</p>
                <p className="settings-row-desc pb-3">{t("textMessages.consentBody")}</p>
                <div className="flex flex-col gap-2 pt-1 pb-4">
                    <label htmlFor="text-messages-number" className="settings-row-title">
                        {t("textMessages.numberLabel")}
                    </label>
                    <input
                        id="text-messages-number"
                        type="tel"
                        inputMode="tel"
                        autoComplete="tel"
                        spellCheck={false}
                        placeholder={t("textMessages.numberPlaceholder")}
                        value={shownNumber}
                        disabled={busy !== null}
                        aria-invalid={numberHint}
                        onChange={(event) => controller.editNumber(event.target.value)}
                        className={NUMBER_INPUT_CLASS}
                    />
                    {numberHint && (
                        <p role="alert" className="text-xs text-error">
                            {t("textMessages.numberHint")}
                        </p>
                    )}
                    {showConsent && (
                        <div className="mt-1 flex flex-col gap-3 rounded-lg border border-border bg-background-accent/30 p-4">
                            {errors.consent && (
                                <p role="alert" className="text-xs text-error">
                                    {errors.consent}
                                </p>
                            )}
                            <div className="flex flex-wrap items-center justify-end gap-2">
                                <Button
                                    type="button"
                                    variant="outline"
                                    size="sm"
                                    disabled={busy !== null}
                                    onClick={controller.dismissConsent}
                                >
                                    {t("textMessages.consentNotNow")}
                                </Button>
                                <Button
                                    type="button"
                                    size="sm"
                                    disabled={busy !== null}
                                    onClick={controller.allowTexts}
                                >
                                    {busy === "consent" && <Loader2 className="size-3.5 animate-spin" />}
                                    {t("textMessages.consentAllow")}
                                </Button>
                            </div>
                        </div>
                    )}
                </div>

                <SettingsRow
                    title={t("textMessages.enableLabel")}
                    description={!canEnable ? t("textMessages.enableNeedsConsent") : undefined}
                    control={
                        <Switch
                            aria-label={t("textMessages.enableLabel")}
                            checked={status.enabled}
                            disabled={enableDisabled}
                            onCheckedChange={controller.setEnabled}
                        />
                    }
                />
                {status.consentAccepted && !status.verified && !controller.numberChanged && status.permissionGranted && (
                    <div className="flex flex-col gap-2 py-3">
                        {status.verifying && (
                            <>
                                <label htmlFor="text-verification-code" className="settings-row-title">{t("textMessages.codeLabel")}</label>
                                <input id="text-verification-code" inputMode="numeric" autoComplete="one-time-code" maxLength={6}
                                    className={NUMBER_INPUT_CLASS} value={controller.code} disabled={busy !== null}
                                    onChange={(event) => controller.editCode(event.target.value)} />
                                <Button type="button" size="sm" disabled={busy !== null || !/^[0-9]{6}$/.test(controller.code)} onClick={() => void controller.confirmVerification()}>
                                    {t("textMessages.confirmCode")}
                                </Button>
                            </>
                        )}
                        <Button type="button" variant="outline" size="sm" disabled={busy !== null} onClick={() => void controller.startVerification()}>
                            {t("textMessages.sendCode")}
                        </Button>
                    </div>
                )}
                {errors.verify && <p role="alert" className="pb-2 text-xs text-error">{errors.verify}</p>}
                {errors.enable && (
                    <p role="alert" className="pb-2 text-xs text-error">
                        {errors.enable}
                    </p>
                )}

                {showGrant && (
                    <SettingsRow
                        title={t("textMessages.permissionLabel")}
                        description={t("textMessages.permissionNeeded")}
                        control={
                            <Button
                                type="button"
                                variant="outline"
                                size="sm"
                                disabled={busy !== null}
                                onClick={() => void controller.grantMessages()}
                            >
                                {busy === "grant" && <Loader2 className="size-3.5 animate-spin" />}
                                {t("textMessages.grantButton")}
                            </Button>
                        }
                    />
                )}
                {errors.grant && (
                    <p role="alert" className="pb-2 text-xs text-error">
                        {errors.grant}
                    </p>
                )}
                {status.lastError && controller.testOutcome?.kind !== "failed" && !Object.values(errors).some(Boolean) && (
                    <p role="alert" className="settings-row-desc pb-2">{describeTextError(status.lastError, t)}</p>
                )}
            </div>
        );
    })();

    return (
        <SettingsCard
            title={t("textMessages.title")}
            description={status?.supported === false ? undefined : t("textMessages.description")}
        >
            {body}
        </SettingsCard>
    );
}

/**
 * Text message details for the iPhone page's Advanced section: whether texts
 * may name people, a test text, today's count and the last problem, in plain
 * words. Nothing shows until the settings load on a Mac that can send texts.
 */
export function TextMessagesDetails({ controller }: { controller: TextMessagesController }) {
    const { t } = useTranslation("settings");
    const { status, busy, errors, testOutcome, numberChanged } = controller;

    if (status === null || !status.supported) return null;

    const namesDisabled = busy !== null || !status.enabled;
    const testDisabled = busy !== null || !status.enabled || numberChanged;
    const testDescription = !status.enabled
        ? t("textMessages.testNeedsEnable")
        : numberChanged
          ? t("textMessages.testNeedsSave")
          : t("textMessages.testDescription");

    return (
        <SettingsCard
            title={t("textMessages.detailsTitle")}
            description={t("textMessages.detailsDescription")}
        >
            <div className="flex flex-col">
                <SettingsRow
                    title={t("textMessages.namesLabel")}
                    description={
                        status.enabled ? t("textMessages.namesDescription") : t("textMessages.namesNeedsEnable")
                    }
                    control={
                        <Switch
                            aria-label={t("textMessages.namesLabel")}
                            checked={status.includeNames}
                            disabled={namesDisabled}
                            onCheckedChange={controller.setIncludeNames}
                        />
                    }
                />
                {errors.names && (
                    <p role="alert" className="pb-2 text-xs text-error">
                        {errors.names}
                    </p>
                )}

                <SettingsRow
                    title={t("textMessages.testLabel")}
                    description={testDescription}
                    control={
                        <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            disabled={testDisabled}
                            onClick={() => void controller.sendTest()}
                        >
                            {busy === "test" && <Loader2 className="size-3.5 animate-spin" />}
                            {t("textMessages.testButton")}
                        </Button>
                    }
                />
                {testOutcome?.kind === "sent" && (
                    <p role="status" className="pb-2 text-xs text-ink">
                        {t("textMessages.testSent")}
                    </p>
                )}
                {testOutcome?.kind === "failed" && <p role="alert" className="pb-2 text-xs text-muted">{testOutcome.reason}</p>}

                <div className="flex flex-col gap-1 pt-3">
                    <p className="settings-row-desc">
                        {t("textMessages.sentToday", {
                            sent: status.sentToday,
                            limit: TEXT_DAILY_LIMIT,
                        })}
                    </p>
                </div>
            </div>
        </SettingsCard>
    );
}
