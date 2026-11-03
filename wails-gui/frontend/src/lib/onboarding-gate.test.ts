import { describe, expect, it } from "vitest";
import { onboardingFinished } from "./onboarding-gate";

describe("onboardingFinished", () => {
    it("is false while onboarding was only hidden with Escape", () => {
        // Escape sets a session flag and leaves the saved state untouched.
        expect(onboardingFinished({ completed: false, neverShowAgain: false })).toBe(false);
    });

    it("is true once onboarding was finished or turned off for good", () => {
        expect(onboardingFinished({ completed: true, neverShowAgain: false })).toBe(true);
        expect(onboardingFinished({ completed: false, neverShowAgain: true })).toBe(true);
    });
});
