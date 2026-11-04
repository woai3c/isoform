import { useEffect, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ActivityInlineNotice, errorNotice, type ActivityNotice } from '@/components/activity-inline-notice';
import { useDevTools } from '@/lib/dev-tools-context';
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
export function PlaybookPageActions({
    authorStatus,
    onOpenPlaybook,
}: {
    authorStatus: SopAuthorStatus | null;
    onOpenPlaybook?: (id: string) => void;
}) {
    const { t } = useTranslation('knowledge');
    const devTools = useDevTools();
    // Generate, Import and their failures answer in a line under the actions (W1, W10).
    const [notice, setNotice] = useState<ActivityNotice | null>(null);
    const { importPlaybook } = usePlaybookTransfer(setNotice, onOpenPlaybook);
    const [createOpen, setCreateOpen] = useState(false);
    const [trajectoryOpen, setTrajectoryOpen] = useState(false);

    // A draft says where it goes, under the actions, from start to end (W01).
    const [phase, setPhase] = useState<'idle' | 'starting' | 'running' | 'done'>('idle');
    const startedAt = useRef(0);
    const lastDays = useRef<number | null>(null);
    const running = Boolean(authorStatus?.running);
    const statusAt = authorStatus?.updatedAt ?? 0;
    const latest = useRef(authorStatus);
    latest.current = authorStatus;
    useEffect(() => {
        if (running) setPhase('running');
        else setPhase((old) => (old === 'running' || (old === 'starting' && statusAt >= startedAt.current) ? 'done' : old));
    }, [running, statusAt]);

    const generate = (days: number) => {
        lastDays.current = days;
        setNotice(null);
        setPhase('idle');
        const at = Date.now();
        startedAt.current = at;
        GenerateSOPFromRange(at - days * DAY_MS, 0)
            .then(() => {
                const status = latest.current;
                const finished = !status?.running && (status?.updatedAt ?? 0) >= at;
                setPhase((old) => (old !== 'idle' ? old : finished ? 'done' : 'starting'));
            })
            .catch(() => setNotice(errorNotice(t('playbooks.generateFailed'), () => generate(days))));
    };

    const days = lastDays.current;
    const draftNotice: ActivityNotice | null =
        phase !== 'done' ? null
        : authorStatus?.lastEvent === 'saved' ? { tone: 'done', text: t('playbooks.drafted') }
        : authorStatus?.lastEvent === 'skipped' ? { tone: 'done', text: t('playbooks.matchesExisting') }
        : authorStatus?.lastEvent === 'failed' && days !== null ? errorNotice(t('playbooks.draftFailed'), () => generate(days))
        : null;

    return (
        <div className="flex flex-col items-end gap-1.5">
        <div className="flex flex-wrap items-center justify-end gap-2">
            {/* The agent log is developer tools only; everyone else sees a line (W18). */}
            {authorStatus?.running && devTools && (
                <Button variant="quiet" size="sm" title={t('playbooks.trajectoryTitle')} onClick={() => setTrajectoryOpen(true)}>
                    <Loader2 className="animate-spin" />
                    {t('playbooks.drafting')}
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
            {devTools && <SopTrajectoryDialog open={trajectoryOpen} onOpenChange={setTrajectoryOpen} authorStatus={authorStatus} />}
        </div>
        {(phase === 'starting' || phase === 'running') && !notice && (
            <p role="status" className="m-0 text-[12.5px] text-muted">{t('playbooks.generateStarted')}</p>
        )}
        <ActivityInlineNotice notice={notice ?? draftNotice} className="text-[12.5px]" />
        </div>
    );
}
