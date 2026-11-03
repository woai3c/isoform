import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Trans, useTranslation } from "react-i18next";
import {
    DeferModelDownloads,
    GetModelChoices,
    GetModelDownloads,
    GetModelNetworkState,
    GetSetupPermissions,
    PlanSetupModelDownload,
    RequestPermission,
    StartModelDownloads,
} from "@/lib/wails-shim-app";
import type {
    ModelChoices,
    ModelDownload,
    ModelDownloadPlan,
    ModelNetworkState,
} from "@/lib/setup-backend";
import { EventsOn } from "../../../../../wailsjs/runtime/runtime";
import { SetupIcon } from "../SetupIcons";
import type { SceneProps } from "../types";
import {
    chosenModels,
    formatModelBytes,
    MODEL_ROWS,
    networkReasonKey,
    planRequest,
    rowBytes,
    rowProgress,
    shortModelName,
    type ModelRowId,
    type ModelRowSpec,
    type RowsOn,
} from "../models";
import { rowStates } from "./SeesScene";

// Screen 4 (handoff-2026-10-29-1523-setup-v2.2.0 §2, "Runs on this computer"):
// what runs on this computer, by job, with sizes from its own plan. Download
// starts the confirmed models at once, visibly, and Continue is ready
// straight away; Later confirms them for when the computer is plugged in and
// idle. Nothing downloads from any other button.

type Phase = "loading" | "choose" | "downloading";

const ALL_ON: RowsOn = { calls: true, afterCall: true, voice: true, always: true };

export function RunsScene({ onNext, reduced, still, enter }: SceneProps) {
    const { t, i18n } = useTranslation("onboarding");
    const frozen = !!still;
    const [fixture] = useState(() => (still ? stillFixture(still) : null));
    const [choices, setChoices] = useState<ModelChoices | null>(fixture?.choices ?? null);
    const [callsOn, setCallsOn] = useState(true);
    const [on, setOn] = useState<RowsOn>(fixture?.on ?? ALL_ON);
    const [plan, setPlan] = useState<ModelDownloadPlan | null>(fixture?.plan ?? null);
    const [planFailed, setPlanFailed] = useState(false);
    const [phase, setPhase] = useState<Phase>(fixture?.phase ?? "loading");
    const [downloads, setDownloads] = useState<ModelDownload[]>(fixture?.downloads ?? []);
    const [network, setNetwork] = useState<ModelNetworkState | null>(null);
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    const [asking, setAsking] = useState<ModelRowId | null>(null);

    const chosen = useMemo(
        () => (choices ? chosenModels(choices, i18n.language) : {}),
        [choices, i18n.language],
    );

    // What this computer would use, whether Your calls is on, and whether a
    // download setup confirmed is already running (Back from screen 5).
    useEffect(() => {
        if (frozen) return;
        let live = true;
        void Promise.all([
            GetModelChoices(),
            GetSetupPermissions().catch(() => null),
            GetModelDownloads().catch(() => [] as ModelDownload[]),
        ])
            .then(([next, perms, jobs]) => {
                if (!live) return;
                const calls = rowStates(perms).calls === "on";
                const afterCallChosen =
                    next.jobs.find((job) => job.job === "afterCall")?.chosenId !== "none";
                setChoices(next);
                setCallsOn(calls);
                setDownloads(jobs ?? []);
                const running = (jobs ?? []).filter(
                    (job) => !job.deferred && job.state !== "failed",
                );
                if (running.some((job) => job.state !== "done")) {
                    const picked = chosenModels(next, i18n.language);
                    const has = (row: ModelRowSpec) =>
                        row.jobs.some((job) => running.some((d) => d.id === picked[job]?.id));
                    setOn({
                        calls: has(MODEL_ROWS[0]),
                        afterCall: has(MODEL_ROWS[1]),
                        voice: has(MODEL_ROWS[2]),
                        always: true,
                    });
                    setPlan(planFromJobs(running));
                    setPhase("downloading");
                    return;
                }
                setOn({ calls, afterCall: calls && afterCallChosen, voice: true, always: true });
                setPhase("choose");
            })
            .catch(() => {
                if (live) {
                    setPlanFailed(true);
                    setPhase("choose");
                }
            });
        return () => {
            live = false;
        };
        // The language is read once, with the choices.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [frozen]);

    // The plan follows the switches: its total, the free space and whether
    // it fits come from the desktop's planner, never from this screen.
    const revision = useRef(0);
    useEffect(() => {
        if (frozen || phase !== "choose" || !choices) return;
        const request = ++revision.current;
        const { choices: wire, skip } = planRequest(chosen, on);
        PlanSetupModelDownload(wire, skip)
            .then((next) => {
                if (request !== revision.current) return;
                setPlan(next);
                setPlanFailed(false);
            })
            .catch(() => {
                if (request !== revision.current) return;
                setPlan(null);
                setPlanFailed(true);
            });
    }, [frozen, phase, choices, chosen, on]);

    // Real progress while the confirmed models download.
    useEffect(() => {
        if (frozen || phase !== "downloading") return;
        let live = true;
        const readNetwork = () =>
            void GetModelNetworkState()
                .then((next) => live && setNetwork(next))
                .catch(() => {});
        readNetwork();
        const off = EventsOn("models:downloads", (next: ModelDownload[]) => {
            if (!live) return;
            setDownloads(next ?? []);
            readNetwork();
        });
        void GetModelDownloads()
            .then((next) => live && setDownloads(next ?? []))
            .catch(() => {});
        return () => {
            live = false;
            off();
        };
    }, [frozen, phase]);

    const download = useCallback(async () => {
        if (!plan || busy) return;
        if (plan.items.length === 0) {
            onNext();
            return;
        }
        setBusy(true);
        setFailed(false);
        try {
            await StartModelDownloads(plan);
            setPhase("downloading");
        } catch {
            setFailed(true);
            // A refused plan is spent; plan again from the same switches.
            setOn((cur) => ({ ...cur }));
        } finally {
            setBusy(false);
        }
    }, [plan, busy, onNext]);

    const later = useCallback(async () => {
        if (busy) return;
        if (plan && plan.items.length > 0) {
            setBusy(true);
            await DeferModelDownloads(plan).catch((error: unknown) =>
                console.warn("[setup-v2] DeferModelDownloads failed", error),
            );
            setBusy(false);
        }
        onNext();
    }, [plan, busy, onNext]);

    // "Needs Your calls · Turn on": the microphone and system audio prompts,
    // then this row switches on.
    const turnOnCalls = useCallback(
        async (row: ModelRowId) => {
            if (asking) return;
            setAsking(row);
            try {
                const mic = await RequestPermission("microphone");
                const audio = await RequestPermission("system_audio");
                if (mic && audio) {
                    setCallsOn(true);
                    setOn((cur) => ({ ...cur, [row]: true }));
                }
            } catch {
                /* The row stays off; the prompt can be asked again. */
            } finally {
                setAsking(null);
            }
        },
        [asking],
    );

    const downloading = phase === "downloading";
    const shortfall = plan ? Math.max(0, plan.requiredBytes - plan.freeBytes) : 0;
    const fits = !!plan && plan.enoughSpace;
    const reason = downloading ? networkReasonKey(downloads, network) : null;
    const nonCommercial = (row: ModelRowSpec) =>
        row.jobs
            .map((job) => chosen[job])
            .find((entry) => entry?.licence.nonCommercial && plan?.items.some((i) => i.id === entry.id));

    const modelLine = (row: ModelRowSpec) => {
        const size = formatModelBytes(rowBytes(row, chosen));
        switch (row.id) {
            case "calls":
                return t("v2.runs.rows.calls.model", { name: shortModelName(chosen.live), size });
            case "afterCall":
                return t("v2.runs.rows.afterCall.model", { name: shortModelName(chosen.afterCall), size });
            case "voice":
                return t(
                    chosen.voice?.languages.includes("zh")
                        ? "v2.runs.rows.voice.modelBilingual"
                        : "v2.runs.rows.voice.model",
                    { name: shortModelName(chosen.voice), size },
                );
            default:
                return t("v2.runs.rows.always.model", { size });
        }
    };

    return (
        <section className={`scene ${enter}`} data-testid="setup-v2-runs">
            <h2 className="t">{t("v2.runs.title")}</h2>
            <p className="s">{t("v2.runs.line")}</p>
            <div className="ill">
                <div className={`mods${reduced ? " still" : ""}`}>
                    {MODEL_ROWS.map((row) => {
                        const rowOn = row.switchable ? on[row.id] : true;
                        const needs = row.needsCalls && !callsOn && !rowOn && !downloading;
                        const name = t(`v2.runs.rows.${row.id}.name`);
                        const licence = !downloading ? nonCommercial(row) : undefined;
                        const progress =
                            downloading && rowOn && plan ? rowProgress(row, plan, chosen, downloads) : 0;
                        return (
                            <div
                                key={row.id}
                                className={`mr${rowOn ? "" : " off"}${downloading && rowOn ? " dl" : ""}${row.switchable ? "" : " fixed"}`}
                                data-testid={`setup-v2-model-${row.id}`}
                                data-on={rowOn}
                            >
                                <span className="mi">
                                    <SetupIcon id={row.icon} />
                                </span>
                                <div>
                                    <b>{name}</b>
                                    <span>{t(`v2.runs.rows.${row.id}.line`)}</span>
                                    <em>{modelLine(row)}</em>
                                    {licence && (
                                        <em className="lic">
                                            {t("models.nonCommercial", {
                                                name: licence.displayName,
                                                licence: licence.licence.name,
                                            })}
                                        </em>
                                    )}
                                    {needs && (
                                        <em className="need">
                                            {t("v2.runs.needsCalls")}{" "}
                                            <button
                                                className="link"
                                                type="button"
                                                data-testid={`setup-v2-model-${row.id}-turn-on`}
                                                disabled={!!asking}
                                                onClick={() => void turnOnCalls(row.id)}
                                            >
                                                {t("v2.runs.turnOn")}
                                            </button>
                                        </em>
                                    )}
                                </div>
                                <span className="ctl">
                                    {downloading ? (
                                        rowOn && (
                                            <span className="pct">
                                                {t("v2.runs.percent", {
                                                    // 100% only once every file is on disk.
                                                    percent:
                                                        progress >= 1 ? 100 : Math.min(99, Math.round(progress * 100)),
                                                })}
                                            </span>
                                        )
                                    ) : row.switchable ? (
                                        <button
                                            className="tg"
                                            type="button"
                                            role="switch"
                                            aria-checked={rowOn}
                                            aria-label={name}
                                            disabled={phase !== "choose" || (row.needsCalls && !callsOn)}
                                            onClick={() => setOn((cur) => ({ ...cur, [row.id]: !cur[row.id] }))}
                                        />
                                    ) : (
                                        <span className="al">{t("v2.runs.alwaysOn")}</span>
                                    )}
                                </span>
                                <i className="bar" style={{ transform: `scaleX(${progress})` }} />
                            </div>
                        );
                    })}
                </div>
            </div>
            <div className="act">
                <div
                    className={`tot${downloading && !reason ? " ok" : ""}${!downloading && plan && !fits ? " error" : ""}${reason ? " wait" : ""}`}
                    aria-live="polite"
                    data-testid="setup-v2-models-total"
                >
                    {downloading
                        ? t(reason ?? "v2.runs.downloading")
                        : plan
                          ? fits
                              ? t("v2.runs.total", {
                                    total: formatModelBytes(plan.totalBytes),
                                    free: formatModelBytes(plan.freeBytes),
                                })
                              : t("models.shortfall", { size: formatModelBytes(shortfall) })
                          : planFailed
                            ? t("models.loadFailed")
                            : ""}
                </div>
                <button
                    className="btn primary"
                    type="button"
                    data-testid="setup-v2-continue"
                    disabled={!downloading && (busy || !plan || !fits)}
                    onClick={() => (downloading ? onNext() : void download())}
                >
                    {downloading || (plan && plan.items.length === 0)
                        ? t("v2.continue")
                        : t("v2.runs.download", {
                              total: formatModelBytes(plan?.totalBytes ?? 0),
                          })}
                </button>
                {!downloading && (
                    <p className={`fine${failed ? " error" : ""}`}>
                        {failed ? (
                            t("models.startFailed")
                        ) : (
                            <Trans
                                t={t}
                                i18nKey="v2.runs.fine"
                                components={{
                                    later: (
                                        <button
                                            className="link"
                                            type="button"
                                            data-testid="setup-v2-models-later"
                                            disabled={busy || phase === "loading"}
                                            onClick={() => void later()}
                                        />
                                    ),
                                }}
                            />
                        )}
                    </p>
                )}
            </div>
        </section>
    );
}

/** The rows' progress after Back: the confirmed jobs stand in for the plan. */
function planFromJobs(jobs: ModelDownload[]): ModelDownloadPlan {
    return {
        planId: "",
        choices: {},
        requiredBytes: 0,
        items: jobs.map((job) => ({
            id: job.id,
            bytes: job.bytesTotal,
            fileCount: 0,
            source: { kind: "mirror", url: "" },
            host: "",
            licence: { name: "", url: "", nonCommercial: false },
            nonCommercial: false,
        })),
        totalBytes: jobs.reduce((sum, job) => sum + job.bytesTotal, 0),
        freeBytes: 0,
        enoughSpace: true,
        modelsFolder: "",
    };
}

// The development gallery's frozen states (handoff §9): today's recommended
// set, as the design stills draw it. Live screens read the desktop's plan.
const STILL_SIZES: Record<string, number> = {
    sensevoice: 239_549_735,
    "speaker-pack": 30_465_498,
    "qwen3-asr-0.6b": 878_702_423,
    "kokoro-en": 319_625_534,
    "melotts-zh-en": 167_006_755,
    "gliner-pii": 673_414_035,
    minilm: 90_636_722,
};
const STILL_NAMES: Record<string, string> = {
    sensevoice: "SenseVoice",
    "speaker-pack": "Speaker pack",
    "qwen3-asr-0.6b": "Qwen3-ASR 0.6B",
    "kokoro-en": "Kokoro",
    "melotts-zh-en": "MeloTTS",
    "gliner-pii": "GLiNER privacy filter",
    minilm: "MiniLM memory search",
};
const STILL_JOBS: Array<[ModelChoices["jobs"][number]["job"], string[]]> = [
    ["live", ["sensevoice"]],
    ["speakerPack", ["speaker-pack"]],
    ["afterCall", ["qwen3-asr-0.6b"]],
    ["voice", ["kokoro-en", "melotts-zh-en"]],
    ["privacy", ["gliner-pii"]],
    ["memory", ["minilm"]],
];

function stillFixture(still: NonNullable<SceneProps["still"]>) {
    const choices: ModelChoices = {
        jobs: STILL_JOBS.map(([job, ids]) => ({
            job,
            chosenId: ids[0],
            locked: false,
            alternatives: ids.map((id) => ({
                id,
                job,
                displayName: STILL_NAMES[id],
                line: "",
                evidence: "",
                sizeBytes: STILL_SIZES[id],
                files: [],
                sources: [],
                licence: { name: "", url: "", nonCommercial: false },
                recommended: id === ids[0],
                languages: id === "melotts-zh-en" ? ["zh", "en"] : ["en"],
                downloaded: false,
            })),
        })),
        totalBytesToDownload: 0,
        freeBytes: 412e9,
        modelsFolder: "",
    };
    const on: RowsOn = { ...ALL_ON, afterCall: still !== "runs-off" };
    const ids = STILL_JOBS.filter(([job]) => on.afterCall || job !== "afterCall").map(([, ids]) => ids[0]);
    const plan: ModelDownloadPlan = {
        ...planFromJobs(
            ids.map((id) => ({
                id,
                state: "queued",
                pauseReason: "",
                bytesDone: 0,
                bytesTotal: STILL_SIZES[id],
                error: "",
            })),
        ),
        freeBytes: 412e9,
    };
    // The design still's percentages: 62, 18, 41 and 27.
    const share: Record<string, number> = {
        sensevoice: 0.62,
        "speaker-pack": 0.62,
        "qwen3-asr-0.6b": 0.18,
        "kokoro-en": 0.41,
        "gliner-pii": 0.27,
        minilm: 0.27,
    };
    const downloads: ModelDownload[] =
        still === "runs-downloading"
            ? ids.map((id) => ({
                  id,
                  state: "downloading",
                  pauseReason: "",
                  bytesDone: Math.round(STILL_SIZES[id] * share[id]),
                  bytesTotal: STILL_SIZES[id],
                  error: "",
              }))
            : [];
    return {
        choices,
        on,
        plan,
        downloads,
        phase: (still === "runs-downloading" ? "downloading" : "choose") as Phase,
    };
}
