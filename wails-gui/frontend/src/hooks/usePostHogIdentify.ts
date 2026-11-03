import { useEffect, useRef, useState } from 'react';
import posthog from 'posthog-js';
import { GetWebBaseURL, GetVersion, GetTelemetryTier } from '../../wailsjs/go/main/App';
import { EventsOn } from '../../wailsjs/runtime/runtime';

const READY_EVENT_NAME = 'client.telemetry.ready';
// Emitted by SetTelemetryTier when the user flips "Share diagnostic data".
const TIER_CHANGED_EVENT = 'telemetry:tier-changed';
const POSTHOG_STAGING_KEY = import.meta.env.VITE_PUBLIC_POSTHOG_STAGING_KEY ?? '';
const POSTHOG_STAGING_HOST = import.meta.env.VITE_PUBLIC_POSTHOG_STAGING_HOST ?? '';
const POSTHOG_PROD_KEY = import.meta.env.VITE_PUBLIC_POSTHOG_PROD_KEY ?? '';
const POSTHOG_PROD_HOST = import.meta.env.VITE_PUBLIC_POSTHOG_PROD_HOST ?? '';

export interface PostHogTelemetryStatus {
    isReady: boolean;
    environment: 'prod' | 'staging' | null;
    webBaseURL: string;
    appVersion: string;
}

function isStaging(webBaseURL: string): boolean {
    return webBaseURL.includes('staging');
}

function getPostHogConfig(webBaseURL: string) {
    if (isStaging(webBaseURL)) {
        return { key: POSTHOG_STAGING_KEY, host: POSTHOG_STAGING_HOST };
    }
    return { key: POSTHOG_PROD_KEY, host: POSTHOG_PROD_HOST };
}

/** Whether a stored telemetry tier lets anything be sent. */
function sharingAllowed(tier: string | null | undefined): boolean {
    return tier !== 'off';
}

let requestGeneration = 0;
let requestsAllowed = false;
let originalSendRequest: typeof posthog._send_request | null = null;

function installPostHogRequestGate() {
    if (originalSendRequest) return;
    originalSendRequest = posthog._send_request.bind(posthog);
    posthog._send_request = (request) => {
        if (!requestsAllowed) return;
        const generation = requestGeneration;
        originalSendRequest?.({
            ...request,
            callback: (response) => {
                // A failed in-flight request must not populate an old retry
                // queue after opt-out, even if sharing was enabled again.
                if (requestsAllowed && generation === requestGeneration) {
                    request.callback?.(response);
                }
            },
        });
    };
}

/**
 * Fully tears down the PostHog singleton so a subsequent `init()` with
 * different credentials is accepted. `posthog.reset()` alone only clears
 * identity; the internal `__loaded` flag stays `true` and causes `init()`
 * to silently no-op when called again with a new API key.
 */
function teardownPostHog() {
    requestsAllowed = false;
    requestGeneration += 1;
    try {
        posthog.reset();
    } catch {
        // ignore – may throw if never initialized
    }
    // Clear the internal "already initialized" guard so the next init() call
    // actually applies the new token / api_host. This is an internal flag but
    // has been stable across posthog-js versions and is the only way to
    // reinitialize the default singleton with different credentials.
    posthog.__loaded = false;
    // The SDK's opt-out/reset leave its batch and retry timers alive. Its
    // unload handler retires both queues; the closed transport gate above
    // discards their contents instead of sending them. These SDK internals
    // are covered against the installed bundle in the regression tests.
    posthog._handle_unload();
    posthog.__request_queue = [];
}

function getVisibilityState(): string {
    if (typeof document === 'undefined') {
        return 'unknown';
    }
    return document.visibilityState;
}

function getHasFocus(): boolean | null {
    if (typeof document === 'undefined' || typeof document.hasFocus !== 'function') {
        return null;
    }
    return document.hasFocus();
}

function timezoneName(): string {
    try {
        return Intl.DateTimeFormat().resolvedOptions().timeZone || '';
    } catch {
        return '';
    }
}

/**
 * Initializes PostHog after login based on the selected environment and
 * resets on logout. Events are anonymous (no identify).
 * Gracefully no-ops when PostHog credentials are not configured (local dev).
 * Sends nothing while "Share diagnostic data" is off: PostHog is never
 * initialized, and turning the setting off stops a running client.
 */
export function usePostHogIdentify(isAuthenticated: boolean): PostHogTelemetryStatus {
    const [status, setStatus] = useState<PostHogTelemetryStatus>({
        isReady: false,
        environment: null,
        webBaseURL: '',
        appVersion: '',
    });
    // null until the stored "Share diagnostic data" choice has loaded.
    const [sharing, setSharing] = useState<boolean | null>(null);
    const initializedEnvRef = useRef<string | null>(null);
    const readyEventRef = useRef<string | null>(null);

    useEffect(() => {
        let cancelled = false;
        let receivedTierChange = false;
        GetTelemetryTier()
            .then((tier: string) => {
                if (!cancelled && !receivedTierChange) setSharing(sharingAllowed(tier));
            })
            // Without a readable choice nothing is sent.
            .catch(() => {
                if (!cancelled && !receivedTierChange) setSharing(false);
            });
        const unsubscribe = EventsOn(TIER_CHANGED_EVENT, (tier: string) => {
            receivedTierChange = true;
            setSharing(sharingAllowed(tier));
        });
        return () => {
            cancelled = true;
            unsubscribe?.();
        };
    }, []);

    useEffect(() => {
        if (!isAuthenticated || sharing !== true) {
            if (initializedEnvRef.current) {
                // Stop autocapture, exception capture and queued events too,
                // not only the identity.
                try {
                    posthog.opt_out_capturing();
                } catch {
                    // ignore – may throw if never initialized
                }
                teardownPostHog();
                initializedEnvRef.current = null;
                readyEventRef.current = null;
            }
            setStatus({
                isReady: false,
                environment: null,
                webBaseURL: '',
                appVersion: '',
            });
            return;
        }

        let cancelled = false;

        Promise.all([
            GetWebBaseURL(),
            GetVersion().catch(() => ''),
        ])
            .then(([webBaseURL, appVersion]) => {
                if (cancelled) return;
                const { key, host } = getPostHogConfig(webBaseURL);
                if (!key || !host) return;

                const env = isStaging(webBaseURL) ? 'staging' : 'prod';

                if (initializedEnvRef.current === env) {
                    setStatus({
                        isReady: true,
                        environment: env,
                        webBaseURL,
                        appVersion: appVersion || '',
                    });
                    return;
                }

                if (initializedEnvRef.current) {
                    teardownPostHog();
                    readyEventRef.current = null;
                }

                installPostHogRequestGate();
                let anonymousIdentityReady = false;
                posthog.init(key, {
                    api_host: host,
                    // Extensions initialize before loaded(). Block their
                    // captures and requests until legacy identity is gone.
                    capture_exceptions: false,
                    disable_external_dependency_loading: true,
                    advanced_disable_flags: true,
                    opt_out_capturing_by_default: true,
                    before_send: (event) => anonymousIdentityReady ? event : null,
                    loaded: (client) => {
                        // reset(true) clears local/session properties,
                        // including old distinct_id/$user_id, and rotates the
                        // device id before queues or remote config can start.
                        client.reset(true);
                        anonymousIdentityReady = true;
                        requestsAllowed = true;
                        client.set_config({
                            capture_exceptions: true,
                            disable_external_dependency_loading: false,
                        });
                    },
                    // Only the named events below and errors: no click,
                    // page view or session capture, which would carry what
                    // is on screen.
                    autocapture: false,
                    capture_pageview: false,
                    capture_pageleave: false,
                    disable_session_recording: true,
                    debug: import.meta.env.DEV,
                });
                // The SDK catches loaded() errors. Stay closed if clearing
                // identity failed instead of reporting telemetry as ready.
                if (!anonymousIdentityReady) return;
                // reset() clears SDK consent; only the stored app preference
                // above authorizes capturing again, without an opt-in event.
                if (posthog.has_opted_out_capturing()) {
                    posthog.opt_in_capturing({ captureEventName: false });
                }

                if (appVersion) {
                    posthog.register({ app_version: appVersion });
                }

                initializedEnvRef.current = env;
                setStatus({
                    isReady: true,
                    environment: env,
                    webBaseURL,
                    appVersion: appVersion || '',
                });
            })
            .catch(() => {});

        return () => {
            cancelled = true;
        };
    }, [isAuthenticated, sharing]);

    useEffect(() => {
        // Initialization clears legacy identity before this effect can run;
        // no profile name or email is attached to these anonymous events.
        if (!isAuthenticated || !status.isReady || !status.environment) return;

        const readyEventKey = `${status.environment}:${status.appVersion}`;
        if (readyEventRef.current === readyEventKey) return;

        posthog.capture(READY_EVENT_NAME, {
            client_type: 'desktop',
            source: 'wails-gui-frontend',
            environment: status.environment,
            app_version: status.appVersion,
            web_base_url: status.webBaseURL,
            visibility_state: getVisibilityState(),
            has_focus: getHasFocus(),
            timezone: timezoneName(),
        });
        readyEventRef.current = readyEventKey;
    }, [isAuthenticated, status]);

    return status;
}
