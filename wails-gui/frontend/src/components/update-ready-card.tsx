import { useState } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import {
    CheckForUpdates,
    DownloadUpdate,
    DismissUpdateUntilNextLaunch,
    RestartToUpdate,
} from "@/lib/wails-shim-app";

export function UpdateReadyCard({
    version,
    visible = true,
    onLater,
}: {
    version: string;
    visible?: boolean;
    onLater: () => void;
}) {
    const { t } = useTranslation("settings");
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState(false);
    // Shown from the click until the app quits; cleared only on failure.
    const [restarting, setRestarting] = useState(false);
    async function restart() {
        setRestarting(true);
        try {
            await RestartToUpdate();
        } catch (err) {
            setRestarting(false);
            throw err;
        }
    }
    async function perform(action: () => Promise<void>) {
        setBusy(true);
        setError(false);
        try {
            await action();
        } catch {
            setError(true);
        } finally {
            setBusy(false);
        }
    }
    if (!visible && !error) return null;
    const retry = error && !visible;
    return (
        <section
            className="mx-3 mb-3 shrink-0 rounded-xl border border-border bg-panel px-3 py-3"
            aria-label={t(
                retry ? "aboutPolish.updateFailed" : "aboutPolish.updateReady",
            )}
        >
            <p className="text-[13px] font-medium text-ink">
                {t(
                    retry
                        ? "aboutPolish.updateFailed"
                        : "aboutPolish.cardTitle",
                    { version },
                )}
            </p>
            <p className="mt-1 text-xs leading-relaxed text-muted">
                {t(
                    retry
                        ? "aboutPolish.retryDescription"
                        : "aboutPolish.cardDescription",
                )}
            </p>
            <div className="mt-2.5 flex gap-2">
                <Button
                    size="sm"
                    disabled={busy || restarting}
                    onClick={() =>
                        void perform(
                            retry
                                ? async () => {
                                      const info = await CheckForUpdates();
                                      if (info.error)
                                          throw new Error(info.error);
                                      if (info.available)
                                          await DownloadUpdate();
                                  }
                                : restart,
                        )
                    }
                >
                    {t(
                        retry
                            ? "aboutPolish.download"
                            : restarting
                              ? "aboutPolish.restarting"
                              : "aboutPolish.restartNow",
                    )}
                </Button>
                <Button
                    size="sm"
                    variant="quiet"
                    disabled={busy || restarting}
                    onClick={() =>
                        void perform(async () => {
                            await DismissUpdateUntilNextLaunch();
                            onLater();
                        })
                    }
                >
                    {t("aboutPolish.later")}
                </Button>
            </div>
            {error && (
                <p role="alert" className="mt-2 text-xs text-error">
                    {t("aboutPolish.actionFailed")}
                </p>
            )}
        </section>
    );
}
