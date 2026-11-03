import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button } from '@/components/ui/button';
import { AnswerFirstRead } from '@/lib/wails-shim-app';
import type { MainChatProactivePayload, MainChatReportTarget } from '@/lib/stream-helpers';

export function FirstReadCard({ message, onOpenTarget }: { message: MainChatProactivePayload; onOpenTarget?: (target: MainChatReportTarget) => void }) {
    const { t, i18n } = useTranslation('chat');
    const [confirmed, setConfirmed] = useState(message.state === 'acted');
    const [confirmedAt, setConfirmedAt] = useState(message.firstReadConfirmedAt ?? 0);
    const [undoVisible, setUndoVisible] = useState(false);
    const [correcting, setCorrecting] = useState(false);
    const [text, setText] = useState('');
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    const [corrected, setCorrected] = useState(false);
    const undoRequested = useRef(false);
    const language = i18n.resolvedLanguage?.startsWith('zh') ? 'zh-CN' : 'en';
    const helps = message.localized?.[language]?.helps ?? message.helps ?? [];

    useEffect(() => {
        setConfirmed(message.state === 'acted');
        setConfirmedAt(message.firstReadConfirmedAt ?? 0);
    }, [message.momentId, message.state, message.firstReadConfirmedAt]);
    useEffect(() => {
        const remaining = confirmedAt + 5000 - Date.now();
        setUndoVisible(confirmed && remaining > 0);
        if (!confirmed || remaining <= 0) return;
        const timer = window.setTimeout(() => setUndoVisible(false), remaining);
        return () => window.clearTimeout(timer);
    }, [confirmed, confirmedAt]);

    async function confirm() {
        setBusy(true); setFailed(false); undoRequested.current = false;
        setConfirmed(true); setConfirmedAt(Date.now());
        let recordedAt = 0;
        try {
            const result = await AnswerFirstRead(message.momentId, 'confirm_portrait', '');
            recordedAt = result.confirmedAt ?? Date.now();
            if (undoRequested.current) {
                await AnswerFirstRead(message.momentId, 'undo_portrait', '');
                setConfirmed(false); setConfirmedAt(0);
            } else setConfirmedAt(recordedAt);
        } catch {
            setConfirmed(recordedAt > 0); setConfirmedAt(recordedAt); setFailed(true);
        } finally { setBusy(false); }
    }
    async function undo() {
        if (busy) { undoRequested.current = true; setConfirmed(false); return; }
        setBusy(true); setFailed(false);
        try {
            await AnswerFirstRead(message.momentId, 'undo_portrait', '');
            setConfirmed(false); setConfirmedAt(0);
        } catch { setFailed(true); }
        finally { setBusy(false); }
    }
    async function correct() {
        const correction = text.trim();
        if (!correction) return;
        setBusy(true); setFailed(false);
        try {
            await AnswerFirstRead(message.momentId, 'correct_portrait', correction);
            setCorrected(true);
        } catch { setFailed(true); }
        finally { setBusy(false); }
    }
    if (!message.sentences?.length || corrected) return null;
    return (
        <div className="flex flex-col gap-3" data-testid="first-read">
            <section className="rounded-xl border border-border bg-panel p-4">
                <p className="text-xs font-semibold uppercase text-muted">{t('firstRead.label')}</p>
                <p className="mt-2 text-[15px] leading-relaxed text-ink">{message.sentences.join(' ')}</p>
                {confirmed ? <div className="mt-3">
                    <p className="text-sm font-medium text-accent">{t('firstRead.confirmed')}{undoVisible && <> · <button type="button" onClick={() => void undo()}>{t('firstRead.undo')}</button></>}</p>
                    <p className="mt-1 text-sm text-muted">{t('firstRead.portrait')}</p>
                </div> : correcting ? <div className="mt-3 flex flex-col items-start gap-2">
                    <textarea className="w-full rounded-lg border border-border bg-panel p-2 text-sm text-ink" placeholder={t('firstRead.off')} aria-label={t('firstRead.off')} maxLength={500} value={text} onChange={(event) => setText(event.target.value)} />
                    <Button size="sm" disabled={busy || !text.trim()} onClick={() => void correct()}>{t('firstRead.send')}</Button>
                </div> : <div className="mt-3 flex gap-2">
                    <Button size="sm" variant="secondary" disabled={busy} onClick={() => void confirm()}>{t('firstRead.yes')}</Button>
                    <Button size="sm" variant="secondary" disabled={busy} onClick={() => setCorrecting(true)}>{t('firstRead.no')}</Button>
                </div>}
                {failed && <p role="alert" className="mt-2 text-sm text-error">{t('firstRead.failed')}</p>}
            </section>
            {helps.length >= 2 && <section className="rounded-xl border border-border bg-panel p-4"><p className="text-xs font-semibold uppercase text-muted">{t('firstRead.helps')}</p><ul className="mt-2 space-y-2 text-sm text-ink">{helps.map((line, index) => <li key={index}>{line}</li>)}</ul></section>}
            {(message.offerCount ?? 0) > 0 && <section className="rounded-xl border border-border bg-panel p-4"><p className="text-xs font-semibold uppercase text-muted">{t('firstRead.upNext')}</p><p className="mt-2 font-semibold text-ink">{message.offerCount === 1 ? t('firstRead.offerOne') : t('firstRead.offers', { count: message.offerCount })}</p><p className="mt-2 text-sm text-muted">{t('firstRead.offerDetail')}</p><Button className="mt-3" size="sm" onClick={() => onOpenTarget?.({ kind: 'tasks' })}>{t('firstRead.see')}</Button></section>}
        </div>
    );
}
