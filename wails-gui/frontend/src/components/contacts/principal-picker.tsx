import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Search, User as UserIcon, UserCircle, HelpCircle, Plus } from 'lucide-react';
import {
    ListPrincipals,
    PrincipalDTO,
} from '../../lib/wails-shim-app';
import { cn } from '../../lib/utils';
import { ContactEditorDialog } from './contact-editor';

interface PrincipalPickerProps {
    selectedID?: string | null;
    onSelect: (principal: PrincipalDTO) => void;
    excludeKinds?: PrincipalDTO['kind'][];
    allowCreate?: boolean;
    autoFocusSearch?: boolean;
}

// PrincipalPicker renders a searchable list of every known principal and
// invokes onSelect when one is picked. Used from the Memory "wrong subject"
// dialog (to redirect a misattributed memory) and anywhere else that needs
// a "who is this really about?" affordance.
//
// When allowCreate is true a "+ New contact" affordance is shown that opens
// the shared ContactEditorDialog in create mode; the newly created contact
// is auto-selected on save so the user can finish their primary task
// without bouncing through a second dialog.
export function PrincipalPicker({
    selectedID,
    onSelect,
    excludeKinds,
    allowCreate = true,
    autoFocusSearch = true,
}: PrincipalPickerProps) {
    const { t } = useTranslation('people');
    const [principals, setPrincipals] = useState<PrincipalDTO[]>([]);
    const [keyword, setKeyword] = useState('');
    const [loading, setLoading] = useState(true);
    const [createOpen, setCreateOpen] = useState(false);

    const reload = useCallback(async () => {
        setLoading(true);
        try {
            const rows = await ListPrincipals();
            setPrincipals(rows ?? []);
        } catch (err) {
            console.warn('[principal-picker] list failed:', err);
        } finally {
            setLoading(false);
        }
    }, []);

    useEffect(() => {
        reload();
    }, [reload]);

    const filtered = useMemo(() => {
        const excluded = new Set(excludeKinds ?? []);
        const q = keyword.trim().toLowerCase();
        const matchesKeyword = (p: PrincipalDTO) => {
            if (!q) return true;
            if (p.displayName.toLowerCase().includes(q)) return true;
            if (p.relationship.toLowerCase().includes(q)) return true;
            for (const a of p.aliases ?? []) if (a.toLowerCase().includes(q)) return true;
            for (const h of p.appHandles ?? []) {
                if (h.app.toLowerCase().includes(q)) return true;
                if (h.displayHandle.toLowerCase().includes(q)) return true;
                if (h.handleId.toLowerCase().includes(q)) return true;
            }
            return false;
        };
        const result = principals.filter((p) => !excluded.has(p.kind) && matchesKeyword(p));
        // Sort: self first; then contacts alphabetical; anonymous last.
        result.sort((a, b) => {
            const order = (kind: string) =>
                kind === 'self' ? 0 : kind === 'contact' ? 1 : 2;
            const d = order(a.kind) - order(b.kind);
            if (d !== 0) return d;
            return a.displayName.localeCompare(b.displayName);
        });
        return result;
    }, [principals, keyword, excludeKinds]);

    return (
        <div className="flex flex-col gap-2">
            <div className="relative">
                <Search className="absolute left-2.5 top-1/2 -translate-y-1/2 h-3.5 w-3.5 text-muted pointer-events-none" />
                <input
                    type="text"
                    value={keyword}
                    onChange={(e) => setKeyword(e.target.value)}
                    placeholder={t('pickerSearchPlaceholder') as string}
                    autoFocus={autoFocusSearch}
                    className="w-full h-8 pl-8 pr-3 rounded-md border border-border bg-panel text-[13px] outline-none focus-visible:border-accent-2/60 focus-visible:ring-2 focus-visible:ring-accent-2/30"
                />
            </div>
            <div className="max-h-[240px] overflow-y-auto flex flex-col gap-0.5 rounded-md border border-border bg-panel/30 p-1">
                {loading ? (
                    <p className="px-3 py-2 text-[12px] text-muted">{t('loading')}</p>
                ) : filtered.length === 0 ? (
                    <p className="px-3 py-2 text-[12px] text-muted">{t('pickerNoResults')}</p>
                ) : (
                    filtered.map((p) => {
                        const Icon =
                            p.kind === 'self'
                                ? UserIcon
                                : p.kind === 'contact'
                                  ? UserCircle
                                  : HelpCircle;
                        return (
                            <button
                                key={p.id}
                                type="button"
                                onClick={() => onSelect(p)}
                                data-active={p.id === selectedID}
                                className={cn(
                                    'flex items-center gap-2 px-2 py-1.5 rounded-md text-left transition-colors text-[13px]',
                                    p.id === selectedID
                                        ? 'bg-active-row-bg text-ink'
                                        : 'text-ink hover:bg-ink/[0.05]',
                                )}
                            >
                                <Icon className="h-4 w-4 shrink-0 text-muted" />
                                <span className="flex-1 truncate font-medium">
                                    {p.displayName || t('unnamed')}
                                </span>
                                {p.relationship && (
                                    <span className="text-[11px] text-muted">
                                        {t(`relationship.${p.relationship}` as never, p.relationship) as string}
                                    </span>
                                )}
                                <span className="text-[10px] uppercase tracking-wide text-muted/70">
                                    {t(`kind.${p.kind}` as never, p.kind) as string}
                                </span>
                            </button>
                        );
                    })
                )}
            </div>
            {allowCreate && (
                <button
                    type="button"
                    onClick={() => setCreateOpen(true)}
                    className="inline-flex items-center gap-1.5 self-start h-7 px-2.5 rounded-md text-[12px] text-muted hover:text-ink hover:bg-ink/[0.05] transition-colors"
                >
                    <Plus className="h-3 w-3" />
                    {t('pickerCreateNew')}
                </button>
            )}
            {createOpen && (
                <ContactEditorDialog
                    mode="create"
                    open={createOpen}
                    onOpenChange={setCreateOpen}
                    onSaved={async (saved) => {
                        setCreateOpen(false);
                        await reload();
                        onSelect(saved);
                    }}
                />
            )}
        </div>
    );
}
