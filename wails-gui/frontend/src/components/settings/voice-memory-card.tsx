import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { toast } from 'sonner';
import { ForgetVoices } from '@/lib/wails-shim-app';
import { Button } from '@/components/ui/button';
import { SettingsCard, SettingsRow } from './settings-primitives';

export function VoiceMemoryCard() {
    const { t } = useTranslation('settings');
    const [busy, setBusy] = useState(false);
    const forget = async () => {
        setBusy(true);
        try { await ForgetVoices(); toast.success(t('privacy.voices.forgotten')); }
        catch { toast.error(t('privacy.voices.failed')); }
        finally { setBusy(false); }
    };
    return <SettingsCard title={t('privacy.voices.title')}>
        <SettingsRow title={t('privacy.voices.remembered')} description={t('privacy.voices.description')}
            control={<Button type="button" variant="outline" size="sm" disabled={busy} onClick={() => void forget()}>{t('privacy.voices.forget')}</Button>} />
    </SettingsCard>;
}
