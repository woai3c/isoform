import { useMemo } from 'react';
import { cn } from '@/lib/utils';

export interface TranscriptLine {
    time: string;
    speaker: string;
    text: string;
}

interface TranscriptTurn {
    speaker: string;
    items: { time: string; text: string }[];
}

const TRANSCRIPT_LINE = /^\[(\d{1,2}:\d{2}(?::\d{2})?)\]\s+([^:：]{1,40})[:：]\s*(.*)$/;

/**
 * Parse a `[HH:MM:SS] Speaker: text` transcript body (the shape produced by
 * transcript chat sessions). Returns null unless the content is clearly a
 * transcript — first line must match and at least 3 of the lines / 60%
 * overall — so ordinary assistant markdown never gets hijacked.
 */
export function parseTranscript(content: string): TranscriptLine[] | null {
    const lines = content
        .split('\n')
        .map((line) => line.trim())
        .filter(Boolean);
    if (lines.length < 3) return null;
    const out: TranscriptLine[] = [];
    let matched = 0;
    for (const line of lines) {
        const m = TRANSCRIPT_LINE.exec(line);
        if (m) {
            matched++;
            out.push({ time: m[1]!, speaker: m[2]!.trim(), text: m[3]! });
        } else if (out.length > 0) {
            // Unprefixed line after a matched one: a wrapped continuation of
            // the previous utterance.
            const prev = out[out.length - 1]!;
            prev.text = prev.text ? `${prev.text}\n${line}` : line;
        } else {
            return null;
        }
    }
    if (matched < 3 || matched / lines.length < 0.6) return null;
    return out;
}

/** Speakers the transcription pipeline attributes to the user themself. */
function isSelfSpeaker(speaker: string): boolean {
    return speaker === 'Me' || speaker === '我';
}

/**
 * Structured transcript view: a quiet mono time gutter, speaker turns grouped
 * so consecutive utterances read as one block, and the utterance text in
 * regular body type. Replaces the raw `[time] speaker: text` plaintext wall.
 */
export function TranscriptContent({ lines }: { lines: TranscriptLine[] }) {
    const turns = useMemo(() => {
        const grouped: TranscriptTurn[] = [];
        for (const line of lines) {
            const last = grouped[grouped.length - 1];
            const item = { time: line.time, text: line.text };
            if (last && last.speaker === line.speaker) {
                last.items.push(item);
            } else {
                grouped.push({ speaker: line.speaker, items: [item] });
            }
        }
        return grouped;
    }, [lines]);

    return (
        <div className="flex flex-col select-text">
            {turns.map((turn, i) => (
                <div key={i} className="flex gap-3 mt-4 first:mt-0">
                    <span className="w-14 shrink-0 pt-[1px] text-right font-mono text-[10px] leading-4 tabular-nums text-muted select-none">
                        {turn.items[0]!.time}
                    </span>
                    <div className="flex-1 min-w-0">
                        <div
                            className={cn(
                                'text-xs font-medium leading-4 mb-1',
                                isSelfSpeaker(turn.speaker)
                                    ? 'text-ink'
                                    : 'text-muted',
                            )}
                        >
                            {turn.speaker}
                        </div>
                        {turn.items.map((item, j) => (
                            <p
                                key={j}
                                title={item.time}
                                className={cn(
                                    'm-0 text-sm leading-relaxed text-ink whitespace-pre-wrap',
                                    j > 0 && 'mt-1.5',
                                )}
                            >
                                {item.text}
                            </p>
                        ))}
                    </div>
                </div>
            ))}
        </div>
    );
}
