export const SETUP_V2_SCREENS = [
    "notices",
    "ai",
    "sees",
    "runs",
    "send",
    "reaches",
] as const;
export type SetupV2Screen = (typeof SETUP_V2_SCREENS)[number];

/**
 * The frozen states of the development gallery (handoff §9), the same as the
 * design stills; "on-this-mac" is Notices with the v2.1.0 popover open.
 */
export const SETUP_V2_STILLS = [
    "on-this-mac",
    "notices",
    "ai-empty",
    "ai-picked",
    "ai-one",
    "ai-none",
    "sees",
    "sees-on",
    "runs",
    "runs-off",
    "runs-downloading",
    "send",
    "reaches",
    "reaches-paired",
    "finish-tile",
    "finish-chat",
] as const;
export type SetupV2Still = (typeof SETUP_V2_STILLS)[number];

export interface SceneProps {
    onNext: () => void;
    /** Reduce Motion: each screen shows its finished state, no loops. */
    reduced: boolean;
    /** Gallery: render this frozen state with no backend reads. */
    still?: SetupV2Still;
    /** "enter" for the rise-in, "fade" for the Reduce Motion crossfade. */
    enter: string;
}
