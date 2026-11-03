import { useEffect, useRef, useState } from 'react';
import { FolderOpen } from 'lucide-react';
import { OnFileDrop, OnFileDropOff } from '../../wailsjs/runtime/runtime';
import type { Project } from '@/types';

interface AddProjectLocalProps {
    onOpenLocal: () => Promise<Project | null>;
    onOpenPath: (path: string) => Promise<void>;
    onClose: () => void;
}

export function AddProjectLocal({ onOpenLocal, onOpenPath, onClose }: AddProjectLocalProps) {
    const [loading, setLoading] = useState(false);
    const [dragging, setDragging] = useState(false);
    const dropRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        OnFileDrop((_x, _y, paths) => {
            setDragging(false);
            if (paths.length > 0) {
                setLoading(true);
                onOpenPath(paths[0])
                    .then(() => onClose())
                    .catch(() => {})
                    .finally(() => setLoading(false));
            }
        }, true);

        return () => {
            OnFileDropOff();
        };
    }, [onOpenPath, onClose]);

    const handleSelect = async () => {
        setLoading(true);
        try {
            const result = await onOpenLocal();
            if (result) {
                onClose();
            }
        } finally {
            setLoading(false);
        }
    };

    return (
        <div className="flex flex-col gap-4 p-6 pt-5">
            <p className="m-0 text-[13px] text-muted">
                Open an existing local project directory.
            </p>

            <div
                ref={dropRef}
                className={`flex flex-col items-center justify-center gap-3 rounded-xl border-2 border-dashed px-6 py-10 cursor-pointer transition-all disabled:opacity-50 disabled:cursor-not-allowed ${
                    dragging
                        ? 'border-accent-2 bg-accent-2/[0.06]'
                        : 'border-border bg-panel-warm hover:border-accent-2/40 hover:bg-accent-2/[0.03]'
                }`}
                style={{ '--wails-drop-target': 'drop' } as React.CSSProperties}
                onClick={loading ? undefined : handleSelect}
                onDragOver={() => setDragging(true)}
                onDragLeave={() => setDragging(false)}
            >
                <FolderOpen className={`h-8 w-8 ${dragging ? 'text-accent-2' : 'text-muted'}`} />
                <span className="text-[13px] font-semibold text-ink">
                    {loading
                        ? 'Opening...'
                        : dragging
                          ? 'Drop folder here'
                          : 'Select or drop a directory...'}
                </span>
            </div>
        </div>
    );
}
