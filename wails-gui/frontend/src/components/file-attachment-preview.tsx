import { FileArchive, FileCode, FileSpreadsheet, FileText, File, Presentation, X } from "lucide-react";
import { useTranslation } from "react-i18next";
import { cn } from "@/lib/utils";
import type { FileAttachment, FileCategory } from "@/types";

function getFileIcon(category: FileCategory) {
    switch (category) {
        case "document":
            return FileText;
        case "spreadsheet":
            return FileSpreadsheet;
        case "presentation":
            return Presentation;
        case "code":
            return FileCode;
        case "archive":
            return FileArchive;
        default:
            return File;
    }
}

function formatFileSize(bytes: number): string {
    if (bytes >= 1024 * 1024) {
        return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
    }
    if (bytes >= 1024) {
        return `${(bytes / 1024).toFixed(0)} KB`;
    }
    return `${bytes} B`;
}

interface FileAttachmentChipProps {
    file: FileAttachment;
    onRemove?: () => void;
    compact?: boolean;
}

export function FileAttachmentChip({
    file,
    onRemove,
    compact = false,
}: FileAttachmentChipProps) {
    const { t } = useTranslation("app");
    const Icon = getFileIcon(file.category);

    return (
        <div
            className={cn(
                "group flex items-center gap-1.5 rounded-lg border border-border bg-panel",
                compact ? "px-2 py-1" : "px-2.5 py-1.5",
            )}
        >
            <Icon
                className={cn(
                    "shrink-0 text-muted",
                    compact ? "size-3.5" : "size-4",
                )}
            />
            <div className="min-w-0 flex-1">
                <span
                    className={cn(
                        "block truncate text-ink",
                        compact ? "text-[11px]" : "text-xs",
                    )}
                    title={file.fileName}
                >
                    {file.fileName}
                </span>
                <span
                    className={cn(
                        "text-muted",
                        compact ? "text-[10px]" : "text-[11px]",
                    )}
                >
                    {formatFileSize(file.fileSize)}
                </span>
            </div>
            {onRemove && (
                <button
                    type="button"
                    aria-label={t("composer.attachments.removeFile", { file: file.fileName })}
                    className="shrink-0 rounded p-0.5 text-muted opacity-0 transition-opacity hover:text-ink group-hover:opacity-100 focus-visible:opacity-100 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-border-focus cursor-pointer"
                    onClick={(e) => {
                        e.preventDefault();
                        e.stopPropagation();
                        onRemove();
                    }}
                >
                    <X className="size-3" />
                </button>
            )}
        </div>
    );
}

interface FileAttachmentListProps {
    files: FileAttachment[];
    onRemove?: (id: string) => void;
    compact?: boolean;
    className?: string;
}

export function FileAttachmentList({
    files,
    onRemove,
    compact = false,
    className,
}: FileAttachmentListProps) {
    if (!files || files.length === 0) return null;
    return (
        <div className={cn("flex flex-wrap gap-1.5", className)}>
            {files.map((f) => (
                <FileAttachmentChip
                    key={f.id}
                    file={f}
                    onRemove={onRemove ? () => onRemove(f.id) : undefined}
                    compact={compact}
                />
            ))}
        </div>
    );
}
