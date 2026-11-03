import type { OnboardingState } from "@/types";

/**
 * onboardingFinished reports whether onboarding is over for good: finished,
 * or turned off with "never show again". Escape hides onboarding for the session
 * only and does not count, because onboarding comes back on the next launch.
 * The main chat's setup waits for this, so its first message carries what
 * onboarding asked instead of asking it again.
 */
export function onboardingFinished(
    state: Pick<OnboardingState, "completed" | "neverShowAgain">,
): boolean {
    return state.completed || state.neverShowAgain;
}
