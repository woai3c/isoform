import { act, cleanup, render } from '@testing-library/react';
import type { CaptureResult, PostHog } from 'posthog-js';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const listeners = vi.hoisted(() => new Map<string, (tier: string) => void>());

vi.mock('@/lib/wails-shim-app', () => ({
    IdeaMeaningGroups: () => Promise.resolve(null),
    GetWebBaseURL: () => Promise.resolve('https://app.example.com'),
    GetVersion: () => Promise.resolve('1.2.3'),
    GetTelemetryTier: () => Promise.resolve('detailed'),
}));

vi.mock('../../wailsjs/runtime/runtime', () => ({
    EventsOn: (event: string, callback: (tier: string) => void) => {
        listeners.set(event, callback);
        return () => listeners.delete(event);
    },
}));

describe('anonymous PostHog initialization with the installed SDK', () => {
    let client: PostHog;

    beforeEach(async () => {
        vi.resetModules();
        listeners.clear();
        vi.useFakeTimers();
        vi.stubEnv('VITE_PUBLIC_POSTHOG_PROD_KEY', 'phc_identity_regression');
        vi.stubEnv('VITE_PUBLIC_POSTHOG_PROD_HOST', 'https://posthog.example.com');
        localStorage.clear();
        sessionStorage.clear();
        const actual = await vi.importActual<typeof import('posthog-js')>('posthog-js');
        client = new actual.PostHog();
        vi.doMock('posthog-js', () => ({ default: client }));
    });

    afterEach(() => {
        cleanup();
        client.set_config({ capture_exceptions: false });
        client.opt_out_capturing();
        client.reset(true);
        localStorage.clear();
        sessionStorage.clear();
        document.cookie = 'ph_phc_identity_regression_posthog=; Max-Age=0; path=/';
        document.querySelectorAll('script[src*="posthog.example.com"]').forEach((script) => script.remove());
        vi.clearAllTimers();
        vi.useRealTimers();
        vi.restoreAllMocks();
        vi.doUnmock('posthog-js');
        vi.unstubAllEnvs();
    });

    it('never attaches legacy local, cookie or session identity to captured events', async () => {
        const legacyIdentity = 'Legacy User <legacy@example.com>';
        const stored = JSON.stringify({
            distinct_id: legacyIdentity,
            $user_id: legacyIdentity,
            $device_id: 'legacy-device',
            $user_state: 'identified',
            $stored_person_properties: { name: 'Legacy User', email: 'legacy@example.com' },
        });
        const storageKey = 'ph_phc_identity_regression_posthog';
        localStorage.setItem(storageKey, stored);
        sessionStorage.setItem(storageKey, stored);
        document.cookie = `${storageKey}=${encodeURIComponent(stored)}; path=/`;
        // An earlier version may have explicitly opted in, so the init
        // default alone cannot prevent early SDK captures.
        localStorage.setItem('__ph_opt_in_out_phc_identity_regression', '1');

        const captured: CaptureResult[] = [];
        client.on('eventCaptured', (event: CaptureResult) => captured.push(event));
        // Exercise the real persistence, reset and payload assembly locally;
        // intercept only the transport so this unit test sends no telemetry.
        const send = vi.spyOn(client, '_send_request').mockImplementation(() => {});
        const reset = vi.spyOn(client, 'reset');
        const init = client.init.bind(client);
        let identityAtLoaded: unknown;
        let earlyCapture: CaptureResult | undefined;
        vi.spyOn(client, 'init').mockImplementation((key, config, name) => init(key, {
            ...config,
            debug: false,
            request_batching: false,
            loaded: (sdk) => {
                identityAtLoaded = sdk.get_distinct_id();
                earlyCapture = sdk.capture('startup.before-reset', {}, { send_instantly: true });
                config?.loaded?.(sdk);
            },
        }, name));

        const { usePostHogIdentify } = await import('./usePostHogIdentify');
        function Harness() {
            usePostHogIdentify(true);
            return null;
        }
        render(<Harness />);
        await act(async () => {
            await Promise.resolve();
            await Promise.resolve();
            await Promise.resolve();
        });
        client.capture('after-upgrade', {}, { send_instantly: true });

        expect(identityAtLoaded).toBe(legacyIdentity);
        expect(earlyCapture).toBeUndefined();
        expect(reset).toHaveBeenCalledWith(true);
        expect(captured.map((event) => event.event)).toEqual(['client.telemetry.ready', 'after-upgrade']);
        expect(send).toHaveBeenCalledTimes(2);
        expect(reset.mock.invocationCallOrder[0]).toBeLessThan(send.mock.invocationCallOrder[0]);
        for (const event of captured) {
            expect(event.properties.distinct_id).toBeTruthy();
            expect(event.properties.distinct_id).not.toBe(legacyIdentity);
            expect(event.properties.$user_id).toBeUndefined();
            expect(event.properties.$device_id).not.toBe('legacy-device');
        }
        const serialized = JSON.stringify({ captured, requests: send.mock.calls });
        expect(serialized).not.toContain('Legacy User');
        expect(serialized).not.toContain('legacy@example.com');
        expect(localStorage.getItem(storageKey)).not.toContain('legacy@example.com');
        expect(sessionStorage.getItem(storageKey)).not.toContain('legacy@example.com');
    });

    it('discards queued events and retries across an opt-out/on cycle', async () => {
        const send = vi.spyOn(client, '_send_request').mockImplementation(() => {});
        const init = client.init.bind(client);
        vi.spyOn(client, 'init').mockImplementation((key, config, name) => init(key, {
            ...config,
            debug: false,
        }, name));
        const { usePostHogIdentify } = await import('./usePostHogIdentify');
        function Harness() {
            usePostHogIdentify(true);
            return null;
        }
        render(<Harness />);
        await act(async () => {
            await Promise.resolve();
            await Promise.resolve();
            await Promise.resolve();
        });

        client.capture('queued-before-opt-out');
        client.capture('retry-before-opt-out', {}, { send_instantly: true });
        expect(send).toHaveBeenCalledTimes(1);
        send.mock.calls[0][0].callback?.({ statusCode: 503 });
        client.capture('in-flight-before-opt-out', {}, { send_instantly: true });
        expect(send).toHaveBeenCalledTimes(2);
        const lateFailure = send.mock.calls[1][0].callback;

        await act(async () => {
            listeners.get('telemetry:tier-changed')?.('off');
        });
        expect(send).toHaveBeenCalledTimes(2);
        expect(client.__loaded).toBe(false);

        await act(async () => {
            listeners.get('telemetry:tier-changed')?.('detailed');
            await Promise.resolve();
            await Promise.resolve();
        });
        // An old HTTP failure arriving after re-enable must not restart its
        // retired retry queue. Then exercise timed flush, online and unload.
        lateFailure?.({ statusCode: 503 });
        await act(async () => {
            vi.advanceTimersByTime(12000);
            window.dispatchEvent(new Event('online'));
            window.dispatchEvent(new Event('pagehide'));
        });

        expect(send).toHaveBeenCalledTimes(3);
        const sentAfterOptOut = JSON.stringify(send.mock.calls.slice(2));
        expect(sentAfterOptOut).toContain('client.telemetry.ready');
        expect(sentAfterOptOut).not.toContain('queued-before-opt-out');
        expect(sentAfterOptOut).not.toContain('retry-before-opt-out');
        expect(sentAfterOptOut).not.toContain('in-flight-before-opt-out');
    });
});
