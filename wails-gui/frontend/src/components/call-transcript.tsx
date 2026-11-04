import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { GetMeetingCallTurns, RenameMeetingSpeaker, type MeetingCallTurn, type MeetingCallTurnsView } from '@/lib/wails-shim-app';
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogDescription, DialogCloseButton } from '@/components/ui/dialog';
import { Button } from '@/components/ui/button';

export function CallTranscript({ meetingKey, sessionId }: { meetingKey: string; sessionId: string }) {
    const { t, i18n } = useTranslation('activity');
    const [view, setView] = useState<MeetingCallTurnsView | null>(null);
    const [failed, setFailed] = useState(false);
    const [editing, setEditing] = useState<MeetingCallTurn | null>(null);
    const [name, setName] = useState('');
    const [saving, setSaving] = useState(false);
    const [renameFailed, setRenameFailed] = useState(false);
    const generation = useRef(0);

    useEffect(() => {
        let active = true;
        const load = () => {
            const request = ++generation.current;
            setView(null);
            setFailed(false);
            setEditing(null);
            GetMeetingCallTurns(sessionId).then((result) => {
                if (active && request === generation.current) setView(result);
            }).catch(() => {
                if (active && request === generation.current) setFailed(true);
            });
        };
        load();
        const offSaved = EventsOn('meeting-notes:saved', load);
        const offPrivacy = EventsOn('closed-eyes:changed', load);
        window.addEventListener('focus', load);
        return () => { active = false; ++generation.current; offSaved?.(); offPrivacy?.(); window.removeEventListener('focus', load); };
    }, [sessionId]);

    const label = (turn: MeetingCallTurn) => turn.speakerId === 'you'
        ? t('callTurns.you') : turn.name || t('callTurns.speaker', { number: turn.number });

    const save = async () => {
        if (!editing || !name.trim() || saving) return;
        setSaving(true);
        setRenameFailed(false);
        const request = generation.current;
        try {
            await RenameMeetingSpeaker(meetingKey, editing.speakerId, name.trim());
            if (request === generation.current) {
                const speakerId = editing.speakerId;
                setView((current) => current && ({ ...current, turns: current.turns.map((turn) => turn.speakerId === speakerId ? { ...turn, name: name.trim() } : turn) }));
                setEditing(null);
            }
        } catch {
            if (request === generation.current) setRenameFailed(true);
        } finally { setSaving(false); }
    };

    if (failed) return <p className="text-[11.5px] text-muted">{t('meetingNotes.transcriptUnavailable')}</p>;
    if (!view) return <p className="text-[11.5px] text-muted">{t('meetingNotes.transcriptLoading')}</p>;

    return <>
        {view.status === 'unavailable' && <p className="text-[11px] text-muted">{t('callTurns.limited')}</p>}
        <ol aria-label={t('callTurns.conversation')} className="max-h-96 overflow-y-auto space-y-2 rounded-lg bg-panel p-2">
            {view.turns.map((turn, index) => <li key={`${turn.startTime}-${index}`} className="rounded-lg bg-panel px-3 py-2">
                <div className="flex items-baseline gap-2 text-[11px]">
                    {turn.speakerId === 'you' ? <span className="font-medium text-ink">{label(turn)}</span> :
                        <button type="button" aria-label={t('callTurns.renameLabel', { name: label(turn) })} onClick={() => { setEditing(turn); setName(turn.name); setRenameFailed(false); }} className="rounded-md px-1 -ml-1 font-medium text-ink hover:bg-border/50 underline decoration-border underline-offset-2">{label(turn)}</button>}
                    <time dateTime={new Date(turn.startTime).toISOString()} className="text-muted tabular-nums">{new Date(turn.startTime).toLocaleTimeString(i18n.language, { hour: 'numeric', minute: '2-digit' })}</time>
                </div>
                <p className="mt-1 whitespace-pre-wrap break-words text-[12.5px] leading-relaxed text-ink">{turn.text}</p>
            </li>)}
        </ol>
        <Dialog open={editing !== null} onOpenChange={(open) => { if (!open && !saving) setEditing(null); }}>
            <DialogContent className="w-[400px]">
                <DialogHeader><DialogTitle>{t('callTurns.renameTitle')}</DialogTitle><DialogCloseButton /></DialogHeader>
                <form className="space-y-4 px-6 py-4" onSubmit={(event) => { event.preventDefault(); void save(); }}>
                    <DialogDescription>{t('callTurns.renameDescription')}</DialogDescription>
                    <label className="block text-xs text-ink">{t('callTurns.name')}
                        <input autoFocus maxLength={100} value={name} onChange={(event) => setName(event.target.value)} className="mt-1 w-full rounded-md border border-border bg-panel px-3 py-2 text-sm" />
                    </label>
                    {renameFailed && <p role="alert" className="text-xs text-muted">{t('callTurns.renameFailed')}</p>}
                    <div className="flex justify-end gap-2"><Button type="button" variant="outline" disabled={saving} onClick={() => setEditing(null)}>{t('callTurns.cancel')}</Button><Button type="submit" disabled={saving || !name.trim()}>{t('callTurns.save')}</Button></div>
                </form>
            </DialogContent>
        </Dialog>
    </>;
}
