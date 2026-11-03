import { useCallback } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { ExportSOP, ImportSOP } from '@/lib/wails-shim-app';

/** Export and import of a playbook bundle, with the same toasts everywhere they are offered. */
export function usePlaybookTransfer() {
    const { t } = useTranslation('knowledge');

    const exportPlaybook = useCallback(
        (id: string) => {
            ExportSOP(id)
                .then((path) => {
                    if (!path) return; // dialog canceled
                    toast.success(t('playbooks.exportDone', { path }), { duration: 5000 });
                })
                .catch((err) => {
                    toast.error(t('playbooks.exportFailed', { error: String(err) }), { duration: 4000 });
                });
        },
        [t],
    );

    const importPlaybook = useCallback(() => {
        ImportSOP()
            .then((entry) => {
                if (!entry?.id) return; // dialog canceled
                toast.success(t('playbooks.importDone', { title: entry.title }), { duration: 5000 });
            })
            .catch((err) => {
                toast.error(t('playbooks.importFailed', { error: String(err) }), { duration: 4000 });
            });
    }, [t]);

    return { exportPlaybook, importPlaybook };
}
