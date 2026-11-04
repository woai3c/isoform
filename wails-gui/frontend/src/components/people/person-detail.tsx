// The person detail beside the Who you know overview: who they are, Brief me
// and Draft a reply in place, what is open, your history together, shared
// topics and where Isoform saw them, then everything else folded below. A
// section shows only when it has something in it.
import { useCallback, useEffect, useRef, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { GitMerge, Link, MoreHorizontal, Pencil, Trash2, Unlink, UserPlus } from 'lucide-react';
import { type PersonCard, type PersonDetail, type PrincipalActivitySessionDTO, type PrincipalDTO, type PrincipalWithStatsDTO } from '@/lib/wails-shim-app';
import { GetPersonDetail, ListPrincipalActivitySessions } from '@/lib/page-queries';
import { pageQueryCache } from '@/lib/query-cache';
import { Avatar } from '../ui/avatar';
import { Button } from '../ui/button';
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSeparator,
    DropdownMenuTrigger,
} from '../ui/dropdown-menu';
import { appLabel, dayWords, listWords, loopChip, loopLine, readWithRetry, relationshipLabel, togetherWords, type Translate } from './people-format';
import { PeopleFailureLine, usePeopleDevTools } from './people-failure';
import { PersonBriefBlock, PersonReplyDraft } from './person-brief';
import { ActivitySessionsList, DetailFold, PersonDossier, PersonTechnicalDetails, RecentMessagesList, VoiceFingerprint } from './person-sections';

export interface PersonActions {
    onEdit: (p: PrincipalDTO) => void;
    onPromote: (p: PrincipalDTO) => void;
    onDelete: (p: PrincipalDTO) => void;
    onMerge: (p: PrincipalDTO) => void;
    onLink: (p: PrincipalDTO) => void;
    onUnlink: (p: PrincipalDTO) => void;
}

export function PersonDetailPanel({
    personId,
    card,
    entry,
    revision,
    actions,
    notice,
    onOpenActivitySession,
}: {
    personId: string;
    card?: PersonCard;
    entry?: PrincipalWithStatsDTO;
    /** Bumped by the page after a change, so the detail reads again. */
    revision: number;
    actions: PersonActions;
    /** A failed action on this person (delete, unlink), at the top. */
    notice?: ReactNode;
    onOpenActivitySession?: (sessionId: string, anchorTimestamp?: number, principalName?: string) => void;
}) {
    const { t, i18n } = useTranslation('people');
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const [detail, setDetail] = useState<PersonDetail | null>(
        () => pageQueryCache.peek<PersonDetail>('GetPersonDetail', [personId]) ?? null,
    );
    const [failed, setFailed] = useState(false);
    const [attempt, setAttempt] = useState(0);
    const reread = useCallback(() => setAttempt((n) => n + 1), []);
    const lastRevision = useRef(revision);
    const devTools = usePeopleDevTools();
    const [draftFor, setDraftFor] = useState<string | null>(null);
    const peekSessions = () => pageQueryCache.peek<PrincipalActivitySessionDTO[]>('ListPrincipalActivitySessions', [personId, 50]) ?? null;
    const [sessions, setSessions] = useState<PrincipalActivitySessionDTO[] | null>(peekSessions);
    const [sessionsFailed, setSessionsFailed] = useState(false);
    const [sessionsAttempt, setSessionsAttempt] = useState(0);

    // Activity sessions are read along with the detail: their fold shows
    // only when the person has at least one.
    useEffect(() => {
        let alive = true;
        setSessions(peekSessions());
        setSessionsFailed(false);
        setDraftFor(null);
        if (sessionsAttempt > 0) pageQueryCache.invalidate(['ListPrincipalActivitySessions'], true, [personId, 50]);
        ListPrincipalActivitySessions(personId, 50)
            .then((rows) => {
                if (alive) setSessions(rows ?? []);
            })
            .catch(() => {
                if (alive) setSessionsFailed(true);
            });
        return () => {
            alive = false;
        };
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [personId, sessionsAttempt]);

    // A new person paints from the cache when warm (a retained selection or a
    // repeated deep link); a re-read keeps what is shown.
    useEffect(() => {
        setDetail(pageQueryCache.peek<PersonDetail>('GetPersonDetail', [personId]) ?? null);
        setFailed(false);
    }, [personId]);

    useEffect(() => {
        let alive = true;
        // The page's revision bump (a people change) means re-read from the
        // source, not a fresh-cache answer; the page-queries event map also
        // clears this name on contacts/privacy events.
        const revisionChanged = revision !== lastRevision.current;
        lastRevision.current = revision;
        if (attempt > 0 || revisionChanged) pageQueryCache.invalidate(['GetPersonDetail'], true, [personId]);
        readWithRetry(() => GetPersonDetail(personId))
            .then((next) => {
                if (!alive) return;
                setDetail(next);
                setFailed(false);
            })
            .catch(() => {
                // A later failed re-read keeps the detail on screen and says nothing.
                if (alive) setFailed(true);
            });
        return () => {
            alive = false;
        };
    }, [personId, revision, attempt]);

    const principal = entry?.principal;
    const kind = principal?.kind ?? card?.kind ?? '';
    const isSelf = kind === 'self';
    const name = principal?.contactName || detail?.name || card?.name || tr('unnamed');
    // The chat finds a person by the name stored on the profile.
    const storedName = principal?.displayName || detail?.name || name;
    const relationship = detail?.relationship || principal?.relationship || card?.relationship || '';
    const role = relationship
        ? relationshipLabel((key) => t(key as never) as unknown as string, (key) => i18n.exists(key), relationship)
        : kind && kind !== 'anonymous'
          ? tr(`kind.${kind}`)
          : '';
    const aliases = (principal?.aliases ?? []).filter((a) => a && a !== name);

    // The header and its More menu come from the directory row, so Edit,
    // link, merge and Delete stay reachable while the detail loads or fails.
    const header = (
        <div className="flex items-center gap-3">
            <Avatar src={principal?.contactPhoto || card?.photo} alt="" fallback={name} className="h-12 w-12 text-[15px]" />
            <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                <h2 className="truncate text-[18px] font-semibold leading-tight text-ink" title={name}>
                    {name}
                </h2>
                {role && <span className="text-[12.5px] text-muted">{role}</span>}
                {aliases.length > 0 && <span className="truncate text-[12px] text-muted">{tr('detail.alsoKnownAs', { names: listWords(i18n.language, aliases) })}</span>}
            </div>
            {principal && <PersonMoreMenu principal={principal} name={name} actions={actions} />}
        </div>
    );

    if (!detail) {
        return (
            <article aria-label={name} className="flex flex-col gap-5">
                {header}
                {notice}
                {failed ? (
                    <PeopleFailureLine text={tr('detail.loadFailed')} onRetry={reread} />
                ) : (
                    <DetailSkeleton label={tr('detail.loading')} />
                )}
            </article>
        );
    }

    const replyOwed = detail.loops.find((loop) => loop.kind === 'reply_owed');
    // Brief me shows only when Isoform has something to say about them.
    const canBrief =
        !isSelf &&
        (detail.loops.length > 0 || detail.together.length > 0 || !!detail.researchSummary || detail.messageCount > 0 || (entry?.stats.totalMessages ?? 0) > 0);
    const summary = detail.researchSummary || (isSelf ? tr('detail.selfSummary') : '');

    return (
        <article aria-label={name} className="flex flex-col gap-5">
            {header}
            {notice}

            {summary && <p className={detail.researchSummary ? 'text-[14px] leading-[1.55] text-ink' : 'text-[13px] leading-[1.55] text-muted'}>{summary}</p>}

            {(canBrief || (!isSelf && replyOwed)) && (
                <div className="flex flex-wrap items-start gap-2">
                    {canBrief && <PersonBriefBlock key={personId} personId={personId} />}
                    {!isSelf && replyOwed && draftFor === null && (
                        <Button variant="secondary" size="sm" onClick={() => setDraftFor(replyOwed.id)}>
                            {tr('detail.draft')}
                        </Button>
                    )}
                </div>
            )}

            {detail.loops.length > 0 && (
                <section className="flex flex-col gap-2">
                    <h3 className="text-[13px] font-semibold text-ink">{tr('detail.openLoops')}</h3>
                    <ul className="flex flex-col gap-2">
                        {detail.loops.map((loop) => (
                            <li key={`${loop.kind}:${loop.id}`} className="flex flex-col text-[14px] text-ink">
                                <span className="flex items-start gap-2.5">
                                    <span aria-hidden className="mt-[7px] size-1.5 shrink-0 rounded-full bg-muted" />
                                    <span className="flex min-w-0 flex-col">
                                        <span>{loopLine(tr, loop)}</span>
                                        {(loop.kind === 'you_owe' || loop.kind === 'waiting_on_them') && (
                                            <span className="text-[12px] text-muted">{loopChip(tr, i18n.language, loop)}</span>
                                        )}
                                    </span>
                                </span>
                                {draftFor === loop.id && loop.kind === 'reply_owed' && (
                                    <PersonReplyDraft personId={personId} momentId={loop.id} onClose={() => setDraftFor(null)} />
                                )}
                            </li>
                        ))}
                    </ul>
                </section>
            )}

            {(detail.togetherFailed || detail.together.length > 0) && (
                <section className="flex flex-col gap-1.5">
                    <h3 className="text-[13px] font-semibold text-ink">{tr('detail.together')}</h3>
                    {detail.togetherFailed && <PeopleFailureLine text={tr('detail.togetherFailed')} onRetry={reread} />}
                    <ul className="flex flex-col">
                        {detail.together.map((item, i) => (
                            <li key={`${item.kind}:${item.at}:${i}`} className="flex gap-3 border-b border-border-divider py-[7px]">
                                <span className="w-16 shrink-0 text-[12px] text-muted">{dayWords(tr, i18n.language, item.at)}</span>
                                <span className="min-w-0 text-[13px] text-ink">{togetherWords(tr, item, name)}</span>
                            </li>
                        ))}
                    </ul>
                </section>
            )}

            {(detail.sharedTopics.length > 0 || detail.seenIn.length > 0) && (
                <section className="flex flex-col gap-1.5">
                    {detail.sharedTopics.length > 0 && (
                        <>
                            <h3 className="text-[13px] font-semibold text-ink">{tr('detail.sharedTopics')}</h3>
                            <ul className="flex flex-wrap gap-1.5">
                                {detail.sharedTopics.map((topic) => (
                                    <li key={topic.id} className="inline-flex h-[26px] items-center rounded-full border border-border bg-panel px-2.5 text-[12.5px] text-ink">
                                        {topic.name}
                                    </li>
                                ))}
                            </ul>
                        </>
                    )}
                    {detail.seenIn.length > 0 && (
                        <span className="text-[12px] text-muted">{tr('detail.seenIn', { apps: listWords(i18n.language, detail.seenIn.map((app) => appLabel(tr, app))) })}</span>
                    )}
                </section>
            )}

            {principal?.notes && (
                <section className="flex flex-col gap-1.5">
                    <h3 className="text-[13px] font-semibold text-ink">{tr('notesHeading')}</h3>
                    <p className="whitespace-pre-wrap text-[13px] leading-relaxed text-ink">{principal.notes}</p>
                </section>
            )}

            <div className="flex flex-col">
                {isSelf && principal && (
                    <DetailFold label={tr('voicePrintHeading')} defaultOpen>
                        <VoiceFingerprint principalId={principal.id} />
                    </DetailFold>
                )}
                {entry && entry.stats.recentMessages.length > 0 && (
                    <DetailFold label={tr('detail.recentMessages')} count={entry.stats.recentMessages.length}>
                        <RecentMessagesList stats={entry.stats} />
                    </DetailFold>
                )}
                {sessionsFailed ? (
                    <DetailFold label={tr('activitySessionsLoadingHeading')}>
                        <PeopleFailureLine text={tr('detail.sessionsFailed')} onRetry={() => setSessionsAttempt((n) => n + 1)} />
                    </DetailFold>
                ) : (
                    sessions &&
                    sessions.length > 0 && (
                        <DetailFold label={tr('activitySessionsLoadingHeading')} count={sessions.length}>
                            <ActivitySessionsList sessions={sessions} principalName={storedName} onOpenActivitySession={onOpenActivitySession} />
                        </DetailFold>
                    )
                )}
                {!isSelf && (
                    <DetailFold label={tr('dossierHeading')}>
                        <PersonDossier principalId={personId} name={name} onChanged={reread} />
                    </DetailFold>
                )}
                {devTools && principal && (
                    <DetailFold label={tr('technical.heading')}>
                        <PersonTechnicalDetails principal={principal} stats={entry?.stats} />
                    </DetailFold>
                )}
            </div>
        </article>
    );
}

function DetailSkeleton({ label }: { label: string }) {
    return (
        <div aria-busy="true" aria-label={label} className="flex flex-col gap-5">
            <div className="flex flex-col gap-2">
                <span className="h-3 w-full animate-pulse rounded bg-surface-active" />
                <span className="h-3 w-11/12 animate-pulse rounded bg-surface-active" />
                <span className="h-3 w-2/3 animate-pulse rounded bg-surface-active" />
            </div>
        </div>
    );
}

/** The person's own actions: edit or name, contact link, merge, delete. */
function PersonMoreMenu({ principal, name, actions }: { principal: PrincipalDTO; name: string; actions: PersonActions }) {
    const { t } = useTranslation('people');
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const isSelf = principal.kind === 'self';
    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <Button variant="outline" size="icon" aria-label={tr('menu.personMore', { name })} className="shrink-0 border-transparent">
                    <MoreHorizontal className="size-4" />
                </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
                {principal.kind === 'anonymous' ? (
                    <DropdownMenuItem onSelect={() => actions.onPromote(principal)}>
                        <UserPlus />
                        {tr('promote')}
                    </DropdownMenuItem>
                ) : (
                    <DropdownMenuItem onSelect={() => actions.onEdit(principal)}>
                        <Pencil />
                        {tr('menu.edit')}
                    </DropdownMenuItem>
                )}
                {principal.contactLinked ? (
                    <DropdownMenuItem onSelect={() => actions.onUnlink(principal)}>
                        <Unlink />
                        {tr('menu.unlinkContact')}
                    </DropdownMenuItem>
                ) : (
                    <DropdownMenuItem onSelect={() => actions.onLink(principal)}>
                        <Link />
                        {tr('contactLinkTitle')}
                    </DropdownMenuItem>
                )}
                {!isSelf && !principal.contactLinked && (
                    <DropdownMenuItem onSelect={() => actions.onMerge(principal)}>
                        <GitMerge />
                        {tr('mergeButtonTitle')}
                    </DropdownMenuItem>
                )}
                {!isSelf && (
                    <>
                        <DropdownMenuSeparator />
                        <DropdownMenuItem onSelect={() => actions.onDelete(principal)}>
                            <Trash2 />
                            {tr('menu.delete')}
                        </DropdownMenuItem>
                    </>
                )}
            </DropdownMenuContent>
        </DropdownMenu>
    );
}
