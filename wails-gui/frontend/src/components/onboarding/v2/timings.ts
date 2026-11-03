// Every wait and tween of setup v2, in milliseconds, from the handoff's
// motion table (docs/handoff-2026-10-28/handoff-2026-10-28-1215-setup-v2.0.0.md
// §5) and the prototype. A new handoff changes the numbers here; the scenes
// only read them. The screen order is SETUP_V2_SCREENS in types.ts and the
// copy is the onboarding namespace's v2 keys.

export const TIMINGS = {
    notices: {
        start: 300,
        sweep: 9000,
        hold: 1300,
        fade: 450,
    },
    ai: {
        /** A single available assistant fills Answers first after this. */
        autoPickFirst: 1000,
        /** The same, for one that appears later (a sign-in finished). */
        autoPickLater: 450,
        start: 500,
        tokenIn: 40,
        tokenHold: 750,
        toSlot: 420,
        toWait: 200,
        waitHold: 2000,
        waitFade: 450,
        waitRest: 700,
        toCard: 320,
        toLimit: 300,
        limitHold: 300,
        toBackup: 380,
        thinking: 1100,
        back: 620,
        readyHold: 1800,
        readyFade: 420,
        rest: 400,
        coachIn: 480,
        coachDrag: 720,
        coachHold: 260,
        coachOut: 500,
        /** While Terminal is open for a sign-in or install, re-check this often. */
        recheck: 3000,
        /** The test call waits this long for a gateway that is still starting… */
        testRetry: 2500,
        /** …and tries this many times in all. */
        testTries: 4,
    },
    sees: {
        start: 450,
        sweep: 2300,
        rest: 1400,
        /** Rows flip On at least this far apart. */
        rowFlip: 380,
        /** After the last row, before the reopen. */
        reopen: 320,
    },
    send: {
        ask: 350,
        mail: 500,
        cursorIn: 350,
        toText: 520,
        typeChar: 24,
        typeSpace: 14,
        beforeSend: 250,
        toSend: 460,
        atSend: 420,
        backAway: 420,
        readyIn: 300,
        personIn: 900,
        personMove: 620,
        beforePress: 140,
        press: 150,
        sentHold: 500,
        rest: 2200,
        fade: 420,
    },
    reaches: {
        notice1: 650,
        notice2: 1300,
        copied: 1600,
        /** "Notifications on" holds this long before the finish. */
        notificationsOn: 950,
        notificationsOnReduced: 300,
        /** The pairing code changes this often. */
        codeRefresh: 5 * 60 * 1000,
    },
    finish: {
        badge: 380,
        hold: 1000,
        /** The window's own fade when there is no native flight. */
        fallbackFade: 430,
    },
} as const;
