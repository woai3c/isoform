// Setup v2 gallery (handoff §9): each frozen state at 600 x 560, light or
// dark, with no desktop process or browser bridge contacted. Without
// ?still= it runs the live flow against fixture answers.
import { createRoot } from "react-dom/client";
import i18n from "@/i18n";
import "@/index.css";
import { ThemeProvider } from "@/hooks/useTheme";
import { OnboardingScreen } from "@/components/onboarding-screen";
import { SETUP_V2_STILLS, type SetupV2Still } from "@/components/onboarding/v2/types";
import type { OnboardingState } from "@/types";

const params = new URLSearchParams(location.search);
const stillParam = params.get("still") ?? "";
const still = (SETUP_V2_STILLS as readonly string[]).includes(stillParam)
    ? (stillParam as SetupV2Still)
    : undefined;
try {
    localStorage.setItem("yansu.theme", params.get("theme") === "dark" ? "onedark" : "paper");
    localStorage.removeItem("isoform.setup.agent-probe.v1");
} catch {
    /* The gallery still renders. */
}

const probe = {
    agents: [
        { id: "claude", name: "Claude Code", installed: true, signedIn: true, plan: "Max" },
        { id: "codex", name: "Codex", installed: true, signedIn: true, plan: "Plus" },
    ],
    npm: true,
};
const reads: Record<string, unknown> = {
    LoadUIState: params.get("screen") ?? "",
    GetAgentProbe: probe,
    GetModelAccountsStatus: { accountOrder: [], consent: { accepted: false } },
    GetSetupPermissions: { answers: [], running: false, resumeStep: "sees", next: 0 },
    GetScreenRecordingRelaunch: { needed: false, app: "Isoform" },
    GetTelemetryTier: "detailed",
    GetWhatIsoformSees: { assistant: "claude_code", sources: [
        { job: "mail", on: true }, { job: "calendar", on: true }, { job: "calls", on: true },
        { job: "screen", on: true }, { job: "assistant", on: true }, { job: "contacts", on: false },
    ] },
    GetMobileConnectionStatus: {
        enabled: true,
        connectionCode: "isoform://pair#setup-gallery-sample",
        expiresAt: "",
        pendingPairing: null,
        devices: [],
    },
    PlanModelDownload: { items: [], totalBytes: 3.9e9, enoughSpace: true },
};
(window as unknown as { go: unknown }).go = {
    main: {
        App: new Proxy(
            {},
            {
                get: (_, method: string) => async () => structuredClone(reads[method] ?? null),
            },
        ),
    },
};

const state = {
    version: 2,
    completed: false,
    neverShowAgain: false,
    permissionsComplete: false,
    aboutYou: { busyWith: "", repeatedWeekly: "", wishHandled: "" },
    selectedProviders: [],
    completedProviders: [],
    providerChannelSelections: {},
} as OnboardingState;
const noop = () => {};

void i18n.changeLanguage(params.get("lang") ?? "en").then(() =>
    createRoot(document.getElementById("root")!).render(
        <ThemeProvider>
            <OnboardingScreen
                state={state}
                still={still}
                onStateChange={noop}
                onFinish={() => {
                    document.body.dataset.setupFinished = "true";
                }}
                onSkip={() => {
                    document.body.dataset.setupSkipped = "true";
                }}
            />
        </ThemeProvider>,
    ),
);
