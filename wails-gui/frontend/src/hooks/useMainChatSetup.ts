import { useCallback, useEffect, useRef, useState } from 'react';
import {
    OpenPermissionSettings,
    RequestPermission,
} from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';
import i18n from '@/i18n';
import {
    AnswerMainChatSetup,
    EnsureSelfProfile,
    GetMainChatSetup,
    ListPrincipalsByKind,
    type MainChatSetupState,
    type MainChatSetupStep,
} from '@/lib/wails-shim-app';
import { refreshUserProfile, useUserProfile } from '@/hooks/useUserProfile';

// The main chat's setup sequence. Go owns the order and the "never re-ask"
// rule (GetMainChatSetup); this hook shows the step it names, and a macOS
// prompt only ever follows a tap on Turn on (grant). Nothing here asks for a
// permission on mount, focus or any event.

const SETUP_REFRESH_DEBOUNCE_MS = 300;
/** Focus and visibility re-check at most this often; events re-check at once. */
const SETUP_FOCUS_THROTTLE_MS = 30_000;

/** The renderer's language as Go expects it ("en" or "zh-CN"). */
function setupLanguage(): string {
    return (i18n.language || 'en').toLowerCase().startsWith('zh') ? 'zh-CN' : 'en';
}

/**
 * What a Save on the name card came to: the name is stored (the step closes
 * now or on the next read), nothing was written, another name is already
 * stored (the card asks which to keep), or a second tap while busy.
 */
export type SaveNameResult =
    | { kind: 'saved' }
    | { kind: 'failed' }
    | { kind: 'stored'; name: string }
    | { kind: 'busy' };

export interface MainChatSetupApi {
    state: MainChatSetupState | null;
    /** The step to show now, or null when the sequence is finished. */
    current: MainChatSetupStep | null;
    /**
     * 1-based position of the current step among the chat's own asks, and
     * their count. Steps closed before the chat reached them (already on, or
     * answered in onboarding) are not counted.
     */
    position: number;
    total: number;
    busyKey: string | null;
    /** The permission sent to System Settings, waiting to be turned on. */
    waitingKey: string | null;
    grant: (step: MainChatSetupStep) => Promise<void>;
    /**
     * Saves what the user wants to be called, then closes the name step. A
     * different name stored meanwhile (renamed from the menu, or named by a
     * connected app) is not written over unless `replace` is set.
     */
    saveName: (step: MainChatSetupStep, name: string, replace?: boolean) => Promise<SaveNameResult>;
    notNow: (step: MainChatSetupStep) => Promise<void>;
    openSettings: (step: MainChatSetupStep) => Promise<void>;
    refresh: () => Promise<MainChatSetupState | null | undefined>;
}

export function useMainChatSetup(enabled: boolean): MainChatSetupApi {
    const [state, setState] = useState<MainChatSetupState | null>(null);
    const [busyKey, setBusyKey] = useState<string | null>(null);
    const [waitingKey, setWaitingKey] = useState<string | null>(null);
    const requestRef = useRef(0);
    const busyRef = useRef(false);
    const liveRef = useRef(enabled);
    liveRef.current = enabled;

    const apply = useCallback((next: MainChatSetupState | null) => {
        if (!next || !liveRef.current) return;
        setState(next);
    }, []);

    const refresh = useCallback(async (): Promise<MainChatSetupState | null | undefined> => {
        if (!liveRef.current) return;
        const request = ++requestRef.current;
        const next = await GetMainChatSetup(setupLanguage()).catch(() => null);
        if (request !== requestRef.current) return;
        apply(next);
        return next;
    }, [apply]);

    useEffect(() => {
        if (!enabled) return;
        let timer: ReturnType<typeof setTimeout> | null = null;
        const schedule = () => {
            if (timer) clearTimeout(timer);
            timer = setTimeout(() => {
                timer = null;
                void refresh();
            }, SETUP_REFRESH_DEBOUNCE_MS);
        };
        let lastFocusCheck = Date.now();
        const onFocus = () => {
            const now = Date.now();
            if (now - lastFocusCheck < SETUP_FOCUS_THROTTLE_MS) return;
            lastFocusCheck = now;
            schedule();
        };
        const onVisible = () => {
            if (document.visibilityState === 'visible') onFocus();
        };
        void refresh();
        const unsubs = [
            EventsOn('permissions:changed', schedule),
            EventsOn('model-accounts:updated', schedule),
            EventsOn('background-listening:changed', schedule),
        ];
        window.addEventListener('focus', onFocus);
        document.addEventListener('visibilitychange', onVisible);
        return () => {
            if (timer) clearTimeout(timer);
            requestRef.current++;
            unsubs.forEach((u) => u());
            window.removeEventListener('focus', onFocus);
            document.removeEventListener('visibilitychange', onVisible);
        };
    }, [enabled, refresh]);

    // A name given elsewhere (the bottom-left menu, People) closes the name
    // step: look again as soon as the stored name changes.
    const { profile } = useUserProfile();
    const selfName = profile?.display_name || profile?.name || '';
    const lastSelfNameRef = useRef(selfName);
    useEffect(() => {
        if (lastSelfNameRef.current === selfName) return;
        lastSelfNameRef.current = selfName;
        if (enabled) void refresh();
    }, [enabled, selfName, refresh]);

    const current = state?.next ? (state.steps.find((s) => s.key === state.next) ?? null) : null;

    const answer = useCallback(
        async (step: string, value: 'granted' | 'not_now' | 'done') => {
            requestRef.current++;
            const next = await AnswerMainChatSetup(step, value).catch(() => null);
            apply(next);
            return next;
        },
        [apply],
    );

    const grant = useCallback(
        async (step: MainChatSetupStep) => {
            // Contacts belongs to the Who you know consent sheet, including stale setup cards.
            if (step.key === 'contacts') return;
            if (busyRef.current) return;
            busyRef.current = true;
            setBusyKey(step.key);
            try {
                if (step.kind === 'permission') {
                    // The only place a macOS prompt can start: the user's tap.
                    const granted = await RequestPermission(step.key).catch(() => false);
                    if (granted) {
                        const next = await answer(step.key, 'granted');
                        // macOS said yes but the live check does not agree yet.
                        setWaitingKey(next?.next === step.key ? step.key : null);
                        return;
                    }
                    // A slow prompt can come back false while macOS still
                    // grants it; look again before sending the user away. Read
                    // directly so a superseding focus refresh cannot hide it.
                    requestRef.current++;
                    const after = await GetMainChatSetup(setupLanguage()).catch(() => null);
                    apply(after);
                    if (after && after.next !== step.key) {
                        setWaitingKey(null);
                        return;
                    }
                    // Some permissions are only turned on in System Settings.
                    await OpenPermissionSettings(step.key).catch(() => undefined);
                    setWaitingKey(step.key);
                    return;
                }
                // The assistant: Go decides whether it is ready.
                const next = await answer(step.key, 'granted');
                setWaitingKey(next?.next === step.key ? step.key : null);
            } finally {
                busyRef.current = false;
                setBusyKey(null);
            }
        },
        [answer, apply],
    );

    const saveName = useCallback(
        async (step: MainChatSetupStep, name: string, replace = false): Promise<SaveNameResult> => {
            const trimmed = name.trim();
            if (busyRef.current) return { kind: 'busy' };
            if (!trimmed) return { kind: 'failed' };
            busyRef.current = true;
            setBusyKey(step.key);
            try {
                // The name lives in the Who you know "self" profile; Go closes the
                // step once it finds it there. A different name stored since
                // the card appeared is not written over without asking.
                try {
                    const self = await ListPrincipalsByKind('self');
                    const stored = self?.[0]?.displayName?.trim() ?? '';
                    if (stored && stored !== trimmed && !replace) {
                        return { kind: 'stored', name: stored };
                    }
                    if (stored !== trimmed) {
                        await EnsureSelfProfile(trimmed, []);
                        refreshUserProfile();
                    }
                } catch (err) {
                    console.error('[main-chat-setup] save name failed:', err);
                    return { kind: 'failed' };
                }
                // The name is stored. Closing the step is separate: when the
                // answer does not get through, the next read closes it.
                if ((await answer(step.key, 'granted')) === null) void refresh();
                return { kind: 'saved' };
            } finally {
                busyRef.current = false;
                setBusyKey(null);
            }
        },
        [answer, refresh],
    );

    const notNow = useCallback(
        async (step: MainChatSetupStep) => {
            setWaitingKey(null);
            await answer(step.key, 'not_now');
        },
        [answer],
    );

    const openSettings = useCallback(async (step: MainChatSetupStep) => {
        if (step.key === 'contacts') return;
        await OpenPermissionSettings(step.key).catch(() => undefined);
        setWaitingKey(step.key);
    }, []);

    const asked = state?.steps.filter((s) => s.asked || s.status === 'pending') ?? [];
    const total = asked.length;
    const position = current ? asked.findIndex((s) => s.key === current.key) + 1 : 0;

    return {
        state,
        current,
        position,
        total,
        busyKey,
        waitingKey,
        grant,
        saveName,
        notNow,
        openSettings,
        refresh,
    };
}
