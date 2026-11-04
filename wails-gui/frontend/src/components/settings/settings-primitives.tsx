import { SettingsFailureLine, type SettingsFailure } from "./settings-failure";
// Card, row and section-header primitives shared by every Settings page.
// settings-page.tsx re-exports them so existing imports keep working.

// ── Reusable settings card ──

export function SettingsCard({
    title,
    description,
    headerRight,
    children,
}: {
    title?: string;
    description?: string;
    headerRight?: React.ReactNode;
    children: React.ReactNode;
}) {
    const hasHeader = Boolean(title) || Boolean(headerRight);
    return (
        <div className="settings-card settings-card--padded">
            {hasHeader && (
                <div className={`flex items-center justify-between ${description ? "mb-1" : "mb-3"}`}>
                    {title ? (
                        <h2 className="settings-card-title">{title}</h2>
                    ) : (
                        <span />
                    )}
                    {headerRight}
                </div>
            )}
            {description && (
                <p className="settings-card-desc mb-4">{description}</p>
            )}
            {children}
        </div>
    );
}

/**
 * A single label + description + control row used inside SettingsCard.
 * Layout matches the Littlebird reference: left-aligned title/description,
 * right-aligned control (Switch, Select, Button, Badge, etc).
 */
export function SettingsRow({
    title,
    description,
    control,
    children,
    failure,
}: {
    title: string;
    description?: React.ReactNode;
    control?: React.ReactNode;
    children?: React.ReactNode;
    /** An in-place failure under the row's words (settings review ST9). */
    failure?: SettingsFailure | null;
}) {
    return (
        <div className="settings-row">
            <div className="flex flex-col min-w-0">
                <span className="settings-row-title truncate">{title}</span>
                {description && (
                    <span className="settings-row-desc">{description}</span>
                )}
                <SettingsFailureLine failure={failure ?? null} />
            </div>
            <div className="shrink-0 flex items-center gap-2">
                {control ?? children}
            </div>
        </div>
    );
}

/**
 * A top-level settings section header — used for grouping cards under a
 * shared label (e.g. "Manage Context Collected" above a group of action rows).
 */
export function SettingsSectionHeader({
    title,
    description,
}: {
    title: string;
    description?: string;
}) {
    return (
        <div className="flex flex-col gap-1 -mb-1">
            <h2 className="settings-section-heading">{title}</h2>
            {description && (
                <p className="settings-card-desc">{description}</p>
            )}
        </div>
    );
}
