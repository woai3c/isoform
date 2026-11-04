import {
    KeyboardEvent,
    useCallback,
    useEffect,
    useRef,
    useState,
} from 'react';
import { useTranslation } from 'react-i18next';
import { Loader2, Plus, X } from 'lucide-react';
import {
    AppHandleDTO,
    BindAppHandle,
    CreateContact,
    EnsureSelfProfile,
    ListPrincipals,
    PrincipalDTO,
    PromoteAnonymousToContact,
    UnbindAppHandle,
    UpdatePrincipal,
} from '../../lib/wails-shim-app';
import { refreshUserProfile } from '@/hooks/useUserProfile';
import {
    Dialog,
    DialogCloseButton,
    DialogContent,
    DialogHeader,
    DialogTitle,
} from '../ui/dialog';
import { Button } from '../ui/button';
import { PeopleFailureLine, usePeopleDevTools } from '../people/people-failure';
import { appLabel, maskHandleId } from '../people/people-format';

// Canonical app ids users can bind handles to. Must match the chat
// renderers in renderParsedContentSection so resolver lookups succeed.
export const SUPPORTED_APPS = [
    'wechat',
    'telegram',
    'slack',
    'discord',
    'feishu',
    'imessage',
    'email',
];

export const RELATIONSHIPS = ['family', 'partner', 'friend', 'colleague', 'acquaintance'];

export type ContactEditorMode = 'create' | 'edit' | 'promote' | 'self';

export interface ContactEditorDialogProps {
    mode: ContactEditorMode;
    initial?: PrincipalDTO;
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onSaved: (principal: PrincipalDTO) => Promise<void> | void;
    /** The person saved but some chat accounts did not: the list behind
     *  updates at once while the dialog stays open with Try again. */
    onChanged?: (principal: PrincipalDTO) => Promise<void> | void;
}

const visibleHandles = (p?: PrincipalDTO) => p?.appHandles?.filter((h) => h.app !== 'contacts' && h.app !== 'vcard') ?? [];
const handleKey = (h: AppHandleDTO) => `${h.app.toLowerCase()}|${h.handleId}|${h.displayHandle}`;

/** What is left after a save: the words under Chat accounts. */
type SaveFailure =
    | { kind: 'save' }
    | { kind: 'add'; handles: AppHandleDTO[] }
    | { kind: 'remove'; handles: AppHandleDTO[] }
    | { kind: 'gone'; handle: AppHandleDTO };

export function useAppName(): (app: string) => string {
    const { t } = useTranslation('people');
    return (app: string) => appLabel((key, options) => t(key as never, options as never) as unknown as string, app);
}

export function ContactEditorDialog({
    mode,
    initial,
    open,
    onOpenChange,
    onSaved,
    onChanged,
}: ContactEditorDialogProps) {
    const { t } = useTranslation('settings');
    const appName = useAppName();
    const [name, setName] = useState(initial?.displayName ?? '');
    // No relationship until you pick one.
    const [relationship, setRelationship] = useState(initial?.relationship ?? '');
    const [aliases, setAliases] = useState<string[]>(initial?.aliases ?? []);
    const [handles, setHandles] = useState<AppHandleDTO[]>(visibleHandles(initial));
    const [saving, setSaving] = useState(false);
    const [failure, setFailure] = useState<SaveFailure | null>(null);
    // An account that belongs to someone else asks before it moves.
    const [moveAsk, setMoveAsk] = useState<{ handle: AppHandleDTO; other: string } | null>(null);
    const movesAllowed = useRef(new Set<string>());
    const savedProfile = useRef<PrincipalDTO | undefined>(initial);
    const savedHandles = useRef<AppHandleDTO[]>(visibleHandles(initial));
    // The name, relationship and other names last saved; Try again saves
    // only what is left.
    const savedFields = useRef<string | null>(null);

    useEffect(() => {
        savedProfile.current = initial;
        savedHandles.current = visibleHandles(initial);
        savedFields.current = null;
        movesAllowed.current = new Set();
        setName(initial?.displayName ?? '');
        setRelationship(initial?.relationship ?? '');
        setAliases(initial?.aliases ?? []);
        setHandles(visibleHandles(initial));
        setFailure(null);
        setMoveAsk(null);
    }, [initial]);

    const save = useCallback(async () => {
        const finalName = name.trim();
        if (!finalName) return;
        setSaving(true);
        setFailure(null);
        const owner = mode === 'edit' || mode === 'promote' ? savedProfile.current : undefined;
        if (mode !== 'self') {
            // Ask before taking an account another person has.
            const before = new Set(savedHandles.current.map(handleKey));
            const adding = handles.filter((h) => !before.has(handleKey(h)) && !movesAllowed.current.has(handleKey(h)));
            if (adding.length > 0) {
                const everyone = await ListPrincipals().catch(() => [] as PrincipalDTO[]);
                for (const h of adding) {
                    const other = (everyone ?? []).find((p) => p.id !== owner?.id && (p.appHandles ?? []).some((x) => handleKey(x) === handleKey(h)));
                    if (other) {
                        setMoveAsk({ handle: h, other: other.contactName || other.displayName });
                        setSaving(false);
                        return;
                    }
                }
            }
        }
        const fields = JSON.stringify([finalName, relationship, aliases]);
        let saved: PrincipalDTO | null = null;
        try {
            if (mode === 'self') {
                // EnsureSelfProfile is the only binding that creates/updates
                // the singleton self principal; it takes name + aliases only.
                saved = await EnsureSelfProfile(finalName, aliases);
                // In standalone the displayed user name comes from this self
                // profile (Go GetUserProfile fallback) — refresh so the sidebar
                // / menu / greeting update immediately.
                refreshUserProfile();
            } else if (mode === 'create') {
                saved = await CreateContact(finalName, relationship, aliases, handles);
            } else if ((mode === 'edit' || mode === 'promote') && savedProfile.current) {
                const profile = savedProfile.current;
                if (savedFields.current === fields) {
                    saved = profile;
                } else {
                    saved = mode === 'promote' && profile.kind === 'anonymous'
                        ? await PromoteAnonymousToContact(profile.id, finalName, relationship, aliases)
                        : await UpdatePrincipal({ ...profile, displayName: finalName, relationship, aliases });
                    savedProfile.current = saved;
                    savedFields.current = fields;
                }
            }
        } catch {
            setFailure({ kind: 'save' });
            setSaving(false);
            return;
        }
        if (!saved) {
            setSaving(false);
            return;
        }
        if (mode === 'edit' || mode === 'promote') {
            const result = await syncHandleChanges(saved.id, savedHandles.current, handles, (current) => {
                savedHandles.current = current;
            });
            if (result.gone || result.added.length > 0 || result.removed.length > 0) {
                setFailure(
                    result.gone
                        ? { kind: 'gone', handle: result.gone }
                        : result.added.length > 0
                          ? { kind: 'add', handles: result.added }
                          : { kind: 'remove', handles: result.removed },
                );
                setSaving(false);
                await onChanged?.(saved);
                return;
            }
        }
        await onSaved(saved);
    }, [mode, name, relationship, aliases, handles, onSaved, onChanged]);

    const isSelf = mode === 'self';
    const handleWords = (h: AppHandleDTO) => h.displayHandle || maskHandleId(h.handleId);
    const failureText = !failure
        ? ''
        : failure.kind === 'save'
          ? t('contacts.saveFailed', { name: name.trim() })
          : failure.kind === 'gone'
            ? t('contacts.saveAddGone', { handle: handleWords(failure.handle) })
            : failure.handles.length > 1
              ? t(failure.kind === 'add' ? 'contacts.saveAddFailedMany' : 'contacts.saveRemoveFailedMany', { count: failure.handles.length })
              : t(failure.kind === 'add' ? 'contacts.saveAddFailed' : 'contacts.saveRemoveFailed', { handle: handleWords(failure.handles[0]), app: appName(failure.handles[0].app) });
    const title =
        mode === 'self'
            ? t('contacts.selfTitle')
            : mode === 'create'
              ? t('contacts.createTitle')
              : mode === 'promote'
                ? t('contacts.promoteTitle')
                : t('contacts.editTitle');

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="w-[560px]">
                <DialogHeader>
                    <DialogTitle>{title}</DialogTitle>
                    <DialogCloseButton />
                </DialogHeader>
                <div className="flex flex-col gap-4 px-6 py-4 overflow-y-auto">
                    <div className="flex flex-col gap-1.5">
                        <label className="text-[12px] text-muted">{t('contacts.fieldName')}</label>
                        <input
                            type="text"
                            value={name}
                            onChange={(e) => setName(e.target.value)}
                            placeholder={t('contacts.namePlaceholder') as string}
                            className="h-9 rounded-lg border border-border bg-panel px-3 text-[13px] outline-none focus-visible:border-accent-2/60 focus-visible:ring-2 focus-visible:ring-accent-2/30"
                            autoFocus
                        />
                    </div>
                    {!isSelf && (
                        <div className="flex flex-col gap-1.5">
                            <label className="text-[12px] text-muted">{t('contacts.fieldRelationship')}</label>
                            <select
                                value={relationship}
                                onChange={(e) => setRelationship(e.target.value)}
                                className="h-9 rounded-lg border border-border bg-panel px-3 text-[13px] outline-none focus-visible:border-accent-2/60 focus-visible:ring-2 focus-visible:ring-accent-2/30"
                            >
                                <option value="">{t('contacts.relationshipNone')}</option>
                                {RELATIONSHIPS.map((r) => (
                                    <option key={r} value={r}>
                                        {t(`contacts.relationship.${r}` as never, r) as string}
                                    </option>
                                ))}
                            </select>
                        </div>
                    )}
                    <div className="flex flex-col gap-1.5">
                        <label className="text-[12px] text-muted">{t('contacts.fieldAliases')}</label>
                        <ChipInput
                            chips={aliases}
                            onChange={setAliases}
                            placeholder={t('contacts.aliasesPlaceholder') as string}
                        />
                        <p className="text-[11px] text-muted">{t('contacts.aliasesHint')}</p>
                    </div>
                    {!isSelf && (
                        <div className="flex flex-col gap-1.5">
                            <label className="text-[12px] text-muted">{t('contacts.fieldHandles')}</label>
                            <HandleListEditor handles={handles} onChange={setHandles} />
                            {moveAsk && (
                                <div role="alertdialog" aria-label={t('contacts.moveQuestion', { handle: handleWords(moveAsk.handle), other: moveAsk.other, name: name.trim() }) as string} className="flex flex-wrap items-center gap-2 text-[12.5px] text-copper">
                                    <span>{t('contacts.moveQuestion', { handle: handleWords(moveAsk.handle), other: moveAsk.other, name: name.trim() })}</span>
                                    <Button
                                        size="sm"
                                        onClick={() => {
                                            movesAllowed.current.add(handleKey(moveAsk.handle));
                                            setMoveAsk(null);
                                            void save();
                                        }}
                                    >
                                        {t('contacts.move')}
                                    </Button>
                                    <Button size="sm" variant="quiet" onClick={() => setMoveAsk(null)}>
                                        {t('contacts.cancel')}
                                    </Button>
                                </div>
                            )}
                        </div>
                    )}
                    {failure && <PeopleFailureLine text={failureText as string} onRetry={save} />}
                </div>
                <div className="flex items-center justify-end gap-2 px-6 pb-5">
                    <button
                        type="button"
                        onClick={() => onOpenChange(false)}
                        className="px-3 py-1.5 rounded-md text-xs font-medium text-ink bg-border/50 hover:bg-border transition-colors"
                    >
                        {t('contacts.cancel')}
                    </button>
                    <button
                        type="button"
                        onClick={save}
                        disabled={!name.trim() || saving}
                        className="inline-flex items-center gap-1.5 px-3 py-1.5 rounded-md text-xs font-medium patina-verdigris bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] hover:opacity-90 disabled:opacity-50 transition-opacity"
                    >
                        {saving && <Loader2 className="h-3 w-3 animate-spin" />}
                        {t('contacts.save')}
                    </button>
                </div>
            </DialogContent>
        </Dialog>
    );
}

// syncHandles diffs the old vs new handle list and issues the minimal set
// of Bind/Unbind calls. Equality is determined by app + handleId +
// displayHandle (the same unique tuple the storage layer uses).
export async function syncHandles(
    principalID: string,
    before: AppHandleDTO[],
    after: AppHandleDTO[],
    onProgress: (current: AppHandleDTO[]) => void = () => {},
): Promise<void> {
    const key = (h: AppHandleDTO) =>
        `${h.app.toLowerCase()}|${h.handleId}|${h.displayHandle.toLowerCase()}`;
    const beforeMap = new Map(before.map((h) => [key(h), h] as const));
    const afterMap = new Map(after.map((h) => [key(h), h] as const));

    for (const [k, h] of beforeMap) {
        if (!afterMap.has(k)) {
            await UnbindAppHandle(h.id);
            beforeMap.delete(k);
            onProgress([...beforeMap.values()]);
        }
    }
    for (const [k, h] of afterMap) {
        if (!beforeMap.has(k)) {
            const bound = await BindAppHandle(principalID, h.app, h.handleId, h.displayHandle);
            beforeMap.set(k, bound);
            onProgress([...beforeMap.values()]);
        }
    }
}

const isGone = (err: unknown) => /no rows|not exist|no longer/i.test(String(err));

/** syncHandles that keeps going: each account that didn't save is
 *  returned, and the rest stay saved. gone: the person was merged or
 *  deleted meanwhile. */
export async function syncHandleChanges(
    principalID: string,
    before: AppHandleDTO[],
    after: AppHandleDTO[],
    onProgress: (current: AppHandleDTO[]) => void = () => {},
): Promise<{ added: AppHandleDTO[]; removed: AppHandleDTO[]; gone?: AppHandleDTO }> {
    const beforeMap = new Map(before.map((h) => [handleKey(h), h] as const));
    const afterMap = new Map(after.map((h) => [handleKey(h), h] as const));
    const added: AppHandleDTO[] = [];
    const removed: AppHandleDTO[] = [];
    for (const [k, h] of [...beforeMap]) {
        if (afterMap.has(k)) continue;
        try {
            await UnbindAppHandle(h.id);
            beforeMap.delete(k);
            onProgress([...beforeMap.values()]);
        } catch {
            removed.push(h);
        }
    }
    for (const [k, h] of afterMap) {
        if (beforeMap.has(k)) continue;
        try {
            const bound = await BindAppHandle(principalID, h.app, h.handleId, h.displayHandle);
            beforeMap.set(k, bound);
            onProgress([...beforeMap.values()]);
        } catch (err) {
            if (isGone(err)) return { added, removed, gone: h };
            added.push(h);
        }
    }
    return { added, removed };
}

// ── Reusable: handle list editor ──

export function HandleListEditor({
    handles,
    onChange,
}: {
    handles: AppHandleDTO[];
    onChange: (next: AppHandleDTO[]) => void;
}) {
    const { t } = useTranslation('settings');
    const appName = useAppName();
    const devTools = usePeopleDevTools();
    const [draftApp, setDraftApp] = useState(SUPPORTED_APPS[0]);
    const [draftHandleId, setDraftHandleId] = useState('');
    const [draftDisplay, setDraftDisplay] = useState('');

    const addHandle = useCallback(() => {
        const handleId = draftHandleId.trim();
        const display = draftDisplay.trim();
        if (!draftApp || (!handleId && !display)) return;
        const next: AppHandleDTO = {
            id: 0,
            principalId: '',
            app: draftApp,
            handleId,
            displayHandle: display,
            createdAt: Date.now(),
        };
        onChange([...handles, next]);
        setDraftHandleId('');
        setDraftDisplay('');
    }, [draftApp, draftHandleId, draftDisplay, handles, onChange]);

    const removeHandle = useCallback(
        (idx: number) => {
            onChange(handles.filter((_, i) => i !== idx));
        },
        [handles, onChange],
    );

    return (
        <div className="flex flex-col gap-2">
            {handles.length > 0 && (
                <ul className="flex flex-col gap-1.5">
                    {handles.map((h, idx) => (
                        <li
                            key={`${h.id}-${idx}`}
                            className="flex items-center gap-2 rounded-md border border-border bg-panel/30 px-2 py-1.5 text-[12px]"
                        >
                            <span className="font-medium text-ink/80 w-[80px]">{appName(h.app)}</span>
                            <span className="flex-1 text-ink truncate">
                                {h.displayHandle || (devTools ? h.handleId : maskHandleId(h.handleId))}
                                {devTools && h.handleId && h.displayHandle && (
                                    <span className="text-muted ml-1">({h.handleId})</span>
                                )}
                            </span>
                            <button
                                type="button"
                                onClick={() => removeHandle(idx)}
                                className="text-muted hover:text-error transition-colors"
                                aria-label={t('contacts.removeHandleAria') as string}
                            >
                                <X className="h-3.5 w-3.5" />
                            </button>
                        </li>
                    ))}
                </ul>
            )}
            <div className="flex items-center gap-1.5">
                <select
                    value={draftApp}
                    onChange={(e) => setDraftApp(e.target.value)}
                    className="h-8 rounded-md border border-border bg-panel px-2 text-[12px] outline-none focus-visible:border-accent-2/60 focus-visible:ring-2 focus-visible:ring-accent-2/30"
                >
                    {SUPPORTED_APPS.map((a) => (
                        <option key={a} value={a}>
                            {appName(a)}
                        </option>
                    ))}
                </select>
                <input
                    type="text"
                    value={draftDisplay}
                    onChange={(e) => setDraftDisplay(e.target.value)}
                    placeholder={t('contacts.handleDisplayPlaceholder') as string}
                    className="h-8 flex-1 rounded-md border border-border bg-panel px-2 text-[12px] outline-none focus-visible:border-accent-2/60 focus-visible:ring-2 focus-visible:ring-accent-2/30"
                />
                {devTools && (
                    <input
                        type="text"
                        value={draftHandleId}
                        onChange={(e) => setDraftHandleId(e.target.value)}
                        placeholder={t('contacts.handleIdPlaceholder') as string}
                        className="h-8 w-[120px] rounded-md border border-border bg-panel px-2 text-[12px] outline-none focus-visible:border-accent-2/60 focus-visible:ring-2 focus-visible:ring-accent-2/30"
                    />
                )}
                <button
                    type="button"
                    onClick={addHandle}
                    disabled={!draftApp || (!draftDisplay.trim() && !draftHandleId.trim())}
                    aria-label={t('contacts.addHandleAria') as string}
                    className="inline-flex items-center gap-1 h-8 px-2.5 rounded-md text-xs font-medium patina-verdigris bg-solid-btn-bg text-solid-btn-fg shadow-[var(--shadow-solid-btn)] hover:opacity-90 disabled:opacity-50 transition-opacity"
                >
                    <Plus className="h-3 w-3" />
                </button>
            </div>
        </div>
    );
}

// ── Reusable: chip input ──

export function ChipInput({
    chips,
    onChange,
    placeholder,
}: {
    chips: string[];
    onChange: (next: string[]) => void;
    placeholder?: string;
}) {
    const { t } = useTranslation('settings');
    const [draft, setDraft] = useState('');

    const add = useCallback(
        (raw: string) => {
            const value = raw.trim();
            if (!value) return;
            if (chips.includes(value)) return;
            onChange([...chips, value]);
            setDraft('');
        },
        [chips, onChange],
    );

    const remove = useCallback(
        (target: string) => {
            onChange(chips.filter((c) => c !== target));
        },
        [chips, onChange],
    );

    const onKeyDown = useCallback(
        (e: KeyboardEvent<HTMLInputElement>) => {
            if (e.key === 'Enter' || e.key === ',') {
                e.preventDefault();
                add(draft);
            } else if (e.key === 'Backspace' && draft === '' && chips.length > 0) {
                e.preventDefault();
                onChange(chips.slice(0, -1));
            }
        },
        [add, draft, chips, onChange],
    );

    return (
        <div className="min-h-[36px] rounded-lg border border-border bg-panel px-2 py-1.5 flex flex-wrap items-center gap-1.5 focus-within:border-accent-2/60 focus-within:ring-2 focus-within:ring-accent-2/30 transition-colors">
            {chips.map((c) => (
                <span
                    key={c}
                    className="inline-flex items-center gap-1 rounded-full bg-panel-warm dark:bg-panel/50 pl-2.5 pr-1 py-0.5 text-[12px] text-ink"
                >
                    {c}
                    <button
                        type="button"
                        onClick={() => remove(c)}
                        aria-label={t('contacts.removeChipAria', { value: c }) as string}
                        className="ml-0.5 inline-flex h-3.5 w-3.5 items-center justify-center rounded-full text-muted hover:text-ink"
                    >
                        <X className="h-2.5 w-2.5" />
                    </button>
                </span>
            ))}
            <input
                type="text"
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={onKeyDown}
                onBlur={() => draft.trim() && add(draft)}
                placeholder={placeholder}
                className="flex-1 min-w-[120px] bg-transparent text-[12px] text-ink outline-none placeholder:text-muted"
                autoComplete="off"
            />
        </div>
    );
}
