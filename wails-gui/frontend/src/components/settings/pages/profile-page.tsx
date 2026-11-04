import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { EnsureSelfProfile } from '@/lib/wails-shim-app';
import { refreshUserProfile, useUserProfile } from '@/hooks/useUserProfile';
import { Button } from '@/components/ui/button';
import { SettingsCard } from '../settings-primitives';

/** The account-menu header opens the same local self identity used by People. */
export function ProfilePage() {
    const { t } = useTranslation('app');
    const { profile, loading } = useUserProfile();
    const [draft, setDraft] = useState<string | null>(null);
    const [saving, setSaving] = useState(false);
    const [failed, setFailed] = useState(false);
    const current = profile?.display_name || profile?.name || '';
    const name = draft ?? current;
    const save = async () => {
        if (!name.trim() || saving) return;
        setSaving(true);
        setFailed(false);
        try {
            await EnsureSelfProfile(name.trim(), []);
            refreshUserProfile();
            setDraft(null);
        } catch { setFailed(true); }
        finally { setSaving(false); }
    };
    const changed = name.trim() !== current;
    // settings-review ST71: no card title under the Profile page; Save shows once the name changes.
    return <SettingsCard>
        <form className="flex flex-col gap-3" onSubmit={(event) => { event.preventDefault(); void save(); }}>
            <label htmlFor="profile-name" className="text-[13px] text-ink">{t('userMenu.nameLabel')}</label>
            <input id="profile-name" className="h-8 rounded-lg border border-border bg-panel px-3 text-[13px] text-ink focus-visible:outline-border-focus" value={name} disabled={loading || saving} maxLength={80} onChange={(event) => setDraft(event.target.value)} placeholder={t('userMenu.namePlaceholder')} />
            {failed && <p role="alert" className="text-[12px] text-copper">{t('userMenu.nameSaveError')}</p>}
            {changed && <div className="flex justify-end gap-2">
                <Button type="button" variant="quiet" size="sm" disabled={saving} onClick={() => { setDraft(null); setFailed(false); }}>{t('userMenu.cancelName')}</Button>
                <Button type="submit" size="sm" disabled={loading || saving || !name.trim()}>{t('userMenu.saveName')}</Button>
            </div>}
        </form>
    </SettingsCard>;
}
