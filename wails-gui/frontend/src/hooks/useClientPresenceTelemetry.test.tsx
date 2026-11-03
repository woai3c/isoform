import { act, render } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
    splitPresenceSlices,
    useClientPresenceTelemetry,
} from './useClientPresenceTelemetry';
import posthog from 'posthog-js';

vi.mock('posthog-js', () => ({
    default: {
        __loaded: true,
        capture: vi.fn(),
    },
}));

function PresenceHarness({
    authenticated,
    telemetryReady = true,
}: {
    authenticated: boolean;
    telemetryReady?: boolean;
}) {
    useClientPresenceTelemetry(authenticated, telemetryReady);
    return null;
}

function setVisibilityState(value: DocumentVisibilityState) {
    Object.defineProperty(document, 'visibilityState', {
        configurable: true,
        value,
    });
}

describe('presence telemetry helpers', () => {
    it('splits duration slices at local midnight', () => {
        const start = new Date(2026, 0, 1, 23, 59, 30).getTime();
        const end = new Date(2026, 0, 2, 0, 0, 30).getTime();

        const slices = splitPresenceSlices(start, end);

        expect(slices).toHaveLength(2);
        expect(slices[0]).toMatchObject({
            startMs: start,
            endMs: new Date(2026, 0, 2, 0, 0, 0).getTime(),
            localDate: '2026-01-01',
        });
        expect(slices[1]).toMatchObject({
            startMs: new Date(2026, 0, 2, 0, 0, 0).getTime(),
            endMs: end,
            localDate: '2026-01-02',
        });
    });
});

describe('useClientPresenceTelemetry', () => {
    beforeEach(() => {
        vi.useFakeTimers();
        vi.setSystemTime(new Date(2026, 0, 1, 9, 0, 0));
        setVisibilityState('visible');
        vi.spyOn(document, 'hasFocus').mockReturnValue(true);
        (posthog as { __loaded?: boolean }).__loaded = true;
        vi.mocked(posthog.capture).mockClear();
    });

    afterEach(() => {
        vi.restoreAllMocks();
        vi.useRealTimers();
    });

    it('records foreground duration shortly after telemetry becomes ready', () => {
        render(<PresenceHarness authenticated />);

        act(() => {
            vi.advanceTimersByTime(5_000);
        });

        expect(posthog.capture).toHaveBeenCalledTimes(1);
        expect(posthog.capture).toHaveBeenCalledWith(
            'client.presence.duration_recorded',
            expect.objectContaining({
                client_type: 'desktop',
                state: 'foreground',
                duration_ms: 5_000,
                local_date: '2026-01-01',
                flush_reason: 'startup',
                gap_detected: false,
            })
        );
    });

    it('continues recording foreground duration on the interval', () => {
        render(<PresenceHarness authenticated />);

        act(() => {
            vi.advanceTimersByTime(60_000);
        });

        expect(posthog.capture).toHaveBeenCalledTimes(2);
        expect(posthog.capture).toHaveBeenLastCalledWith(
            'client.presence.duration_recorded',
            expect.objectContaining({
                state: 'foreground',
                duration_ms: 55_000,
                flush_reason: 'interval',
            })
        );
    });

    it('flushes the previous state when the app moves to background', () => {
        render(<PresenceHarness authenticated />);

        act(() => {
            vi.advanceTimersByTime(15_000);
            vi.mocked(document.hasFocus).mockReturnValue(false);
            window.dispatchEvent(new Event('blur'));
        });

        expect(posthog.capture).toHaveBeenCalledTimes(2);
        expect(posthog.capture).toHaveBeenLastCalledWith(
            'client.presence.duration_recorded',
            expect.objectContaining({
                state: 'foreground',
                duration_ms: 10_000,
                flush_reason: 'state_change',
            })
        );

        act(() => {
            vi.advanceTimersByTime(60_000);
        });

        expect(posthog.capture).toHaveBeenCalledTimes(3);
        expect(posthog.capture).toHaveBeenLastCalledWith(
            'client.presence.duration_recorded',
            expect.objectContaining({
                state: 'background',
                duration_ms: 45_000,
                flush_reason: 'interval',
            })
        );
    });

    it('keeps the current slice open until PostHog is initialized', () => {
        vi.mocked(posthog.capture).mockClear();
        (posthog as { __loaded?: boolean }).__loaded = false;
        render(<PresenceHarness authenticated />);

        act(() => {
            vi.advanceTimersByTime(60_000);
        });

        expect(posthog.capture).not.toHaveBeenCalled();

        (posthog as { __loaded?: boolean }).__loaded = true;
        act(() => {
            vi.advanceTimersByTime(60_000);
        });

        expect(posthog.capture).toHaveBeenCalledTimes(1);
        expect(posthog.capture).toHaveBeenCalledWith(
            'client.presence.duration_recorded',
            expect.objectContaining({
                state: 'foreground',
                duration_ms: 120_000,
                flush_reason: 'interval',
            })
        );
    });

    it('waits for PostHog telemetry readiness before starting the first slice', () => {
        const { rerender } = render(<PresenceHarness authenticated telemetryReady={false} />);

        act(() => {
            vi.advanceTimersByTime(60_000);
        });

        expect(posthog.capture).not.toHaveBeenCalled();

        rerender(<PresenceHarness authenticated telemetryReady />);

        act(() => {
            vi.advanceTimersByTime(5_000);
        });

        expect(posthog.capture).toHaveBeenCalledTimes(1);
        expect(posthog.capture).toHaveBeenCalledWith(
            'client.presence.duration_recorded',
            expect.objectContaining({
                state: 'foreground',
                duration_ms: 5_000,
                flush_reason: 'startup',
            })
        );
    });
});
