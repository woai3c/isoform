import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { ChevronDown, FlaskConical, Loader2, Plus, Upload } from 'lucide-react';
import { GenerateSOPFromRange, type SopAuthorStatus } from '@/lib/wails-shim-app';
import { Button } from '@/components/ui/button';
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import { CreateSopDialog } from '@/components/sops/CreateSopDialog';
import { SopTrajectoryDialog } from '@/components/sops/SopTrajectoryDialog';
import { usePlaybookTransfer } from '@/components/sops/playbook-io';

const DAY_MS = 24 * 60 * 60 * 1000;

/**
 * The playbook actions that stay on How you do things whether or not any
 * playbook is confirmed: create, import, make one from recent work, and the
 * progress of a playbook being drafted now.
 */
export function PlaybookPageActions({ authorStatus }: { authorStatus: SopAuthorStatus | null }) {
    const { t } = useTranslation('knowledge');
    const { importPlaybook } = usePlaybookTransfer();
    const [createOpen, setCreateOpen] = useState(false);
    const [trajectoryOpen, setTrajectoryOpen] = useState(false);

    const generate = (days: number) => {
        GenerateSOPFromRange(Date.now() - days * DAY_MS, 0)
            .then(() => {
                toast.success(t('playbooks.generateStarted'), { duration: 5000 });
            })
            .catch((err) => {
                toast.error(t('playbooks.generateFailed', { error: String(err) }), { duration: 4000 });
            });
    };

    return (
        <div className="flex flex-wrap items-center justify-end gap-2">
            {authorStatus?.running && (
                <Button variant="quiet" size="sm" title={t('playbooks.trajectoryTitle')} onClick={() => setTrajectoryOpen(true)}>
                    <Loader2 className="animate-spin" />
                    {t('playbooks.authoring')}
                </Button>
            )}
            <Button variant="outline" size="sm" onClick={() => importPlaybook()}>
                <Upload />
                {t('playbooks.import')}
            </Button>
            <DropdownMenu>
                <DropdownMenuTrigger asChild>
                    <Button variant="outline" size="sm">
                        <FlaskConical />
                        {t('playbooks.generate')}
                        <ChevronDown />
                    </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="end" className="min-w-[170px]">
                    {(
                        [
                            [1, t('playbooks.generateLast1')],
                            [3, t('playbooks.generateLast3')],
                            [7, t('playbooks.generateLast7')],
                        ] as const
                    ).map(([days, label]) => (
                        <DropdownMenuItem key={days} onSelect={() => generate(days)}>
                            {label}
                        </DropdownMenuItem>
                    ))}
                </DropdownMenuContent>
            </DropdownMenu>
            <Button size="sm" onClick={() => setCreateOpen(true)}>
                <Plus />
                {t('playbooks.create')}
            </Button>
            <CreateSopDialog open={createOpen} onOpenChange={setCreateOpen} authorStatus={authorStatus} />
            <SopTrajectoryDialog open={trajectoryOpen} onOpenChange={setTrajectoryOpen} authorStatus={authorStatus} />
        </div>
    );
}
