import { useTranslation } from 'react-i18next';
import type { TFunction } from 'i18next';
import type { CronRunState } from '@/hooks/useCronJobs';

export function runBackendName(backend: string, t: TFunction<'app'>): string | undefined {
    switch (backend) {
        case 'claude-code':
            return t('automations.runBackend.claudeCode');
        case 'codex':
            return t('automations.runBackend.codex');
        case 'native':
            return t('automations.runBackend.native');
        default:
            return undefined;
    }
}

/**
 * Which agent ran a scheduled run, in its history. Chat shows no attribution;
 * why a pick was skipped and Try again live in Settings > Assistant.
 */
export function RunBackendLabel({ run, className }: {
    run: Pick<CronRunState, 'backend' | 'status'>;
    className?: string;
}) {
    const { t } = useTranslation('app');
    const backend = runBackendName(run.backend ?? '', t);
    // Old runs have no route record. Never guess which assistant ran them.
    if (!backend) return null;
    let text: string;
    switch (run.status) {
        case 'running':
            text = t('automations.runBackend.runningOn', { backend });
            break;
        case 'blocked':
            text = t('automations.runBackend.ranOnBlocked', { backend });
            break;
        default:
            text = t('automations.runBackend.ranOn', { backend });
    }
    return <p className={className}>{text}</p>;
}
