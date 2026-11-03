import { useEffect, useRef } from "react";
import { useTranslation } from "react-i18next";
import { SetupIcon } from "../SetupIcons";
import { Run, reflow } from "../motion";
import { playSetupFinish } from "../native";
import { TIMINGS } from "../timings";

// The finish (handoff §4, "The finish"): the icon pops in with a check and
// eight rays, holds 1.0 s, then the window shrinks away while the icon flies
// into the menu bar mark (drawn natively, above every window). The main
// window then opens on Chat.

const RAYS = Array.from({ length: 8 }, (_, k) => k * 45 + 22.5);

interface FinishSceneProps {
    /** The flight landed (or there was none): open the main window on Chat. */
    onDone: () => void;
    /** The window is going: the shell shrinks and fades it (0.28 s). */
    onClosing: () => void;
    reduced: boolean;
    still?: boolean;
}

const T = TIMINGS.finish;

export function FinishScene({ onDone, onClosing, reduced, still }: FinishSceneProps) {
    const { t } = useTranslation("onboarding");
    const tile = useRef<HTMLDivElement>(null);
    const badge = useRef<HTMLSpanElement>(null);
    const burst = useRef<HTMLDivElement>(null);
    const done = useRef(onDone);
    const closing = useRef(onClosing);
    done.current = onDone;
    closing.current = onClosing;

    useEffect(() => {
        const tileEl = tile.current;
        if (!tileEl) return;
        const run = new Run(reduced);
        if (still) {
            badge.current?.classList.add("show");
            return () => run.cancel();
        }
        const fly = async () => {
            const box = tileEl.getBoundingClientRect();
            closing.current();
            const flown = await playSetupFinish({
                iconX: box.left + box.width / 2,
                iconY: box.top + box.height / 2,
                iconSize: box.width,
                callout: t("v2.finish.callout"),
                calloutHidden: t("v2.finish.calloutHidden"),
                reduceMotion: reduced,
            });
            // Without the native flight the window's own fade still plays.
            if (!flown) await run.wait(T.fallbackFade);
            if (run.alive) done.current();
        };
        run.go(async () => {
            if (reduced) {
                tileEl.classList.add("pop");
                badge.current?.classList.add("show");
                await run.wait(0);
                await fly();
                return;
            }
            reflow(tileEl);
            tileEl.classList.add("pop");
            await run.wait(T.badge);
            badge.current?.classList.add("show");
            burst.current?.classList.add("go");
            await run.wait(T.hold);
            await fly();
        });
        return () => run.cancel();
    }, [reduced, still, t]);

    return (
        <div className="done" data-testid="setup-v2-finish" role="status" aria-label={t("v2.finish.done")}>
            <div className="atile" ref={tile}>
                <SetupIcon id="mk" />
                <span className="badge" ref={badge}>
                    <SetupIcon id="i-check" />
                </span>
                <div className="burst" ref={burst}>
                    {RAYS.map((a) => (
                        <i key={a} style={{ ["--a" as string]: `${a}deg` }} />
                    ))}
                </div>
            </div>
        </div>
    );
}
