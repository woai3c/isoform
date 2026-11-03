import { useCallback, useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";
import { toast } from "sonner";
import {
    GetActivityMaxStorageGB,
    GetActivityRetentionDays,
    GetActivityStorageStats,
    SetActivityMaxStorageGB,
    SetActivityRetentionDays,
} from "../../../../wailsjs/go/main/App";

// The recording time limit and the storage limit, shared by the Privacy page
// ("Keep recordings for") and the Storage page (the storage limit and what
// is used).

export const GIB = 1024 * 1024 * 1024;

// Retention choices in days; 0 keeps recordings until the storage limit fills.
export const RETENTION_DAYS = [0, 7, 14, 30, 60, 90, 180, 360] as const;

// Storage limit presets in GB. Intentionally NO Unlimited option: without a
// hard quota the rotator could grow on-disk activity data without bound,
// which is a worse foot-gun than the original 2GB cap that wiped weeks of
// recordings on a single rotation. Users who need more than 100GB can pick
// "Custom" and type any integer; values below 1GB are rejected by the Go
// binding because even one meeting recording can exceed them.
export const STORAGE_CAP_PRESETS_GB = [1, 2, 5, 10, 20, 50, 100] as const;
export const STORAGE_CAP_CUSTOM = "custom" as const;
export const STORAGE_CAP_MIN = 1;
export const STORAGE_CAP_MAX = 10000; // sanity ceiling: 10TB. Far above any realistic device.

export interface ActivityStorageStats {
    retainedBytes: number;
    retainedSessionCount: number;
    earliestRetainedMs: number;
}

// Format a unix-ms timestamp as an absolute + relative hint, e.g.
// "2026-03-29 (30 days ago)". Returns "" when ts is 0 / unset.
//
// Uses calendar-day comparison (local timezone), not a 24-hour rolling
// window. A timestamp from yesterday evening must read "1 day ago" even
// if elapsed wall-clock time is < 24h — otherwise the user sees "today"
// for data that is plainly from yesterday on the calendar.
export function formatEarliest(ms: number, t: TFunction<"settings">): string {
    if (!ms || ms <= 0) return "";
    const d = new Date(ms);
    const dateStr = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
    const startOfDay = (date: Date) => new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime();
    const ageDays = Math.max(0, Math.round((startOfDay(new Date()) - startOfDay(d)) / (24 * 60 * 60 * 1000)));
    if (ageDays === 0) return t("activityRetention.earliestToday", { date: dateStr });
    if (ageDays === 1) return t("activityRetention.earliestYesterday", { date: dateStr });
    return t("activityRetention.earliestDaysAgo", { date: dateStr, count: ageDays });
}

export function retentionLabel(t: TFunction<"settings">, days: number): string {
    return days === 0
        ? t("activityRetention.retentionUnlimited")
        : t("activityRetention.retentionDaysLabel", { count: days });
}

/** A shorter time limit deletes recordings now; a longer one or Unlimited
 *  deletes nothing. */
export function shortensRetention(currentDays: number, nextDays: number): boolean {
    if (nextDays === 0) return false;
    if (currentDays === 0) return true;
    return nextDays < currentDays;
}

export interface RecordingStorage {
    loading: boolean;
    saving: boolean;
    days: number;
    maxGB: number;
    stats: ActivityStorageStats | null;
    /** Bumped a moment after a setting is saved, once the rotation pass has
     *  had time to run, so usage readers can fetch again. */
    refreshKey: number;
    /** Fetch usage again after something else changed it (the cleanup). */
    refresh: () => void;
    saveDays: (days: number) => void;
    saveMaxGB: (gb: number) => void;
}

/** Retention days, storage limit and usage, shared by the "Keep recordings
 *  for" card on Privacy and the storage limit on Storage. */
export function useRecordingStorage(): RecordingStorage {
    const { t } = useTranslation("settings");
    const [days, setDays] = useState(30);
    const [maxGB, setMaxGB] = useState(10);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const [stats, setStats] = useState<ActivityStorageStats | null>(null);

    // Refetch counter — bump after a setting change so the summary reflects
    // post-purge state once the rotation pass finishes.
    const [refreshKey, setRefreshKey] = useState(0);

    // Held in a ref so the unmount cleanup can clear a pending refresh and
    // each new save replaces the prior one — otherwise rapid setting changes
    // pile up timers that all bump refreshKey after unmount.
    const refreshTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

    useEffect(() => {
        Promise.all([GetActivityRetentionDays(), GetActivityMaxStorageGB()])
            .then(([d, gb]) => {
                setDays(d);
                setMaxGB(gb);
            })
            .catch(() => {})
            .finally(() => setLoading(false));
    }, []);

    useEffect(() => {
        GetActivityStorageStats()
            .then((s) => setStats(s as ActivityStorageStats))
            // Usage is informational only; the cards work without it.
            .catch(() => setStats(null));
    }, [refreshKey]);

    useEffect(() => {
        return () => {
            if (refreshTimerRef.current !== null) {
                clearTimeout(refreshTimerRef.current);
                refreshTimerRef.current = null;
            }
        };
    }, []);

    // Both setters trigger an async rotation pass on the backend, so wait a
    // beat before refetching usage so the user sees post-purge numbers.
    const scheduleStatsRefresh = useCallback(() => {
        if (refreshTimerRef.current !== null) {
            clearTimeout(refreshTimerRef.current);
        }
        refreshTimerRef.current = setTimeout(() => {
            refreshTimerRef.current = null;
            setRefreshKey((k) => k + 1);
        }, 1500);
    }, []);

    const saveDays = useCallback(
        (next: number) => {
            const previous = days;
            setDays(next);
            setSaving(true);
            SetActivityRetentionDays(next)
                .then(() => {
                    scheduleStatsRefresh();
                })
                .catch((err: unknown) => {
                    setDays(previous);
                    toast.error(
                        t("activityRetention.retentionFailedToast", {
                            error: err instanceof Error ? err.message : String(err),
                        }),
                    );
                })
                .finally(() => setSaving(false));
        },
        [days, scheduleStatsRefresh, t],
    );

    const saveMaxGB = useCallback(
        (gb: number) => {
            const previous = maxGB;
            setMaxGB(gb);
            setSaving(true);
            SetActivityMaxStorageGB(gb)
                .then(() => {
                    scheduleStatsRefresh();
                })
                .catch((err: unknown) => {
                    // Restore the previous value so the UI doesn't lie about
                    // what's persisted.
                    setMaxGB(previous);
                    toast.error(
                        t("activityRetention.storageCapFailedToast", {
                            error: err instanceof Error ? err.message : String(err),
                        }),
                    );
                })
                .finally(() => setSaving(false));
        },
        [maxGB, scheduleStatsRefresh, t],
    );

    const refresh = useCallback(() => setRefreshKey((k) => k + 1), []);

    return { loading, saving, days, maxGB, stats, refreshKey, refresh, saveDays, saveMaxGB };
}
