import type { PlaybooksState, PlaybookView, SopEntry } from '@/lib/wails-shim-app';

/** A playbook with the plan Isoform keeps for it (schedule, last run), when it has one. */
export type PlaybookListItem = {
    entry: SopEntry;
    plan: PlaybookView | null;
};

/** True while Isoform has offered this playbook and the user has not answered yet. */
export function hasPendingOffer(plan: PlaybookView | null | undefined): boolean {
    return !!plan?.proposal && !plan.proposal.answer;
}

/** True while an unanswered offer should show in Tasks › Up next: not
 *  hidden by Not now (two weeks). */
export function offerShowsNow(plan: PlaybookView | null | undefined, now = Date.now()): boolean {
    return hasPendingOffer(plan) && (plan?.proposal?.snoozedUntil ?? 0) <= now;
}

function withPlans(sops: SopEntry[] | null | undefined, playbooks: PlaybooksState | null | undefined): PlaybookListItem[] {
    const plans = playbooks?.plans ?? [];
    const rows = Array.isArray(sops) ? sops : [];
    return rows.map((entry) => ({ entry, plan: plans.find((plan) => plan.id === entry.id) ?? null }));
}

/**
 * Confirmed playbooks, newest first. This is the only list How you do things
 * shows: "Isoform can do these for you". An open offer is not confirmed yet,
 * so it is left out even when its status says confirmed.
 */
export function listConfirmedPlaybooks(
    sops: SopEntry[] | null | undefined,
    playbooks: PlaybooksState | null | undefined,
): PlaybookListItem[] {
    return withPlans(sops, playbooks)
        .filter((item) => item.entry.status === 'confirmed' && !hasPendingOffer(item.plan))
        .sort((a, b) => b.entry.updatedAt - a.entry.updatedAt);
}

/**
 * Drafts, newest first: every playbook that is not confirmed (Isoform drafted
 * it from your activity), plus any playbook with an open offer. Nothing on How
 * you do things lists these. Tasks › Up next turns them into offers.
 */
export function listDraftPlaybooks(
    sops: SopEntry[] | null | undefined,
    playbooks: PlaybooksState | null | undefined,
): PlaybookListItem[] {
    return withPlans(sops, playbooks)
        .filter((item) => item.entry.status !== 'confirmed' || hasPendingOffer(item.plan))
        .sort((a, b) => b.entry.updatedAt - a.entry.updatedAt);
}
