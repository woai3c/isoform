import { useEffect } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';

import { EventsOn } from '@/lib/wails-shim-runtime';

// Each language gets its own progress toast so concurrent downloads (a mixed
// reply needs both MeloTTS and Kokoro) stack instead of overwriting one shared
// toast and flickering between the two.
const progressToastId = (lang: string) => `tts-model-download-${lang}`;
// Failures collapse onto one shared toast — kept in sync with the stream-card
// speak-button catch so a model error never shows two stacked error toasts.
const ERROR_TOAST_ID = 'tts-model-download';

interface TtsDownloadProgress {
    lang: string;
    downloaded: number;
    total: number;
    bytesPerSec: number;
}

interface TtsModelEvent {
    lang: string;
    source?: string;
}

function formatBytes(bytes: number): string {
    if (!Number.isFinite(bytes) || bytes <= 0) return '0 MB';
    return `${(bytes / 1024 / 1024).toFixed(bytes >= 100 * 1024 * 1024 ? 0 : 1)} MB`;
}

export function TtsDownloadNotifications() {
    const { t } = useTranslation('chat');

    useEffect(() => {
        const languageName = (lang: string) => t(lang === 'en' ? 'ttsDownload.english' : 'ttsDownload.chinese');
        // Languages that actually streamed a download this session. A model
        // already on disk loads into memory without any progress event, so it
        // should not pop a "ready" toast — only confirm what the user watched
        // download.
        const downloadingLangs = new Set<string>();
        const cancelledLangs = new Set<string>();

        const offProgress = EventsOn('tts:download-progress', (progress: TtsDownloadProgress) => {
            if (cancelledLangs.has(progress.lang)) {
                if (progress.downloaded !== 0 || progress.total !== 0) return;
                cancelledLangs.delete(progress.lang);
            }
            downloadingLangs.add(progress.lang);
            const percent = progress.total > 0
                ? Math.min(100, Math.round((progress.downloaded / progress.total) * 100))
                : 0;
            const description = progress.total > 0
                ? t('ttsDownload.progressDetail', {
                    downloaded: formatBytes(progress.downloaded),
                    total: formatBytes(progress.total),
                    speed: formatBytes(progress.bytesPerSec),
                })
                : t('ttsDownload.starting');

            toast.loading(
                percent >= 100
                    ? t('ttsDownload.preparing', { language: languageName(progress.lang) })
                    : t('ttsDownload.downloading', {
                        language: languageName(progress.lang),
                        percent,
                    }),
                {
                    id: progressToastId(progress.lang),
                    description,
                    duration: Infinity,
                },
            );
        });
        const offReady = EventsOn('tts:model-ready', (event: TtsModelEvent) => {
            // Settings observes every installed voice. A background install
            // must not finish an explicit request's engine-loading toast.
            if (event.source === 'background') return;
            // Only confirm readiness for a language the user actually saw
            // download; a silent in-memory load shouldn't surface a toast.
            if (!downloadingLangs.delete(event.lang)) return;
            toast.success(t('ttsDownload.ready', { language: languageName(event.lang) }), {
                id: progressToastId(event.lang),
                duration: 3000,
            });
        });
        const offError = EventsOn('tts:model-error', (event: TtsModelEvent) => {
            // Clear this language's lingering (duration: Infinity) progress toast,
            // then surface a single shared error toast.
            downloadingLangs.delete(event.lang);
            toast.dismiss(progressToastId(event.lang));
            toast.error(t('ttsDownload.failed'), {
                id: ERROR_TOAST_ID,
                description: t('ttsDownload.retry'),
                duration: 8000,
            });
        });
        const offCancelled = EventsOn('tts:model-cancelled', (event: TtsModelEvent) => {
            cancelledLangs.add(event.lang);
            downloadingLangs.delete(event.lang);
            toast.dismiss(progressToastId(event.lang));
        });

        return () => {
            offProgress();
            offReady();
            offError();
            offCancelled();
        };
    }, [t]);

    return null;
}
