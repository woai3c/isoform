import { useState, useEffect, useCallback } from 'react';
import { GetPRInfo, BuildPRCreateURL } from '../../wailsjs/go/main/App';
import type { PRInfo } from '@/types';

interface UsePRInfoDeps {
    projectPath?: string;
    remoteUrl?: string;
    branch?: string;
    baseBranch?: string;
    enabled: boolean;
}

interface UsePRInfoResult {
    prInfo: PRInfo | null;
    loading: boolean;
    createPRUrl: string | null;
    refresh: () => void;
}

export function usePRInfo({
    projectPath,
    remoteUrl,
    branch,
    baseBranch,
    enabled,
}: UsePRInfoDeps): UsePRInfoResult {
    const [prInfo, setPrInfo] = useState<PRInfo | null>(null);
    const [loading, setLoading] = useState(false);
    const [createPRUrl, setCreatePRUrl] = useState<string | null>(null);

    const refresh = useCallback(() => {
        if (!enabled || !projectPath || !branch) {
            setPrInfo(null);
            setCreatePRUrl(null);
            return;
        }

        setLoading(true);

        const prPromise = GetPRInfo(projectPath, branch)
            .then((info) => setPrInfo(info as PRInfo | null))
            .catch(() => setPrInfo(null));

        const urlPromise = remoteUrl
            ? BuildPRCreateURL(remoteUrl, branch, baseBranch || 'main')
                  .then((url) => setCreatePRUrl(url))
                  .catch(() => setCreatePRUrl(null))
            : Promise.resolve();

        Promise.all([prPromise, urlPromise]).finally(() => setLoading(false));
    }, [enabled, projectPath, branch, baseBranch, remoteUrl]);

    useEffect(() => {
        refresh();
    }, [refresh]);

    return { prInfo, loading, createPRUrl, refresh };
}
