import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { errorNotice, type ActivityNotice } from '@/components/activity-inline-notice';
import { ExportSOP, ImportSOP } from '@/lib/wails-shim-app';

/** Export and import of a playbook bundle, in place everywhere they are offered (W10):
 *  nothing on a finished export, the imported playbook opens, failures come back
 *  as a copper line with Try again. */
export function usePlaybookTransfer(onNotice?: (notice: ActivityNotice | null) => void, onImported?: (id: string) => void) {
    const { t } = useTranslation('knowledge');

    const exportPlaybook = useCallback(
        (id: string) => {
            ExportSOP(id)
                .then(() => onNotice?.(null))
                .catch(() => onNotice?.(errorNotice(t('playbooks.exportFailed'), () => exportPlaybook(id))));
        },
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [onNotice, t],
    );

    const importPlaybook = useCallback(() => {
        ImportSOP()
            .then((entry) => {
                onNotice?.(null);
                if (entry?.id) onImported?.(entry.id);
            })
            .catch(() => onNotice?.(errorNotice(t('playbooks.importFailed'), () => importPlaybook())));
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, [onImported, onNotice, t]);

    return { exportPlaybook, importPlaybook };
}
