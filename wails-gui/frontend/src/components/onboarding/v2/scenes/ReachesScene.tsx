import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { QRCodeSVG } from "qrcode.react";
import {
    ConfirmMobilePairing,
    EnableMobileConnectionUsingRelay,
    GenerateMobileConnectionCode,
    GetMobileConnectionStatus,
    NativeClipboardSetText,
    type MobileConnectionStatus,
} from "@/lib/wails-shim-app";
import { GetSetupPermissions, OpenPermissionSettings, RequestPermission } from "../../../../../wailsjs/go/main/App";
import { BrowserOpenURL, ClipboardSetText, EventsOn } from "../../../../../wailsjs/runtime/runtime";
import { IPHONE_APP_LINK } from "@/lib/iphone-app-link";
import { SetupIcon } from "../SetupIcons";
import { Run, reflow } from "../motion";
import type { SceneProps } from "../types";
import { TIMINGS } from "../timings";

// Screen 5 (handoff §4, "Screen 5 · Reaches you"): two notices drop onto a
// lock screen; beside it one code pairs the iPhone app with this computer.
// Turn on notifications asks macOS, shows "Notifications on", and the finish
// begins 0.95 s later. Not now goes straight to the finish.

/** The code changes every 5 minutes (handoff §4, screen 5). */
export const PAIRING_CODE_REFRESH_MS = TIMINGS.reaches.codeRefresh;

type Card =
    | { kind: "code"; code: string }
    | { kind: "pending"; pairingId: string; phone: string; code: string }
    | { kind: "paired"; phone: string };

/** What the card shows for a connection status. */
const T = TIMINGS.reaches;

export function cardFor(status: MobileConnectionStatus | null): Card {
    if (status?.pendingPairing) {
        return {
            kind: "pending",
            pairingId: status.pendingPairing.pairingId,
            phone: status.pendingPairing.deviceName,
            code: status.pendingPairing.code,
        };
    }
    const device = status?.devices?.[0];
    if (device) return { kind: "paired", phone: device.deviceName };
    return { kind: "code", code: status?.connectionCode ?? "" };
}

async function copyText(value: string): Promise<boolean> {
    try {
        const nativeRuntime = (window as unknown as { runtime?: { ClipboardSetText?: unknown } })
            .runtime?.ClipboardSetText;
        if (nativeRuntime) {
            await NativeClipboardSetText(value);
        } else if (!(await ClipboardSetText(value))) {
            return false;
        }
        return true;
    } catch {
        return false;
    }
}

// The gallery shows these fixed states; a still's code is a sample, not a
// pairing link.
const STILL_CARDS: Record<string, Card> = {
    reaches: { kind: "code", code: "isoform://pair#setup-gallery-sample" },
    "reaches-paired": { kind: "paired", phone: "Alex's iPhone" },
};

export function ReachesScene({ onNext, reduced, still, enter }: SceneProps) {
    const { t } = useTranslation("onboarding");
    const frozen = !!still;
    const [status, setStatus] = useState<MobileConnectionStatus | null>(null);
    const [notifications, setNotifications] = useState<"unknown" | "off" | "asking" | "on">("unknown");
    const [notificationError, setNotificationError] = useState(false);
    const [notificationDenied, setNotificationDenied] = useState(false);
    const notificationReadVersion = useRef(0);
    const requestingNotifications = useRef(false);
    const [copied, setCopied] = useState(false);
    const [pairFailed, setPairFailed] = useState(false);
    const phone = useRef<HTMLDivElement>(null);
    const getapp = useRef<HTMLDivElement>(null);
    const pn1 = useRef<HTMLDivElement>(null);
    const pn2 = useRef<HTMLDivElement>(null);
    const card = frozen ? (STILL_CARDS[still] ?? STILL_CARDS.reaches) : cardFor(status);

    // The picture: the phone and the card come in, then the two notices
    // drop at 0.65 s and 1.95 s.
    useEffect(() => {
        const phoneEl = phone.current;
        const cardEl = getapp.current;
        if (!phoneEl || !cardEl) return;
        const notes = [pn1.current, pn2.current];
        const run = new Run(reduced);
        if (reduced || still) {
            phoneEl.classList.add("in");
            cardEl.classList.add("in");
            notes.forEach((el) => el?.classList.add("show"));
            return () => run.cancel();
        }
        reflow(phoneEl);
        phoneEl.classList.add("in");
        cardEl.classList.add("in");
        run.go(async () => {
            await run.wait(T.notice1);
            pn1.current?.classList.add("show");
            await run.wait(T.notice2);
            pn2.current?.classList.add("show");
        });
        return () => run.cancel();
    }, [reduced, still]);

    // The connection: turn it on when it's off, show a fresh code, and
    // follow pairing live.
    useEffect(() => {
        if (frozen) return;
        let live = true;
        const take = (next: MobileConnectionStatus) => {
            if (live) setStatus(next);
            return next;
        };
        const fresh = async () => {
            try {
                take(await GenerateMobileConnectionCode());
            } catch {
                /* The last code stays until the next try. */
            }
        };
        void (async () => {
            try {
                let current = take(await GetMobileConnectionStatus());
                if (!current.enabled) current = take(await EnableMobileConnectionUsingRelay(""));
                if (!current.devices?.length && !current.pendingPairing) await fresh();
            } catch {
                /* Without a connection the card keeps its empty code. */
            }
        })();
        const timer = window.setInterval(() => {
            void GetMobileConnectionStatus()
                .then((s) => {
                    if (!live || s.devices?.length || s.pendingPairing) return;
                    return fresh();
                })
                .catch(() => {});
        }, PAIRING_CODE_REFRESH_MS);
        const off = EventsOn("mobile:updated", () => {
            void GetMobileConnectionStatus()
                .then(take)
                .catch(() => {});
        });
        return () => {
            live = false;
            window.clearInterval(timer);
            off();
        };
    }, [frozen]);

    // A code that expires before the 5 minutes are up is replaced at once.
    const expiresAt = status?.expiresAt ?? "";
    useEffect(() => {
        if (frozen || card.kind !== "code" || !expiresAt) return;
        const left = Date.parse(expiresAt) - Date.now();
        if (!Number.isFinite(left) || left > PAIRING_CODE_REFRESH_MS) return;
        const timer = window.setTimeout(
            () => void GenerateMobileConnectionCode().then(setStatus).catch(() => {}),
            Math.max(0, left),
        );
        return () => window.clearTimeout(timer);
    }, [frozen, card.kind, expiresAt]);

    const copy = useCallback(async () => {
        if (!(await copyText(IPHONE_APP_LINK))) return;
        setCopied(true);
    }, []);
    useEffect(() => {
        if (!copied) return;
        const timer = window.setTimeout(() => setCopied(false), T.copied);
        return () => window.clearTimeout(timer);
    }, [copied]);

    const confirm = useCallback(async (pairingId: string, approve: boolean) => {
        setPairFailed(false);
        try {
            const next = await ConfirmMobilePairing(pairingId, approve);
            setStatus(next);
            if (!approve) setStatus(await GenerateMobileConnectionCode());
        } catch {
            setPairFailed(true);
            void GenerateMobileConnectionCode().then(setStatus).catch(() => {});
        }
    }, []);

    const readNotifications = useCallback(async () => {
        const version = ++notificationReadVersion.current;
        try {
            const permissions = await GetSetupPermissions();
            const granted = permissions.notificationGranted;
            if (version !== notificationReadVersion.current) return null;
            if (typeof granted !== "boolean") throw new Error("Notification permission unavailable");
            setNotifications(granted ? "on" : "off");
            setNotificationError(false);
            if (granted) setNotificationDenied(false);
            return granted;
        } catch {
            if (version === notificationReadVersion.current) {
                setNotifications("unknown");
                setNotificationError(true);
            }
            return null;
        }
    }, []);
    useEffect(() => {
        if (frozen) return;
        void readNotifications();
        const refresh = () => { if (!requestingNotifications.current) void readNotifications(); };
        window.addEventListener("focus", refresh);
        return () => window.removeEventListener("focus", refresh);
    }, [frozen, readNotifications]);

    const turnOnNotifications = useCallback(async () => {
        if (notifications === "on") { onNext(); return; }
        if (notifications === "asking") return;
        requestingNotifications.current = true;
        setNotifications("asking");
        setNotificationError(false);
        try {
            await RequestPermission("notifications");
            const granted = await readNotifications();
            if (granted === false) {
                setNotificationDenied(true);
                await OpenPermissionSettings("notifications");
                return;
            }
            if (granted !== true) return;
            const run = new Run(reduced);
            finishing.current = run;
            run.go(async () => {
                await run.wait(reduced ? T.notificationsOnReduced : T.notificationsOn);
                onNext();
            });
        } catch {
            await readNotifications();
            setNotificationError(true);
        } finally {
            requestingNotifications.current = false;
        }
    }, [notifications, reduced, onNext, readNotifications]);
    const finishing = useRef<Run | null>(null);
    useEffect(() => () => finishing.current?.cancel(), []);

    // Under the code, the pairing and the paired phone alike (v2.1.0 §2).
    const sealed = (
        <span className="sealed">
            <SetupIcon id="i-lock" />
            {t("v2.reaches.sealed")}
        </span>
    );

    return (
        <section className={`scene ${enter}`} data-testid="setup-v2-reaches">
            <h2 className="t">{t("v2.reaches.title")}</h2>
            <p className="s">{t("v2.reaches.line")}</p>
            <div className="ill">
                <div className="reach">
                    <div className="phone" ref={phone}>
                        <div className="island" />
                        <div className="ptime">{t("v2.reaches.time")}</div>
                        <div className="pdate">{t("v2.reaches.date")}</div>
                        <div className="pn" ref={pn1} style={{ top: 96 }}>
                            <div className="pnh">
                                <span className="aicon">
                                    <SetupIcon id="mk" />
                                </span>
                                <b>{t("v2.reaches.appName")}</b>
                                <span>{t("v2.reaches.now")}</span>
                            </div>
                            <p>{t("v2.reaches.notice1")}</p>
                        </div>
                        <div className="pn" ref={pn2} style={{ top: 156 }}>
                            <div className="pnh">
                                <span className="aicon">
                                    <SetupIcon id="mk" />
                                </span>
                                <b>{t("v2.reaches.appName")}</b>
                                <span>{t("v2.reaches.now")}</span>
                            </div>
                            <p>{t("v2.reaches.notice2")}</p>
                        </div>
                    </div>
                    <div
                        className="getapp"
                        ref={getapp}
                        data-testid="setup-v2-getapp"
                        data-card={card.kind}
                        aria-live="polite"
                    >
                        {card.kind === "paired" ? (
                            <>
                                <span className="paired">
                                    <SetupIcon id="i-check" />
                                    {t("v2.reaches.paired", { phone: card.phone })}
                                </span>
                                {sealed}
                                <p>{t("v2.reaches.pairedLine")}</p>
                            </>
                        ) : card.kind === "pending" ? (
                            <>
                                <b>{t("v2.reaches.pending", { phone: card.phone })}</b>
                                <p>{t("v2.reaches.pendingCode", { code: card.code })}</p>
                                <span className="pairact">
                                    <button
                                        className="btn primary"
                                        type="button"
                                        onClick={() => void confirm(card.pairingId, true)}
                                    >
                                        {t("v2.reaches.approve")}
                                    </button>
                                    <button
                                        className="link"
                                        type="button"
                                        onClick={() => void confirm(card.pairingId, false)}
                                    >
                                        {t("v2.reaches.decline")}
                                    </button>
                                </span>
                                {sealed}
                            </>
                        ) : (
                            <>
                                <span className="qrbox" role="img" aria-label={t("v2.reaches.codeLabel")}>
                                    {card.code ? (
                                        <QRCodeSVG
                                            value={card.code}
                                            size={96}
                                            level="L"
                                            marginSize={0}
                                            bgColor="white"
                                            fgColor="black"
                                        />
                                    ) : (
                                        <span className="qr-empty" />
                                    )}
                                </span>
                                <b>{t("v2.reaches.getIt")}</b>
                                <p className={pairFailed ? "error" : undefined}>
                                    {t(pairFailed ? "v2.reaches.pairFailed" : "v2.reaches.scan")}
                                </p>
                                <span className="glink">
                                    <button className="link" type="button" onClick={() => BrowserOpenURL(IPHONE_APP_LINK)}>
                                        {t("v2.reaches.link")}
                                    </button>
                                    <button className="link" type="button" onClick={() => void copy()}>
                                        {t(copied ? "v2.reaches.copied" : "v2.reaches.copy")}
                                    </button>
                                </span>
                                {sealed}
                            </>
                        )}
                    </div>
                </div>
            </div>
            <div className="act">
                <button
                    className={`btn primary${notifications === "on" ? " on" : ""}`}
                    type="button"
                    data-testid="setup-v2-notifications"
                    disabled={notifications === "asking"}
                    onClick={() => void turnOnNotifications()}
                >
                    {notifications === "on" ? (
                        <>
                            <SetupIcon id="i-check" />
                            {t("v2.reaches.on")}
                        </>
                    ) : (
                        t("v2.reaches.turnOn")
                    )}
                </button>
                <button
                    className="btn quiet"
                    type="button"
                    data-testid="setup-v2-not-now"
                    disabled={notifications === "asking"}
                    onClick={onNext}
                >
                    {t("v2.reaches.notNow")}
                </button>
            </div>
            {notificationError && <p role="alert" className="error">{t("v2.reaches.notificationFailed")}</p>}
            {notificationDenied && !notificationError && <p role="status">{t("v2.reaches.notificationDenied")}</p>}
        </section>
    );
}
