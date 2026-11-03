import { useEffect, useState, type ReactNode } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown, ChevronUp, FileText, FolderOpen, Trash2 } from 'lucide-react';
import { toast } from 'sonner';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import {
    DeleteMeetingNotes,
    GetMeetingNotesForSessions,
    RevealMeetingRecording,
    type MeetingNotesFollowup,
    type MeetingNotesPart,
    type MeetingNotesView,
} from '@/lib/wails-shim-app';
import { hostBridgeMediaURL } from '@/lib/host-bridge-media';
import { getFileManagerName } from '@/lib/platform';
import { MarkdownContent } from '@/components/markdown-content';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { CallTranscript } from '@/components/call-transcript';

// A meeting's notes on Activity: what the after-call pass wrote (summary,
// decisions, follow-ups), what the user wrote, and each part of the call
// with its recording and full transcript. The backend leaves out any
// meeting a privacy setting hides now, and refuses its transcript and
// recording; the card reloads when observation is paused or resumed and
// when the window comes back into focus.

const sectionHeadingClass = 'text-[11px] font-semibold uppercase tracking-[0.08em] text-muted';

function formatClock(ms: number, lang: string): string {
    return new Date(ms).toLocaleTimeString(lang, { hour: 'numeric', minute: '2-digit' });
}

/** The transcript body without the metadata header the backend writes. */
export function transcriptBody(markdown: string): string {
    const marker = '## Transcript';
    const at = markdown.indexOf(marker);
    return (at >= 0 ? markdown.slice(at + marker.length) : markdown).trim();
}

export function ActivityMeetingNotes({ sessionIds, bridgePort }: { sessionIds: string[]; bridgePort: number }) {
    const { t } = useTranslation('activity');
    const [notes, setNotes] = useState<MeetingNotesView[]>([]);
    const idsKey = sessionIds.join('\n');

    useEffect(() => {
        let cancelled = false;
        const ids = idsKey ? idsKey.split('\n') : [];
        const load = () => {
            if (ids.length === 0) {
                setNotes([]);
                return;
            }
            GetMeetingNotesForSessions(ids)
                .then((result) => {
                    if (!cancelled) setNotes(result ?? []);
                })
                .catch(() => {
                    if (!cancelled) setNotes([]);
                });
        };
        load();
        const offSaved = EventsOn('meeting-notes:saved', load);
        const offClosedEyes = EventsOn('closed-eyes:changed', load);
        window.addEventListener('focus', load);
        return () => {
            cancelled = true;
            offSaved?.();
            offClosedEyes?.();
            window.removeEventListener('focus', load);
        };
    }, [idsKey]);

    if (notes.length === 0) return null;

    return (
        <div className="space-y-3 shrink-0">
            <h2 className={sectionHeadingClass}>{t('meetingNotes.section')}</h2>
            {notes.map((meeting) => (
                <MeetingNotesCard
                    key={meeting.meetingKey}
                    meeting={meeting}
                    bridgePort={bridgePort}
                    onDeleted={() => setNotes((current) => current.filter((n) => n.meetingKey !== meeting.meetingKey))}
                />
            ))}
        </div>
    );
}

/** One meeting's notes card. Exported for the Activity Day view, which
 *  renders it inside the meeting row's accordion. */
export function MeetingNotesCard({
    meeting,
    bridgePort,
    onDeleted,
}: {
    meeting: MeetingNotesView;
    bridgePort: number;
    onDeleted: () => void;
}) {
    const { t, i18n } = useTranslation('activity');
    const [open, setOpen] = useState(true);
    const [confirmDelete, setConfirmDelete] = useState(false);
    const title =
        meeting.title ||
        (meeting.meetingApp ? t('meetingNotes.callTitle', { app: meeting.meetingApp }) : t('meetingNotes.untitledCall'));
    const minutes = Math.max(1, Math.round((meeting.endTime - meeting.startTime) / 60000));
    const words = meeting.parts.reduce((sum, part) => sum + part.wordCount, 0);
    const time = t('meetingNotes.timeRange', {
        start: formatClock(meeting.startTime, i18n.language),
        end: formatClock(meeting.endTime, i18n.language),
    });
    const duration = t('meetingNotes.duration', { minutes });
    const meta =
        words > 0
            ? t('meetingNotes.metaWithWords', { time, duration, words: t('meetingNotes.words', { count: words }) })
            : t('meetingNotes.meta', { time, duration });
    const deleteNotes = () => {
        DeleteMeetingNotes(meeting.meetingKey)
            .then(onDeleted)
            .catch(() => toast.error(t('meetingNotes.deleteFailed')));
    };
    const hasOutline = meeting.summary !== '' || meeting.decisions.length > 0 || meeting.followups.length > 0;
    // Activity sessions a recording only spans have nothing to open.
    const parts = meeting.parts.filter((part) => part.hasRecording || part.hasTranscript);

    return (
        <section className="rounded-lg border border-border bg-panel">
            <button
                type="button"
                onClick={() => setOpen((o) => !o)}
                aria-expanded={open}
                aria-label={(open ? t('meetingNotes.collapse') : t('meetingNotes.expand')) as string}
                className="w-full flex items-start gap-3 px-4 py-3 text-left rounded-lg hover:bg-ink/[0.02] transition-colors"
            >
                <div className="flex-1 min-w-0">
                    <h3 className="text-[13px] font-medium text-ink truncate">{title}</h3>
                    <p className="mt-0.5 text-[11px] text-muted tabular-nums">{meta}</p>
                </div>
                {open ? (
                    <ChevronUp className="h-3.5 w-3.5 mt-1 shrink-0 text-muted" />
                ) : (
                    <ChevronDown className="h-3.5 w-3.5 mt-1 shrink-0 text-muted" />
                )}
            </button>
            {open && (
                <div className="px-4 pb-4 space-y-4 select-text">
                    {hasOutline ? (
                        <>
                            {meeting.summary && (
                                <NotesBlock title={t('meetingNotes.summary')}>
                                    <p className="text-[12.5px] leading-relaxed text-ink whitespace-pre-line">{meeting.summary}</p>
                                </NotesBlock>
                            )}
                            {meeting.decisions.length > 0 && (
                                <NotesBlock title={t('meetingNotes.decisions')}>
                                    <ul className="list-disc pl-4 space-y-1 text-[12.5px] leading-relaxed text-ink marker:text-muted">
                                        {meeting.decisions.map((decision) => (
                                            <li key={decision}>{decision}</li>
                                        ))}
                                    </ul>
                                </NotesBlock>
                            )}
                            {meeting.followups.length > 0 && (
                                <NotesBlock title={t('meetingNotes.followups')}>
                                    <ul className="space-y-1.5">
                                        {meeting.followups.map((followup) => (
                                            <FollowupRow key={followup.text} followup={followup} />
                                        ))}
                                    </ul>
                                </NotesBlock>
                            )}
                        </>
                    ) : (
                        <p className="text-[12px] text-muted">{t('meetingNotes.noOutline')}</p>
                    )}
                    {meeting.userNotes && (
                        <NotesBlock title={t('meetingNotes.yourNotes')}>
                            <div className="text-[12.5px] text-ink">
                                <MarkdownContent content={meeting.userNotes} />
                            </div>
                        </NotesBlock>
                    )}
                    {parts.length > 0 && (
                        <div className="space-y-3 border-t border-border/60 pt-3">
                            {parts.map((part, index) => (
                                <MeetingPartRow
                                    key={part.sessionId}
                                    part={part}
                                    index={index}
                                    total={parts.length}
                                    bridgePort={bridgePort}
                                    meetingKey={meeting.meetingKey}
                                />
                            ))}
                        </div>
                    )}
                    <div className="flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
                        <p className="text-[10.5px] text-muted/80">{t('meetingNotes.learnedNote')}</p>
                        <button
                            type="button"
                            onClick={() => setConfirmDelete(true)}
                            className="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] text-muted hover:text-ink hover:bg-border/40 transition-colors"
                        >
                            <Trash2 className="h-3 w-3" />
                            {t('meetingNotes.delete')}
                        </button>
                    </div>
                </div>
            )}
            <ConfirmDialog
                open={confirmDelete}
                onOpenChange={setConfirmDelete}
                title={t('meetingNotes.deleteTitle')}
                description={t('meetingNotes.deleteDescription')}
                confirmLabel={t('meetingNotes.deleteConfirm')}
                variant="danger"
                onConfirm={deleteNotes}
            />
        </section>
    );
}

function NotesBlock({ title, children }: { title: string; children: ReactNode }) {
    return (
        <div className="space-y-1.5">
            <h4 className="text-[11px] font-medium text-muted">{title}</h4>
            {children}
        </div>
    );
}

function FollowupRow({ followup }: { followup: MeetingNotesFollowup }) {
    const { t } = useTranslation('activity');
    const owner =
        followup.owner === 'me' ? t('meetingNotes.ownerMe') : followup.person || t('meetingNotes.ownerSomeone');
    return (
        <li className="flex items-start gap-2 text-[12.5px] leading-relaxed text-ink">
            <span className="mt-[2px] shrink-0 rounded-full bg-panel-warm px-1.5 py-0.5 text-[10.5px] leading-none text-muted dark:bg-panel dark:text-muted">
                {owner}
            </span>
            <span className="min-w-0">
                {followup.text}
                {followup.due && <span className="text-muted"> {t('meetingNotes.dueSuffix', { due: followup.due })}</span>}
            </span>
        </li>
    );
}

function MeetingPartRow({
    part,
    index,
    total,
    bridgePort,
    meetingKey,
}: {
    part: MeetingNotesPart;
    index: number;
    total: number;
    bridgePort: number;
    meetingKey: string;
}) {
    const { t, i18n } = useTranslation('activity');
    const [transcriptOpen, setTranscriptOpen] = useState(false);
    const [audioFailed, setAudioFailed] = useState(false);

    const toggleTranscript = () => {
        setTranscriptOpen((o) => !o);
    };
    const reveal = () => {
        RevealMeetingRecording(part.sessionId).catch(() => toast.error(t('meetingNotes.revealFailed')));
    };
    const canPlay = part.hasRecording && bridgePort > 0 && !audioFailed;

    return (
        <div className="space-y-2">
            {total > 1 && (
                <p className="text-[11px] font-medium text-muted">
                    {t('meetingNotes.part', { index: index + 1, time: formatClock(part.startTime, i18n.language) })}
                </p>
            )}
            {canPlay && (
                <audio
                    controls
                    preload="none"
                    src={hostBridgeMediaURL(bridgePort, 'audio', { session: part.sessionId })}
                    onError={() => setAudioFailed(true)}
                    aria-label={t('meetingNotes.recordingLabel') as string}
                    className="w-full h-8"
                />
            )}
            {part.hasRecording && !canPlay && (
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11.5px] text-muted">
                    <span>{t('meetingNotes.recordingUnavailable')}</span>
                    <button
                        type="button"
                        onClick={reveal}
                        className="inline-flex items-center gap-1 rounded-md px-1.5 py-0.5 text-ink/80 hover:text-ink hover:bg-surface-hover transition-colors"
                    >
                        <FolderOpen className="h-3 w-3" />
                        {t('meetingNotes.revealRecording', { fileManager: getFileManagerName(i18n.language) })}
                    </button>
                </div>
            )}
            {part.hasTranscript && (
                <button
                    type="button"
                    onClick={toggleTranscript}
                    aria-expanded={transcriptOpen}
                    className="inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-[11.5px] text-ink/80 hover:text-ink hover:bg-surface-hover transition-colors"
                >
                    <FileText className="h-3 w-3" />
                    {transcriptOpen ? t('meetingNotes.hideTranscript') : t('meetingNotes.showTranscript')}
                </button>
            )}
            {transcriptOpen && <CallTranscript meetingKey={meetingKey} sessionId={part.sessionId} />}
        </div>
    );
}
