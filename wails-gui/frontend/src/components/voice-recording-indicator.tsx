import { useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Mic } from 'lucide-react';

interface VoiceRecordingIndicatorProps {
    /** 0..1 mic input loudness for the waveform animation. */
    audioLevel: number;
    /** Click → stop + transcribe. Wired to useVoiceInput.stopAndTranscribe. */
    onClick: () => void;
}

/**
 * Pill-shaped "recording in progress" indicator that replaces the mic
 * button while voice capture is active. Pulsing red dot + mm:ss timer
 * + 7-bar waveform that reacts to audioLevel + mic glyph. Tapping
 * anywhere on the pill stops recording and triggers transcription.
 *
 * Self-contained: owns its own elapsed-seconds counter so callers only
 * need to mount/unmount it on voiceState === "recording" without
 * threading the timer through. Used by both the main UI composer and
 * the spotlight pill / conversation panel so the recording UX is
 * identical across surfaces.
 */
export function VoiceRecordingIndicator({
    audioLevel,
    onClick,
}: VoiceRecordingIndicatorProps) {
    const { t } = useTranslation('chat');
    const [elapsed, setElapsed] = useState(0);
    useEffect(() => {
        const interval = setInterval(() => setElapsed((prev) => prev + 1), 1000);
        return () => clearInterval(interval);
    }, []);

    const formatted = formatElapsed(elapsed);

    return (
        <button
            type="button"
            className="flex items-center gap-2 h-8 pl-3 pr-3 rounded-full bg-error/10 border border-error/30 cursor-pointer hover:bg-error/15 transition-colors"
            onClick={onClick}
            title={t('quickChat.stopRecording')}
        >
            <span className="relative flex size-2 shrink-0">
                <span className="absolute inline-flex size-full rounded-full bg-error opacity-75 animate-ping" />
                <span className="relative inline-flex size-2 rounded-full bg-error" />
            </span>
            <span className="text-xs font-medium text-error tabular-nums">
                {formatted}
            </span>
            <div className="flex items-center gap-[2px] h-5">
                {Array.from({ length: 7 }).map((_, i) => {
                    const symmetry = 1 - Math.abs(i - 3) / 3;
                    const barHeight =
                        audioLevel > 0.005 ? 4 + symmetry * audioLevel * 18 : 3;
                    return (
                        <span
                            key={i}
                            className="w-[3px] rounded-full bg-error/80"
                            style={{
                                height: `${barHeight}px`,
                                transition: 'height 100ms ease-out',
                            }}
                        />
                    );
                })}
            </div>
            <Mic className="size-3.5 text-error" />
        </button>
    );
}

function formatElapsed(seconds: number): string {
    const m = Math.floor(seconds / 60);
    const s = seconds % 60;
    return `${m}:${s.toString().padStart(2, '0')}`;
}
