import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { MergePeople, NotSamePeople, type PeopleOverview, type MergeResult } from '@/lib/wails-shim-app';
import { Button } from '@/components/ui/button';
import { appLabel, type Translate } from './people-format';
import { PeopleFailureLine } from './people-failure';

type Guess = NonNullable<PeopleOverview['selfGuesses']>[number];

export function SelfSenderGuesses({ guesses, onMerged, onNotSame }: {
    guesses: Guess[];
    onMerged: (result: MergeResult, selfId: string) => Promise<void>;
    onNotSame: (ids: string[]) => Promise<void>;
}) {
    const { t } = useTranslation('people');
    const tr: Translate = (key, options) => t(key as never, options as never) as unknown as string;
    const [busy, setBusy] = useState(false);
    const [failed, setFailed] = useState(false);
    if (guesses.length === 0) return null;
    const guess = guesses[0];
    const ids = [guess.selfId, guess.personId];
    const choose = async (yes: boolean) => {
        setBusy(true);
        setFailed(false);
        try {
            if (yes) await onMerged(await MergePeople(guess.selfId, [guess.personId]), guess.selfId);
            else { await NotSamePeople(ids); await onNotSame(ids); }
        } catch { setFailed(true); }
        finally { setBusy(false); }
    };
    return (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-[13px] text-muted">
            <span>{tr('overview.selfGuess', { name: guess.name, app: appLabel(tr, guess.app) })}</span>
            <Button variant="secondary" size="sm" disabled={busy} onClick={() => void choose(true)}>{tr('overview.selfGuessYes')}</Button>
            <Button variant="outline" size="sm" disabled={busy} onClick={() => void choose(false)}>{tr('overview.selfGuessNo')}</Button>
            {failed && <PeopleFailureLine text={tr('overview.selfGuessFailed')} />}
        </div>
    );
}
