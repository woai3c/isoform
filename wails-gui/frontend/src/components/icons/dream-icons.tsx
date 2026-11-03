interface DreamIconProps {
    className?: string;
}

// Why these icons exist
// --------------------
// Per project rule, generic lucide line glyphs look like AI slop when
// they're the *primary visual element* on a product surface. The
// Memory Dream dialog has three such moments:
//   1. The title chip (a moon — the "nightly consolidation" mark).
//   2. The safety-panel header badge (a shield — "protected memories").
//   3. The per-row check chip in the safety panel (a checkmark — each
//      individual safety commitment).
//
// Keep the existing silhouettes and layering. Brand violet uses neutral surface,
// border and ink tokens for these illustrations; they carry no status meaning.

/**
 * DreamMoonIcon — title mark for "Memory Dream".
 *
 * A clean crescent moon in a single neutral surface, set inside a faint
 * accent halo, with two tiny surface highlights off to the upper-left.
 *
 * Earlier revisions tried to add depth with a second "inner crescent
 * rim" layer on top of the main body. That second path was a separate
 * crescent — different center, different sweep — so when stacked it
 * carved a weird bite out of the silhouette and the moon stopped
 * reading as a moon. The dimension here now comes from the halo +
 * stars composition rather than layered fills on the moon body
 * itself.
 */
export function DreamMoonIcon({ className }: DreamIconProps) {
    return (
        <svg
            viewBox="0 0 24 24"
            fill="none"
            className={className}
            xmlns="http://www.w3.org/2000/svg"
            aria-hidden
        >
            {/* Faint accent halo, suggests "nighttime aura". */}
            <circle cx="12" cy="12" r="10.5" fill="var(--color-icon-subtle)" fillOpacity="0.12" />

            {/*
              Crescent body — single solid neutral surface.
              Path geometry: a big sweep on an 8.5-radius circle from
              (20.2, 13.6) round to (10.4, 3.8), then a smaller 6.6-
              radius arc swept the other way back to start — that
              return arc carves the cradle of the crescent. Both arcs
              live on the same overall circle so the silhouette stays a
              clean moon shape (not the broken bite from before).
            */}
            <path
                d="M20.2 13.6A8.5 8.5 0 1 1 10.4 3.8a6.6 6.6 0 0 0 9.8 9.8z"
                fill="var(--color-icon-subtle)"
            />

            {/* Tiny sparkle stars off to the side — surface highlights so they
                read as glints against the neutral body, not as a second
                hue muddying the palette. */}
            <circle cx="5.8" cy="6.4" r="0.85" fill="var(--color-panel)" />
            <circle cx="7.2" cy="3.6" r="0.45" fill="var(--color-panel)" fillOpacity="0.85" />
        </svg>
    );
}

/**
 * DreamShieldIcon — section header for the safety panel.
 *
 * Rounded heraldic shield in flat taupe with a white checkmark. The
 * earlier version used a linearGradient body fill which read as
 * glossy; this version is fully flat. Depth comes from a slightly
 * deeper-taupe back-shadow path offset down-right (still solid colors,
 * just two stacked silhouettes).
 */
export function DreamShieldIcon({ className }: DreamIconProps) {
    return (
        <svg
            viewBox="0 0 24 24"
            fill="none"
            className={className}
            xmlns="http://www.w3.org/2000/svg"
            aria-hidden
        >
            {/* Back shadow — solid darker taupe at low opacity, offset
                down-right. Reads as a soft drop without resorting to a
                CSS filter (which would clip in the badge slot). */}
            <path
                d="M12 3l7.2 2.5v5c0 4.8-3.1 8.9-7.2 10.2-4.1-1.3-7.2-5.4-7.2-10.2v-5z"
                transform="translate(0.4,0.5)"
                fill="var(--color-icon-subtle)"
                fillOpacity="0.18"
            />
            {/* Shield body — single flat color. */}
            <path
                d="M12 3l7.2 2.5v5c0 4.8-3.1 8.9-7.2 10.2-4.1-1.3-7.2-5.4-7.2-10.2v-5z"
                fill="var(--color-icon-subtle)"
            />
            {/* Checkmark — surface stroke, generous weight so it reads at 16px. */}
            <path
                d="M8.4 12l2.6 2.6 4.6-5"
                stroke="var(--color-panel)"
                strokeWidth="2"
                strokeLinecap="round"
                strokeLinejoin="round"
            />
        </svg>
    );
}

/**
 * DreamCheckIcon — per-row safety chip.
 *
 * Small flat taupe circle with a white checkmark. The earlier version
 * used a vertical linearGradient for shading that read as glossy; this
 * version is a single solid color. Compact (~16px), used multiple
 * times in the safety list so it stays visually quiet.
 */
export function DreamCheckIcon({ className }: DreamIconProps) {
    return (
        <svg
            viewBox="0 0 16 16"
            fill="none"
            className={className}
            xmlns="http://www.w3.org/2000/svg"
            aria-hidden
        >
            {/* Filled circle — single flat color. */}
            <circle cx="8" cy="8" r="7" fill="var(--color-icon-subtle)" />
            {/* Check stroke. */}
            <path
                d="M5 8.2l2.1 2.1 4-4.6"
                stroke="var(--color-panel)"
                strokeWidth="1.6"
                strokeLinecap="round"
                strokeLinejoin="round"
            />
        </svg>
    );
}
