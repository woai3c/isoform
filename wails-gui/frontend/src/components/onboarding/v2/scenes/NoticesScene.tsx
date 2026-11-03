import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useTelemetryPreference } from "@/hooks/useTelemetryPreference";
import { GetTodayDayShape } from "@/lib/wails-shim-app";
import { iconMarkup, type SetupIconId } from "../SetupIcons";
import { Run, reflow } from "../motion";
import type { SceneProps } from "../types";
import { TIMINGS } from "../timings";

// Screen 1 (handoff §4, "Screen 1 · Notices"): the day strip from Today,
// 7 AM to 7 PM over 360 pt, swept by the violet "now" line in 9 s.
const STRIP = 360;
const DAY_START = 7;
const DAY_HOURS = 12;

const EVENTS: Array<{
    x: number;
    icon: SetupIconId;
    key: "brief" | "priya" | "lena" | "invoice";
    need: boolean;
}> = [
    { x: 25, icon: "i-play", key: "brief", need: false },
    { x: 100, icon: "i-mail", key: "priya", need: true },
    { x: 195, icon: "i-cal", key: "lena", need: false },
    { x: 275, icon: "i-flag", key: "invoice", need: true },
];

/** Sample blocks for people whose calendar Isoform can't read yet. */
const SAMPLE_BLOCKS = [
    { left: 75, width: 15 },
    { left: 210, width: 30 },
    { left: 285, width: 15 },
];

type Block = { left: number; width: number };

/** Today's real meetings as strip blocks, only when calendar access exists. */
const T = TIMINGS.notices;

export function blocksFromDayShape(
    events: Array<{ start: string; end: string; allDay?: boolean }>,
): Block[] {
    const blocks: Block[] = [];
    for (const event of events) {
        if (event.allDay) continue;
        const start = new Date(event.start);
        const end = new Date(event.end);
        if (Number.isNaN(start.getTime()) || Number.isNaN(end.getTime()))
            continue;
        const hour = (d: Date) => d.getHours() + d.getMinutes() / 60;
        const a = Math.max(DAY_START, hour(start));
        const b = Math.min(DAY_START + DAY_HOURS, hour(end));
        if (b <= a) continue;
        const left = ((a - DAY_START) / DAY_HOURS) * STRIP;
        const width = Math.max(8, ((b - a) / DAY_HOURS) * STRIP);
        blocks.push({ left: Math.round(left), width: Math.round(width) });
    }
    return blocks;
}

function todayISO() {
    const d = new Date();
    const pad = (n: number) => String(n).padStart(2, "0");
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

export function NoticesScene({ onNext, reduced, still, enter }: SceneProps) {
    const { t } = useTranslation("onboarding");
    const telemetry = useTelemetryPreference();
    const [teleOpen, setTeleOpen] = useState(false);
    const [teleFailed, setTeleFailed] = useState(false);
    const [blocks, setBlocks] = useState<Block[]>(SAMPLE_BLOCKS);
    const lane = useRef<HTMLDivElement>(null);
    const chip = useRef<HTMLDivElement>(null);
    const now = useRef<HTMLDivElement>(null);
    const past = useRef<HTMLDivElement>(null);
    const markers = useRef<Array<HTMLElement | null>>([]);

    // Real meeting blocks only when calendar access already exists; the
    // sample stays otherwise. Read once, before the sweep reaches them.
    useEffect(() => {
        if (still) return;
        let live = true;
        void GetTodayDayShape(todayISO())
            .then((shape) => {
                if (!live || !shape?.calendarConnected) return;
                const real = blocksFromDayShape(shape.events ?? []);
                if (real.length) setBlocks(real);
            })
            .catch(() => {});
        return () => {
            live = false;
        };
    }, [still]);

    useEffect(() => {
        const nowEl = now.current;
        const pastEl = past.current;
        const chipEl = chip.current;
        if (!nowEl || !pastEl || !chipEl) return;
        const run = new Run(reduced);
        const labels = EVENTS.map(
            (e) =>
                `${iconMarkup(e.icon)}<span>${escapeHTML(t(`v2.notices.cards.${e.key}`))}</span><span class="st ${e.need ? "need" : "ready"}">${escapeHTML(t(e.need ? "v2.notices.needsYou" : "v2.notices.ready"))}</span>`,
        );
        const setNow = (x: number) => {
            nowEl.style.transform = `translateX(${x}px)`;
            pastEl.style.transform = `scaleX(${x / STRIP})`;
        };
        const showChip = (i: number) => {
            chipEl.classList.remove("show");
            chipEl.innerHTML = labels[i];
            const w = chipEl.offsetWidth;
            chipEl.style.left = `${Math.max(0, Math.min(STRIP - w, EVENTS[i].x - w / 2))}px`;
            reflow(chipEl);
            chipEl.classList.add("show");
            markers.current[i]?.classList.add("hit");
        };
        const reset = () => {
            setNow(0);
            chipEl.classList.remove("show");
            markers.current.forEach((m) => m?.classList.remove("hit", "fade"));
        };
        if (reduced || still) {
            markers.current.forEach((m) => m?.classList.add("hit"));
            setNow(STRIP);
            showChip(2);
            return () => run.cancel();
        }
        run.go(async () => {
            for (;;) {
                reset();
                await run.wait(T.start);
                let shown = -1;
                await run.tween(T.sweep, (p) => {
                    const x = p * STRIP;
                    setNow(x);
                    for (let i = shown + 1; i < EVENTS.length; i++) {
                        if (x >= EVENTS[i].x) {
                            shown = i;
                            showChip(i);
                        }
                    }
                });
                await run.wait(T.hold);
                chipEl.classList.remove("show");
                markers.current.forEach((m) => m?.classList.add("fade"));
                nowEl.style.opacity = "0";
                await run.wait(T.fade);
                nowEl.style.opacity = "1";
            }
        });
        return () => run.cancel();
    }, [reduced, still, t]);

    const changeTelemetry = async () => {
        setTeleFailed(false);
        try {
            await telemetry.setEnabled(!telemetry.enabled);
        } catch {
            setTeleFailed(true);
        }
    };

    return (
        <section className={`scene ${enter}`} data-testid="setup-v2-notices">
            <h2 className="t">{t("v2.notices.title")}</h2>
            <p className="s">{t("v2.notices.line")}</p>
            <div className="ill">
                <div className="day">
                    <div className="chip dchip" ref={chip} />
                    <div className="lane" ref={lane}>
                        <div className="past" ref={past} />
                        {blocks.map((b, i) => (
                            <div
                                key={`${b.left}-${i}`}
                                className="blk"
                                style={{ left: b.left, width: b.width }}
                            />
                        ))}
                        {EVENTS.map((e, i) => (
                            <i
                                key={e.key}
                                ref={(el) => {
                                    markers.current[i] = el;
                                }}
                                className={`mkr${e.need ? " need" : ""}`}
                                style={{ left: e.x }}
                            />
                        ))}
                    </div>
                    <div className="now" ref={now} />
                    <div className="hours">
                        <span style={{ left: 0 }}>{t("v2.notices.hours.h7")}</span>
                        <span style={{ left: 90 }}>{t("v2.notices.hours.h10")}</span>
                        <span style={{ left: 180 }}>{t("v2.notices.hours.h13")}</span>
                        <span style={{ left: 270 }}>{t("v2.notices.hours.h16")}</span>
                        <span style={{ left: 360 }}>{t("v2.notices.hours.h19")}</span>
                    </div>
                </div>
            </div>
            <div className={`act${teleOpen ? " tele-open" : ""}`}>
                <p className="fine tele">
                    {t("v2.notices.usage")}{" "}
                    <button
                        className="link"
                        type="button"
                        onClick={() => setTeleOpen(true)}
                    >
                        {t("v2.notices.change")}
                    </button>
                </p>
                <div className="tele-row">
                    <span id="sv2-tele-label">
                        {teleFailed
                            ? t("v2.notices.usageSaveFailed")
                            : t("v2.notices.usageSwitch")}
                    </span>
                    <button
                        className="sw"
                        type="button"
                        role="switch"
                        aria-checked={telemetry.enabled}
                        aria-labelledby="sv2-tele-label"
                        disabled={telemetry.loading || telemetry.saving}
                        onClick={() => void changeTelemetry()}
                    />
                </div>
                <button
                    className="btn primary"
                    type="button"
                    data-testid="setup-v2-continue"
                    onClick={onNext}
                >
                    {t("v2.continue")}
                </button>
            </div>
        </section>
    );
}

export function escapeHTML(value: string) {
    return value
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;")
        .replace(/"/g, "&quot;");
}
