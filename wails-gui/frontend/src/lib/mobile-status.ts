import type { TFunction } from "i18next";
import type { MobileConnectionStatus } from "@/lib/wails-shim-app";

function relayHostname(relayUrl: string): string {
    try {
        return new URL(relayUrl).hostname;
    } catch {
        return "";
    }
}

// Loopback and private relay addresses only establish a local network path.
export function isLocalRelay(relayUrl: string, warningCode?: string): boolean {
    const host = relayHostname(relayUrl).toLowerCase();
    if (warningCode === "relay_loopback" || host === "localhost" || host.endsWith(".local") || host === "[::1]") return true;
    const parts = host.split(".").map(Number);
    if (parts.length !== 4 || parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255)) return false;
    return parts[0] === 10 || parts[0] === 127 || (parts[0] === 192 && parts[1] === 168) || (parts[0] === 172 && parts[1] >= 16 && parts[1] <= 31);
}

const EARLIEST_TIMESTAMP = Date.UTC(2000, 0, 1);

function parseTimestamp(value: string): number | null {
    if (!value) return null;
    const time = Date.parse(value);
    if (Number.isNaN(time) || time < EARLIEST_TIMESTAMP) return null;
    return time;
}

function formatRelative(time: number, now: number, locale: string): string {
    const seconds = Math.round((time - now) / 1000);
    const formatter = new Intl.RelativeTimeFormat(locale, { numeric: "auto" });
    const absolute = Math.abs(seconds);
    if (absolute < 60) return formatter.format(seconds, "second");
    if (absolute < 3_600) return formatter.format(Math.round(seconds / 60), "minute");
    if (absolute < 86_400) return formatter.format(Math.round(seconds / 3_600), "hour");
    return formatter.format(Math.round(seconds / 86_400), "day");
}

export type MobileStatusSummary = Pick<MobileConnectionStatus, "enabled" | "relayUrl" | "relayState" | "relayLastSeenAt" | "warningCode" | "remoteAccess">;

export function describeMobileStatus(current: MobileStatusSummary, now: number, locale: string, t: TFunction<"settings">): { label: string; description: string; on: boolean } {
    if (!current.enabled) {
        return {
            label: t("mobileConnection.statusOff"),
            description: t("mobileConnection.statusOffDescription"),
            on: false,
        };
    }
    switch (current.relayState) {
        case "connected": {
            const seenAt = parseTimestamp(current.relayLastSeenAt);
            const localOnly = isLocalRelay(current.relayUrl, current.warningCode);
            const description =
                current.remoteAccess?.state === "ready"
                    ? seenAt === null
                        ? t("mobileConnection.statusConnectedRemoteNoTime")
                        : t("mobileConnection.statusConnectedRemote", {
                              time: formatRelative(seenAt, now, locale),
                          })
                    : current.remoteAccess
                      ? t("mobileConnection.statusConnectedRemoteNotReady")
                      : localOnly
                        ? seenAt === null
                            ? t("mobileConnection.statusConnectedLocalOnlyNoTime")
                            : t("mobileConnection.statusConnectedLocalOnly", {
                                  time: formatRelative(seenAt, now, locale),
                              })
                        : seenAt === null
                          ? t("mobileConnection.statusConnectedNoTime")
                          : t("mobileConnection.statusConnectedDescription", {
                                time: formatRelative(seenAt, now, locale),
                            });
            return {
                label: t("mobileConnection.statusConnected"),
                description,
                on: true,
            };
        }
        case "offline":
            return {
                label: t("mobileConnection.statusOffline"),
                description: t("mobileConnection.statusOfflineDescription"),
                on: false,
            };
        default:
            return {
                label: t("mobileConnection.statusConnecting"),
                description: t("mobileConnection.statusConnectingDescription"),
                on: false,
            };
    }
}
