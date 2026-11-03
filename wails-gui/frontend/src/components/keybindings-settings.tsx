import { useState, useEffect, useCallback, useRef } from 'react';
import { useTranslation } from 'react-i18next';
import { ChevronDown, RotateCcw } from 'lucide-react';
import { Button } from '@/components/ui/button';
import { Switch } from '@/components/ui/switch';
import { ConfirmDialog } from '@/components/confirm-dialog';
import { SettingsCard, SettingsRow } from '@/components/settings/settings-primitives';
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select';
import {
    comboEquals,
    comboToDisplayString,
    eventToCombo,
    type KeybindingAction,
    type KeyCombo,
    type useKeybindings,
} from '@/hooks/useKeybindings';
import { toast } from 'sonner';
import {
    GetQuickChatHotkey,
    SetQuickChatHotkey,
    SetQuickChatHotkeyRecording,
} from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import { isMac, isWindows } from '@/lib/platform';

const QUICK_CHAT_GLOBAL_ID = 'quick-chat-global';

// Keep the persisted KeyCombo shape, including sentinels for double tap and Off.
const QUICK_CHAT_PRESETS = [
    { id: 'optionTwice', combo: { meta: false, ctrl: false, alt: false, shift: false, key: 'opt-opt' } },
    { id: 'altSpace', combo: { meta: false, ctrl: false, alt: true, shift: false, key: ' ' } },
    { id: 'controlSpace', combo: { meta: false, ctrl: true, alt: false, shift: false, key: ' ' } },
    { id: 'commandShiftSpace', combo: { meta: true, ctrl: false, alt: false, shift: true, key: ' ' } },
    { id: 'off', combo: { meta: false, ctrl: false, alt: false, shift: false, key: 'off' } },
] as const;

type Keybindings = ReturnType<typeof useKeybindings>;

// ── Shared recording and saving ──

// One recorder per card: which row is listening for keys, plus the save and
// reset rules shared by every shortcut (conflict check, and the Quick Chat
// round trip to the Go hook).
function useShortcutEditor(keybindings: Keybindings) {
    const { t } = useTranslation('settings');
    const [recordingId, setRecordingId] = useState<string | null>(null);

    const startRecording = useCallback((actionId: string) => {
        setRecordingId(actionId);
    }, []);

    const cancelRecording = useCallback(() => {
        setRecordingId(null);
    }, []);

    const saveCombo = useCallback(
        (actionId: string, combo: KeyCombo) => {
            const conflict = keybindings.findConflict(combo, actionId);
            if (conflict) {
                toast.error(
                    t('keybindings.toasts.conflict', {
                        action: keybindings.actions.find((entry) => entry.id === conflict)?.label,
                    }),
                );
                return;
            }
            // Quick Chat is OS-level: the renderer never sees its keystrokes,
            // so we push the combo down to Go which retunes the platform hook
            // (Windows WH_KEYBOARD_LL / macOS CGEventTap). At least one
            // modifier is required by the Go validator; reject early with a
            // clearer message than the binding error.
            if (actionId === QUICK_CHAT_GLOBAL_ID) {
                if (!combo.meta && !combo.ctrl && !combo.alt && !combo.shift) {
                    toast.error(t('keybindings.toasts.modifierRequired'));
                    return;
                }
                SetQuickChatHotkey(combo).then(
                    () => {
                        keybindings.updateBinding(actionId, combo);
                        setRecordingId(null);
                        toast.success(t('keybindings.toasts.shortcutUpdated'));
                    },
                    (err) => {
                        toast.error(
                            t('keybindings.toasts.applyFailed', {
                                error: String(err?.message ?? err ?? ''),
                            }),
                        );
                    },
                );
                return;
            }
            keybindings.updateBinding(actionId, combo);
            setRecordingId(null);
            toast.success(t('keybindings.toasts.shortcutUpdated'));
        },
        [keybindings, t],
    );

    const quickChatDefault = keybindings.actions.find(
        (a) => a.id === QUICK_CHAT_GLOBAL_ID,
    )?.defaultCombo;

    const resetBinding = useCallback(
        (actionId: string) => {
            if (actionId === QUICK_CHAT_GLOBAL_ID && quickChatDefault) {
                // Push the default down to Go FIRST so the OS hook is
                // already retuned by the time the UI flips back.
                void SetQuickChatHotkey(quickChatDefault).finally(() => {
                    keybindings.resetBinding(actionId);
                });
                return;
            }
            keybindings.resetBinding(actionId);
        },
        [keybindings, quickChatDefault],
    );

    return { recordingId, startRecording, cancelRecording, saveCombo, resetBinding };
}

// ── Quick Chat shortcut (General, everyday) ──

interface QuickChatShortcutCardProps {
    keybindings: Keybindings;
}

/** The system-wide Quick Chat hotkey, on its own card because it is the one
 *  shortcut most people change. */
export function QuickChatShortcutCard({ keybindings }: QuickChatShortcutCardProps) {
    const { t } = useTranslation('settings');
    const action = keybindings.actions.find((a) => a.id === QUICK_CHAT_GLOBAL_ID);
    const [loading, setLoading] = useState(true);
    const [saving, setSaving] = useState(false);
    const savingRef = useRef(false);

    // Hydrate the OS-level Quick Chat hotkey from Go on mount. The Go
    // side persists this binding to ~/.yansu-agent/quick_chat_hotkey.json
    // and is the source of truth for what the OS hook actually fires on,
    // so localStorage gets reconciled to it rather than the other way
    // around. Without this, a user who changed the combo on one machine
    // and synced their settings from another would see a stale value
    // here while the hotkey keeps firing on the persisted one.
    useEffect(() => {
        if (!isMac && !isWindows) return;
        let cancelled = false;
        void (async () => {
            try {
                const persisted = await GetQuickChatHotkey();
                if (cancelled) return;
                const current = keybindings.getCombo(QUICK_CHAT_GLOBAL_ID);
                if (persisted && persisted.key && (!current || !comboEquals(current, persisted))) {
                    keybindings.updateBinding(QUICK_CHAT_GLOBAL_ID, persisted);
                }
            } catch {
                // Non-fatal: dev server reload race, missing binding, etc.
            } finally {
                if (!cancelled) setLoading(false);
            }
        })();
        return () => {
            cancelled = true;
        };
        // We intentionally only run this on mount.
        // eslint-disable-next-line react-hooks/exhaustive-deps
    }, []);

    if (!action || (!isMac && !isWindows)) return null;

    const current = keybindings.getCombo(action.id) ?? action.defaultCombo;
    const presets = QUICK_CHAT_PRESETS.filter(({ id }) => isMac ? id !== 'altSpace' : id !== 'optionTwice');
    const preset = presets.find(({ combo }) => comboEquals(combo, current));
    const savePreset = async (id: string) => {
        const next = presets.find((entry) => entry.id === id);
        if (!next || savingRef.current) return;
        const conflict = keybindings.findConflict(next.combo, action.id);
        if (conflict) {
            toast.error(t('keybindings.toasts.conflict', {
                action: keybindings.actions.find((entry) => entry.id === conflict)?.label,
            }));
            return;
        }
        savingRef.current = true;
        setSaving(true);
        try {
            await SetQuickChatHotkey(next.combo);
            keybindings.updateBinding(action.id, next.combo);
            toast.success(t('keybindings.toasts.shortcutUpdated'));
        } catch (err) {
            toast.error(t('keybindings.toasts.applyFailed', { error: String(err) }));
        } finally {
            savingRef.current = false;
            setSaving(false);
        }
    };

    return (
        <SettingsCard>
            <SettingsRow
                title={t('keybindings.quickChat.title')}
                description={t('keybindings.quickChat.description')}
                control={
                    <Select value={preset?.id ?? ''} onValueChange={(id) => void savePreset(id)} disabled={loading || saving}>
                        <SelectTrigger className="w-[240px] text-xs" aria-label={t('keybindings.quickChat.title')}>
                            <SelectValue placeholder={t('keybindings.quickChat.savedShortcut')} />
                        </SelectTrigger>
                        <SelectContent>
                            {presets.map(({ id }) => (
                                <SelectItem key={id} value={id} className="text-xs">
                                    {isWindows && id === 'commandShiftSpace' ? t('keybindings.quickChat.presets.windowsShiftSpace') : t(`keybindings.quickChat.presets.${id}`)}
                                </SelectItem>
                            ))}
                        </SelectContent>
                    </Select>
                }
            />
        </SettingsCard>
    );
}

// ── Keyboard shortcuts (General > Advanced) ──

interface KeybindingsSettingsProps {
    keybindings: Keybindings;
    vimMode: boolean;
    onVimModeChange: (enabled: boolean) => void;
}

/** In-app shortcuts, the read-only system-wide Pause observing shortcut and
 *  Vim-style navigation, on one card. Quick Chat has its own card and the
 *  developer-only shortcuts are not listed. */
export function KeybindingsSettings({
    keybindings,
    vimMode,
    onVimModeChange,
}: KeybindingsSettingsProps) {
    const { t } = useTranslation('settings');
    const editor = useShortcutEditor(keybindings);
    const [vimExpanded, setVimExpanded] = useState(false);
    const [confirmResetOpen, setConfirmResetOpen] = useState(false);

    // Editable shortcuts first, then the read-only system-wide ones.
    const listed = keybindings.actions.filter(
        (a) => a.id !== QUICK_CHAT_GLOBAL_ID && !a.devOnly,
    );
    const shortcuts = [
        ...listed.filter((a) => !a.readonly),
        ...listed.filter((a) => a.readonly),
    ];
    const customized = shortcuts.filter(
        (a) => !a.readonly && keybindings.isCustomized(a.id),
    );

    // Resets the shortcuts on this card; Quick Chat keeps its own reset.
    const handleResetAll = () => {
        for (const action of customized) {
            editor.resetBinding(action.id);
        }
        toast.success(t('keybindings.toasts.shortcutResetAll'));
    };

    return (
        <SettingsCard
            title={t('keybindings.title')}
            description={t('keybindings.description')}
        >
            <div className="flex flex-col">
                {shortcuts.map((action) => (
                    <ShortcutRow
                        key={action.id}
                        action={action}
                        title={action.label}
                        description={action.description}
                        currentCombo={keybindings.getCombo(action.id) ?? action.defaultCombo}
                        isCustomized={keybindings.isCustomized(action.id)}
                        recording={editor.recordingId === action.id}
                        onRecord={editor.startRecording}
                        onCancelRecord={editor.cancelRecording}
                        onSaveCombo={editor.saveCombo}
                        onReset={editor.resetBinding}
                    />
                ))}
                <SettingsRow
                    title={t('keybindings.vim.title')}
                    description={t('keybindings.vim.description')}
                    control={
                        <Switch
                            checked={vimMode}
                            onCheckedChange={onVimModeChange}
                            aria-label={t('keybindings.vim.title')}
                        />
                    }
                />
            </div>

            <div className="flex flex-col gap-2 pt-1">
                <button
                    type="button"
                    aria-expanded={vimExpanded}
                    onClick={() => setVimExpanded((v) => !v)}
                    className="flex items-center gap-1.5 self-start text-xs text-muted hover:text-ink transition-colors"
                >
                    <ChevronDown
                        aria-hidden
                        className={`h-3 w-3 shrink-0 transition-transform ${vimExpanded ? 'rotate-180' : ''}`}
                    />
                    {vimExpanded ? t('keybindings.vim.hideKeys') : t('keybindings.vim.showKeys')}
                </button>
                {vimExpanded && (
                    <div className="flex flex-col">
                        {!vimMode && (
                            <p className="settings-row-desc">{t('keybindings.vim.offReason')}</p>
                        )}
                        <div className={`flex flex-col ${vimMode ? '' : 'opacity-60'}`}>
                            {keybindings.vimActions.map((action) => (
                                <ShortcutRow
                                    key={action.id}
                                    action={action}
                                    currentCombo={action.defaultCombo}
                                    isCustomized={false}
                                    recording={false}
                                    onRecord={editor.startRecording}
                                    onCancelRecord={editor.cancelRecording}
                                    onSaveCombo={editor.saveCombo}
                                    onReset={editor.resetBinding}
                                />
                            ))}
                        </div>
                        <p className="text-[11px] text-muted mt-2">
                            {t('keybindings.vim.readonlyHint')}
                        </p>
                    </div>
                )}
            </div>

            {customized.length > 0 && (
                <div className="flex justify-end pt-3">
                    <Button variant="outline" size="sm" onClick={() => setConfirmResetOpen(true)}>
                        <RotateCcw className="h-3.5 w-3.5 mr-1.5" />
                        {t('keybindings.resetAll')}
                    </Button>
                </div>
            )}
            <ConfirmDialog
                open={confirmResetOpen}
                onOpenChange={setConfirmResetOpen}
                title={t('keybindings.resetAllConfirm.title')}
                description={t('keybindings.resetAllConfirm.description')}
                confirmLabel={t('keybindings.resetAllConfirm.confirm')}
                onConfirm={handleResetAll}
            />
        </SettingsCard>
    );
}

// ── ShortcutRow ──

interface ShortcutRowProps {
    action: KeybindingAction;
    /** Overrides the command's own label, for a row that stands on its own card. */
    title?: string;
    /** Overrides the command's own description. */
    description?: string;
    currentCombo: KeyCombo;
    isCustomized: boolean;
    recording: boolean;
    onRecord: (actionId: string) => void;
    onCancelRecord: () => void;
    onSaveCombo: (actionId: string, combo: KeyCombo) => void;
    onReset: (actionId: string) => void;
}

function ShortcutRow({
    action,
    title,
    description,
    currentCombo,
    isCustomized,
    recording,
    onRecord,
    onCancelRecord,
    onSaveCombo,
    onReset,
}: ShortcutRowProps) {
    const { t } = useTranslation('settings');
    const rowRef = useRef<HTMLDivElement>(null);
    const isQuickChat = action.id === QUICK_CHAT_GLOBAL_ID;

    // Recording mode: capture keydown.
    //
    // For most actions, a window-level keydown listener is enough. For
    // quick-chat-global we also engage the Go-side hook so the renderer
    // can record combos the OS would otherwise eat before they reach the
    // browser (Alt+Space → system menu, Win+L → lock screen, etc.). The
    // hook captures every non-modifier key during recording, snapshots
    // the live modifier state, swallows the keystroke, and emits the
    // combo via the `quickChat:hotkeyRecorded` Wails event. Escape is
    // forwarded the same way so cancel still works.
    useEffect(() => {
        if (!recording) return;

        const handleKeyDown = (e: KeyboardEvent) => {
            e.preventDefault();
            e.stopPropagation();

            if (e.key === 'Escape') {
                onCancelRecord();
                return;
            }

            const combo = eventToCombo(e);
            if (combo) {
                onSaveCombo(action.id, combo);
            }
        };

        const timeout = setTimeout(() => {
            window.addEventListener('keydown', handleKeyDown, true);
        }, 50);

        let offRecorded: (() => void) | undefined;
        if (isQuickChat) {
            void SetQuickChatHotkeyRecording(true);
            offRecorded = EventsOn('quickChat:hotkeyRecorded', (combo: KeyCombo) => {
                if (
                    combo.key === 'escape' &&
                    !combo.meta && !combo.ctrl && !combo.alt && !combo.shift
                ) {
                    onCancelRecord();
                    return;
                }
                onSaveCombo(action.id, combo);
            });
        }

        return () => {
            clearTimeout(timeout);
            window.removeEventListener('keydown', handleKeyDown, true);
            if (isQuickChat) {
                void SetQuickChatHotkeyRecording(false);
                offRecorded?.();
            }
        };
    }, [recording, isQuickChat, action.id, onCancelRecord, onSaveCombo]);

    // Click outside to cancel recording
    useEffect(() => {
        if (!recording) return;

        const handleClick = (e: MouseEvent) => {
            if (rowRef.current && !rowRef.current.contains(e.target as Node)) {
                onCancelRecord();
            }
        };

        // Delay to avoid the initial click that started recording
        const timeout = setTimeout(() => {
            document.addEventListener('mousedown', handleClick);
        }, 50);

        return () => {
            clearTimeout(timeout);
            document.removeEventListener('mousedown', handleClick);
        };
    }, [recording, onCancelRecord]);

    const label = title ?? (t(`keybindings.commands.${action.id}.label` as never) as string);
    const detail =
        description ?? (t(`keybindings.commands.${action.id}.description` as never) as string);

    return (
        <div ref={rowRef} className="settings-row">
            <div className="flex flex-col min-w-0">
                <span className="settings-row-title truncate">{label}</span>
                {detail && <span className="settings-row-desc">{detail}</span>}
            </div>

            <div className="flex items-center gap-1.5 shrink-0">
                {/* Key chord pill */}
                <button
                    type="button"
                    onClick={() => !action.readonly && onRecord(action.id)}
                    disabled={action.readonly}
                    aria-label={recording ? undefined : `${label}: ${comboToDisplayString(currentCombo)}`}
                    className={`inline-flex items-center gap-1 px-2.5 py-1 rounded-md text-xs font-mono transition-all ${
                        recording
                            ? 'border border-accent-2 ring-1 ring-accent-2/40 bg-accent-2/5 text-accent-2 animate-pulse'
                            : action.readonly
                              ? 'bg-ink/5 text-muted cursor-default'
                              : 'bg-background border border-border text-ink hover:border-ink/20 cursor-pointer'
                    }`}
                >
                    {recording ? t('keybindings.recordPlaceholder') : comboToDisplayString(currentCombo)}
                </button>

                {/* Reset button (only for customized, non-readonly actions) */}
                {isCustomized && !action.readonly && (
                    <button
                        type="button"
                        onClick={() => {
                            onReset(action.id);
                            toast.success(t('keybindings.toasts.shortcutResetSingle'));
                        }}
                        className="p-1 rounded-md text-muted hover:text-ink hover:bg-ink/[0.03] transition-colors"
                        title={t('keybindings.resetTooltip')}
                        aria-label={t('keybindings.resetTooltip')}
                    >
                        <RotateCcw className="h-3 w-3" />
                    </button>
                )}
            </div>
        </div>
    );
}
