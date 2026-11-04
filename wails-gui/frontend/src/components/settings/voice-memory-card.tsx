import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { ForgetVoices } from '@/lib/wails-shim-app';
import { Button } from '@/components/ui/button';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { SettingsCard, SettingsRow } from './settings-primitives';
import { useSettingsFailure } from './settings-failure';

export function VoiceMemoryCard() {
    const { t } = useTranslation('settings');
    const { failure, fail, clear } = useSettingsFailure();
    const [busy, setBusy] = useState(false);
    const [asking, setAsking] = useState(false);
    const forget = async () => {
        clear();
        setBusy(true);
        try { await ForgetVoices(); }
        catch { fail(t('privacy.voices.failed'), () => void forget()); }
        finally { setBusy(false); }
    };
    return <SettingsCard title={t('privacy.voices.title')}>
        <SettingsRow title={t('privacy.voices.remembered')} description={t('privacy.voices.description')} failure={failure}
            control={<Button type="button" variant="outline" size="sm" disabled={busy} onClick={() => setAsking(true)}>{t('privacy.voices.forget')}</Button>} />
        <ConfirmDialog
            open={asking}
            onOpenChange={setAsking}
            title={t('privacy.voices.confirm.title')}
            description={t('privacy.voices.confirm.description')}
            confirmLabel={t('privacy.voices.forget')}
            variant="danger"
            onConfirm={() => void forget()}
        />
    </SettingsCard>;
}
