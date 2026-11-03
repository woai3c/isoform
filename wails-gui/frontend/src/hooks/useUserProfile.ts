import { useEffect, useState } from 'react';
import { GetUserProfile } from '../../wailsjs/go/main/App';
import type { main } from '../../wailsjs/go/models';

/** Module-level cache so every consumer shares the same data. */
let cachedProfile: main.UserProfile | null = null;
let fetchPromise: Promise<main.UserProfile | null> | null = null;
const profileListeners = new Set<(p: main.UserProfile | null) => void>();

/**
 * Re-fetch the profile and notify all mounted consumers. Call after the local
 * identity changes — e.g. claiming the Who you know "self" profile, which in
 * standalone becomes the displayed name (see Go GetUserProfile fallback).
 */
export function refreshUserProfile(): void {
    cachedProfile = null;
    fetchPromise = null;
    fetchProfile().then((p) => {
        profileListeners.forEach((notify) => notify(p));
    });
}

function fetchProfile(): Promise<main.UserProfile | null> {
    if (!fetchPromise) {
        fetchPromise = GetUserProfile()
            .then((profile) => {
                cachedProfile = profile;
                return profile;
            })
            .catch((err) => {
                console.error('Failed to fetch profile:', err);
                fetchPromise = null; // allow retry on error
                return null;
            });
    }
    return fetchPromise;
}

/**
 * Shared hook that fetches the user profile once and caches it at module level.
 * All consumers receive the same data without duplicate API calls.
 */
export function useUserProfile() {
    const [profile, setProfile] = useState<main.UserProfile | null>(cachedProfile);
    const [loading, setLoading] = useState(cachedProfile === null);

    useEffect(() => {
        let mounted = true;
        // Re-render when the profile is refreshed elsewhere (e.g. after the
        // user claims their People "self" identity or renames themselves).
        // Subscribe even when the cache is warm: a consumer that mounts after
        // the first fetch must still see a later rename.
        const notify = (p: main.UserProfile | null) => {
            if (mounted) {
                setProfile(p);
                setLoading(false);
            }
        };
        profileListeners.add(notify);
        if (cachedProfile) {
            setProfile(cachedProfile);
            setLoading(false);
        } else {
            fetchProfile().then((result) => {
                if (mounted) {
                    setProfile(result);
                    setLoading(false);
                }
            });
        }
        return () => {
            mounted = false;
            profileListeners.delete(notify);
        };
    }, []);

    return { profile, loading };
}

/** Clear the cache (e.g. on logout). */
export function clearUserProfileCache() {
    cachedProfile = null;
    fetchPromise = null;
}
