import { useEffect, useRef, type ReactNode } from "react";
import { ArrowLeft } from "lucide-react";

export function debugDetailOwnsEscape(): boolean {
    const activeElement = document.activeElement;
    return (
        activeElement instanceof Element &&
        activeElement.closest('[data-debug-detail-open="true"]') !== null
    );
}

export function DebugMasterDetail({
    list,
    detail,
    detailKey,
    compactDetailOpen,
    onBack,
    listLabel,
    detailLabel,
    minHeightClassName = "min-h-[560px]",
}: {
    list: ReactNode;
    detail: ReactNode;
    detailKey?: string | null;
    compactDetailOpen: boolean;
    onBack: () => void;
    listLabel: string;
    detailLabel: string;
    minHeightClassName?: string;
}) {
    const detailRef = useRef<HTMLDivElement>(null);

    useEffect(() => {
        if (detailRef.current) {
            detailRef.current.scrollTop = 0;
        }
    }, [detailKey]);

    return (
        <div
            className={`grid grid-cols-1 xl:grid-cols-[minmax(250px,0.78fr)_minmax(0,1.5fr)] ${minHeightClassName}`}
            data-testid="debug-master-detail"
        >
            <div
                aria-label={listLabel}
                className={`max-h-[680px] overflow-auto xl:border-r xl:border-border ${
                    compactDetailOpen ? "hidden xl:block" : "block"
                }`}
                data-testid="debug-master-list"
                role="region"
            >
                {list}
            </div>
            <div
                ref={detailRef}
                aria-label={detailLabel}
                className={`min-w-0 max-h-[680px] overflow-auto ${
                    compactDetailOpen ? "block" : "hidden xl:block"
                }`}
                data-debug-detail-open={compactDetailOpen ? "true" : undefined}
                data-testid="debug-master-detail-panel"
                onKeyDown={(event) => {
                    if (event.key === "Escape" && compactDetailOpen) {
                        event.preventDefault();
                        event.stopPropagation();
                        onBack();
                    }
                }}
                role="region"
            >
                {compactDetailOpen && (
                    <div className="sticky top-0 z-10 border-b border-border bg-panel px-2 py-2 xl:hidden">
                        <button
                            type="button"
                            autoFocus
                            className="inline-flex h-8 items-center gap-1.5 px-2 text-xs font-medium text-ink hover:bg-ink/[0.04]"
                            onClick={onBack}
                        >
                            <ArrowLeft aria-hidden="true" className="h-3.5 w-3.5" />
                            Back to list
                        </button>
                    </div>
                )}
                <div className="p-4">{detail}</div>
            </div>
        </div>
    );
}
