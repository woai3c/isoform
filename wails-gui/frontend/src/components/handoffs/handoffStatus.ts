/**
 * Status copy for one-time work (Handoff entries). This module only decides
 * which i18n key applies; callers resolve it with t() so labels follow the
 * active language. Keys live under `handoffsPage` in the app namespace.
 */

export type HandoffStatusKey =
    | "handoffsPage.status.active"
    | "handoffsPage.status.in_progress"
    | "handoffsPage.status.blocked"
    | "handoffsPage.status.needs_review"
    | "handoffsPage.status.completed"
    | "handoffsPage.status.stale"
    | "handoffsPage.status.aborted"
    | "handoffsPage.status.unavailable";

export type HandoffBlockedReasonKey =
    | "handoffsPage.blockedReason.interrupted"
    | "handoffsPage.blockedReason.timeout";

/**
 * The fields this module reads. Structural so saved entries and main-chat work
 * cards use the same lifecycle labels.
 */
export interface HandoffStatusFields {
    status: string;
    source?: string;
    blockedReason?: string;
}

/** Check results for a run that has ended. `note` explains a zero total. */
export interface HandoffVerification {
    met: number;
    total: number;
    note?: string;
    reason?: string;
}

/**
 * The line shown under a title. Callers resolve `key` with t() and pass the
 * values through; backend diagnostic notes are never rendered.
 */
export type HandoffVerificationLine =
    | {
          kind: "checks";
          key: "handoffsPage.verification.summary";
          met: number;
          total: number;
      }
    | { kind: "none"; key: "handoffsPage.verification.none" }
    | { kind: "reason"; key: HandoffVerificationReasonKey };

const verificationReasons = {
    private_review: "handoffsPage.verification.reason.private_review",
    no_criteria: "handoffsPage.verification.reason.no_criteria",
    invalid_result: "handoffsPage.verification.reason.invalid_result",
    incomplete_result: "handoffsPage.verification.reason.incomplete_result",
    invalid_evidence: "handoffsPage.verification.reason.invalid_evidence",
    check_unavailable: "handoffsPage.verification.reason.check_unavailable",
    invalid_check_reply: "handoffsPage.verification.reason.invalid_check_reply",
    criteria_unconfirmed: "handoffsPage.verification.reason.criteria_unconfirmed",
} as const;
type HandoffVerificationReasonKey = (typeof verificationReasons)[keyof typeof verificationReasons];

/**
 * Reads `blockedReason` from an entry, tolerating older saved records.
 */
export function handoffBlockedReason(entry: object): string | undefined {
    return (entry as { blockedReason?: string }).blockedReason;
}

/**
 * Reads the success criteria parsed from the brief. Older entries and the
 * backend's null slice both yield an empty list.
 */
export function handoffCriteria(entry: object): string[] {
    const raw = (entry as { criteria?: unknown }).criteria;
    if (!Array.isArray(raw)) return [];
    return raw.filter(
        (item): item is string =>
            typeof item === "string" && item.trim() !== "",
    );
}

/** Reads the verifier result, or null before a run has ended. */
export function handoffVerification(entry: object): HandoffVerification | null {
    const raw = (entry as { verification?: unknown }).verification;
    if (!raw || typeof raw !== "object") return null;
    const { met, total, note, reason } = raw as {
        met?: unknown;
        total?: unknown;
        note?: unknown;
        reason?: unknown;
    };
    if (typeof met !== "number" || typeof total !== "number") return null;
    return {
        met,
        total,
        note: typeof note === "string" ? note : undefined,
        reason: typeof reason === "string" ? reason : undefined,
    };
}

/**
 * Picks the verification line for a result: "N of M checks met" when there
 * were checks, otherwise a translated reason or fixed legacy fallback.
 */
export function handoffVerificationLine(
    verification: HandoffVerification | null,
): HandoffVerificationLine | null {
    if (!verification) return null;
    if (verification.total > 0) {
        return {
            kind: "checks",
            key: "handoffsPage.verification.summary",
            met: verification.met,
            total: verification.total,
        };
    }
    const reason = verification.reason;
    if (reason && Object.prototype.hasOwnProperty.call(verificationReasons, reason)) {
        return { kind: "reason", key: verificationReasons[reason as keyof typeof verificationReasons] };
    }
    return { kind: "none", key: "handoffsPage.verification.none" };
}

/**
 * True for Isoform-originated work the runtime has retired: the user already
 * did it, its source ended or its evidence was invalidated. The plan stays for
 * reference only; the work can no longer be started.
 */
export function isRetiredAgencyHandoff(entry: HandoffStatusFields): boolean {
    return entry.source === "agency" && entry.status === "stale";
}

/** Status label key, or null when the status is not one this surface knows. */
export function handoffStatusKey(
    entry: HandoffStatusFields,
): HandoffStatusKey | null {
    if (isRetiredAgencyHandoff(entry)) {
        return "handoffsPage.status.unavailable";
    }
    switch (entry.status) {
        case "active":
            return "handoffsPage.status.active";
        case "in_progress":
            return "handoffsPage.status.in_progress";
        case "blocked":
            return "handoffsPage.status.blocked";
        case "needs_review":
            return "handoffsPage.status.needs_review";
        case "completed":
            return "handoffsPage.status.completed";
        case "stale":
            return "handoffsPage.status.stale";
        case "aborted":
            return "handoffsPage.status.aborted";
        default:
            return null;
    }
}

/** Reason line for a blocked entry, or null when not blocked or reason unknown. */
export function handoffBlockedReasonKey(
    entry: HandoffStatusFields,
): HandoffBlockedReasonKey | null {
    if (entry.status !== "blocked") return null;
    switch (entry.blockedReason) {
        case "interrupted":
            return "handoffsPage.blockedReason.interrupted";
        case "timeout":
            return "handoffsPage.blockedReason.timeout";
        default:
            return null;
    }
}
