import { useState, useMemo, useEffect } from 'react';
import { Link } from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { GetWorktreeSettings } from '../../wailsjs/go/main/App';

interface AddProjectURLProps {
    onCloneURL: (url: string) => void;
}

function repoNameFromURL(url: string): string {
    const stripped = url.replace(/\.git$/, '').replace(/\/$/, '');
    const lastSlash = stripped.lastIndexOf('/');
    const lastColon = stripped.lastIndexOf(':');
    const idx = Math.max(lastSlash, lastColon);
    return idx >= 0 ? stripped.substring(idx + 1) : stripped;
}

export function AddProjectURL({ onCloneURL }: AddProjectURLProps) {
    const { t } = useTranslation('app');
    const [url, setUrl] = useState('');
    const [repoRoot, setRepoRoot] = useState('');
    const [rootFailed, setRootFailed] = useState(false);

    useEffect(() => {
        GetWorktreeSettings()
            .then((s) => {
                if (s.repoRoot) setRepoRoot(s.repoRoot);
                else setRootFailed(true);
            })
            .catch(() => setRootFailed(true));
    }, []);

    const repoName = useMemo(() => {
        const trimmed = url.trim();
        if (!trimmed) return '';
        return repoNameFromURL(trimmed);
    }, [url]);

    const handleClone = () => {
        const trimmed = url.trim();
        if (!trimmed || !repoRoot) return;
        void onCloneURL(trimmed);
    };

    const handleKeyDown = (e: React.KeyboardEvent) => {
        if (e.key === 'Enter' && url.trim()) {
            handleClone();
        }
    };

    return (
        <div className="flex flex-col gap-4 p-6 pt-5">
            <div className="flex flex-col gap-1.5">
                <label className="text-xs font-semibold text-muted uppercase tracking-wider">
                    {t('addProjectURL.repositoryURL')}
                </label>
                <div className="flex items-center gap-2 rounded-lg border border-border bg-panel px-2.5 transition-colors focus-within:border-accent-2/40">
                    <Link className="h-3.5 w-3.5 shrink-0 opacity-40" />
                    <input
                        type="text"
                        value={url}
                        onChange={(e) => setUrl(e.target.value)}
                        onKeyDown={handleKeyDown}
                        placeholder={t('addProjectURL.placeholder')}
                        className="flex h-9 w-full bg-transparent text-[13px] text-ink outline-none placeholder:text-muted"
                    />
                </div>
            </div>

            {repoName && repoRoot && (
                <p className="m-0 text-xs text-muted">
                    {t('addProjectURL.cloneTo')} <span className="font-mono text-ink">{repoRoot}/{repoName}</span>
                </p>
            )}
            {!repoRoot && (
                <p role={rootFailed ? 'alert' : 'status'} className="m-0 text-xs text-muted">
                    {t(rootFailed ? 'addProjectURL.rootFailed' : 'addProjectURL.loadingRoot')}
                </p>
            )}

            <button
                className="self-end px-3.5 py-1.5 rounded-lg border border-[var(--color-sample-border)] bg-background text-[var(--color-sample-text)] text-xs font-semibold cursor-pointer whitespace-nowrap transition-all hover:bg-[var(--color-sample-text)]/[0.06] disabled:opacity-50 disabled:cursor-not-allowed"
                onClick={handleClone}
                disabled={!url.trim() || !repoRoot}
            >
                {t('addProjectURL.clone')}
            </button>
        </div>
    );
}
