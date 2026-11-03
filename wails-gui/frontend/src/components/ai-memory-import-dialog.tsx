import { useCallback, useEffect, useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { CheckCircle2, ChevronDown, ChevronRight, Copy, Download, FileText, Loader2 } from 'lucide-react';
import { Button } from '@/components/ui/button';
import {
    Dialog,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogDescription,
    DialogCloseButton,
} from '@/components/ui/dialog';
import { ChatGPTIcon, ClaudeIcon } from '@/components/icons/app-icons';
import { ImportAIMemories, ImportAIMemoryText, ScanAIMemories } from '../../wailsjs/go/main/App';
import type { main } from '../../wailsjs/go/models';

const SOURCE_ICON: Record<string, React.FC<{ className?: string }>> = {
    'claude-code': ClaudeIcon,
};

const MEMORY_EXTRACTION_PROMPT = `Export all of my stored memories and any context you've learned about me from past conversations. Preserve my words verbatim where possible, especially for instructions and preferences.

## Categories (output in this order):

1. **Instructions**: Rules I've explicitly asked you to follow going forward — tone, format, style, "always do X", "never do Y", and corrections to your behavior. Only include rules from stored memories, not from conversations.

2. **Identity**: Name, age, location, education, family, relationships, languages, and personal interests.

3. **Career**: Current and past roles, companies, and general skill areas.

4. **Projects**: Projects I meaningfully built or committed to. Ideally ONE entry per project. Include what it does, current status, and any key decisions. Use the project name or a short descriptor as the first words of the entry.

5. **Preferences**: Opinions, tastes, and working-style preferences that apply broadly.

## Format:

Use section headers for each category. Within each category, list one entry per line, sorted by oldest date first. Format each line as:

[YYYY-MM-DD] - Entry content here.

If no date is known, use [unknown] instead.

## Output:
- Wrap the entire export in a single code block for easy copying.
- After the code block, state whether this is the complete set or if more remain.`;

// ── Prompt-based import providers ──

interface PromptProvider {
    id: string;
    translationKey: string;
    name: string;
    icon: React.FC<{ className?: string }>;
}

const PROMPT_PROVIDERS: PromptProvider[] = [
    {
        id: 'chatgpt',
        translationKey: 'chatgpt',
        name: 'ChatGPT',
        icon: ChatGPTIcon,
    },
    {
        id: 'claude-desktop',
        translationKey: 'claudeDesktop',
        name: 'Claude Desktop',
        icon: ClaudeIcon,
    },
];

// ── Helpers ──

function formatBytes(bytes: number): string {
    if (bytes < 1024) return `${bytes} B`;
    if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
    return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

// ── Sub-component for a single prompt-based provider ──

function PromptImportCard({
    provider,
    onImported,
}: {
    provider: PromptProvider;
    onImported?: () => void;
}) {
    const { t } = useTranslation('settings');
    const providerName = t(`aiMemoryImportDialog.providers.${provider.translationKey}.name` as never) as string;
    const providerDescription = t(`aiMemoryImportDialog.providers.${provider.translationKey}.description` as never) as string;
    const [step, setStep] = useState<'idle' | 'paste'>('idle');
    const [pastedText, setPastedText] = useState('');
    const [importing, setImporting] = useState(false);
    const [result, setResult] = useState<main.AIMemoryImportResult | null>(null);
    const [copied, setCopied] = useState(false);
    const [promptExpanded, setPromptExpanded] = useState(false);

    const handleCopy = useCallback(async () => {
        await navigator.clipboard.writeText(MEMORY_EXTRACTION_PROMPT);
        setCopied(true);
        setTimeout(() => setCopied(false), 2000);
    }, []);

    const handleImport = useCallback(async () => {
        const text = pastedText.trim();
        if (!text) return;
        setImporting(true);
        setResult(null);
        try {
            const r = await ImportAIMemoryText(provider.name, text);
            setResult(r);
            if (r.imported > 0) {
                onImported?.();
            }
        } catch (err: unknown) {
            setResult({
                imported: 0,
                skipped: 0,
                errors: [String(err)],
            } as main.AIMemoryImportResult);
        } finally {
            setImporting(false);
        }
    }, [pastedText, provider.name, onImported]);

    const Icon = provider.icon;

    return (
        <div className="flex flex-col gap-2">
            <div className="flex items-center gap-3 p-3 rounded-lg border border-dashed border-border bg-panel">
                <div className="w-8 h-8 rounded-md bg-background-accent/40 flex items-center justify-center shrink-0">
                    <Icon className="h-4 w-4" />
                </div>
                <div className="flex-1 min-w-0">
                    <span className="text-sm font-semibold text-ink">{providerName}</span>
                    <p className="text-xs text-muted mt-0.5">{providerDescription}</p>
                </div>
                {step === 'idle' && !result && (
                    <Button
                        size="sm"
                        variant="outline"
                        onClick={() => setStep('paste')}
                        className="shrink-0"
                    >
                        {t('aiMemoryImportDialog.promptStartButton')}
                    </Button>
                )}
            </div>

            {step === 'paste' && !result && (
                <div className="ml-6 border-l-2 border-border pl-3 flex flex-col gap-3 py-2">
                    <div className="flex flex-col gap-1.5">
                        <span className="text-xs font-medium text-ink">
                            {t('aiMemoryImportDialog.step1Heading', { provider: providerName })}
                        </span>
                        <div className="relative">
                            <div
                                className={`relative overflow-hidden rounded-md border border-border bg-background-accent/30 transition-all duration-200 ${promptExpanded ? '' : 'max-h-[80px]'}`}
                                onClick={() => !promptExpanded && setPromptExpanded(true)}
                                role={promptExpanded ? undefined : 'button'}
                                tabIndex={promptExpanded ? undefined : 0}
                                onKeyDown={promptExpanded ? undefined : (e) => { if (e.key === 'Enter') setPromptExpanded(true); }}
                            >
                                <pre className="text-[11px] text-muted p-3 pr-10 whitespace-pre-wrap leading-relaxed cursor-text">
                                    {MEMORY_EXTRACTION_PROMPT}
                                </pre>
                                {!promptExpanded && (
                                    <div className="absolute inset-x-0 bottom-0 h-10 bg-gradient-to-t from-background-accent/80 to-transparent pointer-events-none" />
                                )}
                                <button
                                    type="button"
                                    onClick={(e) => { e.stopPropagation(); handleCopy(); }}
                                    className="absolute bottom-2 right-2 z-10 inline-flex items-center gap-1.5 h-8 px-3 rounded-lg text-xs font-semibold border border-border bg-panel text-muted shadow-sm hover:bg-background-accent hover:text-ink hover:border-border-strong transition-colors cursor-pointer focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-2/40"
                                >
                                    {copied ? (
                                        <CheckCircle2 className="h-3.5 w-3.5 text-success" />
                                    ) : (
                                        <Copy className="h-3.5 w-3.5" />
                                    )}
                                    {copied ? t('aiMemoryImportDialog.copiedLabel') : t('aiMemoryImportDialog.copyPromptButton')}
                                </button>
                            </div>
                        </div>
                    </div>
                    <div className="flex flex-col gap-1.5">
                        <span className="text-xs font-medium text-ink">
                            {t('aiMemoryImportDialog.step2Heading', { provider: providerName })}
                        </span>
                        <textarea
                            value={pastedText}
                            onChange={(e) => setPastedText(e.target.value)}
                            placeholder={t('aiMemoryImportDialog.pastePlaceholder', { provider: providerName })}
                            rows={5}
                            className="w-full rounded-md border border-border bg-background p-3 text-xs text-ink placeholder:text-muted/50 focus:outline-none focus:ring-1 focus:ring-accent resize-y"
                        />
                        <div className="flex justify-end">
                            <Button
                                size="sm"
                                onClick={handleImport}
                                disabled={importing || !pastedText.trim()}
                            >
                                {importing ? (
                                    <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />
                                ) : (
                                    <Download className="h-3.5 w-3.5 mr-1.5" />
                                )}
                                {t('aiMemoryImportDialog.importButton')}
                            </Button>
                        </div>
                    </div>
                </div>
            )}

            {result && (
                <div className={`rounded-lg border p-3 text-sm ${
                    result.imported > 0
                        ? 'border-success/30 bg-success-bg text-success'
                        : 'border-destructive/30 bg-destructive/5 text-destructive'
                }`}>
                    <div className="flex items-center gap-2">
                        {result.imported > 0 && <CheckCircle2 className="h-4 w-4 shrink-0" />}
                        <span>
                            {result.imported > 0
                                ? t('aiMemoryImportDialog.promptImportSuccess', { provider: providerName })
                                : t('aiMemoryImportDialog.promptImportNone')}
                        </span>
                    </div>
                    {result.errors && result.errors.length > 0 && (
                        <ul className="mt-1 text-xs text-destructive list-disc pl-4">
                            {result.errors.map((err, i) => (
                                <li key={i}>{err}</li>
                            ))}
                        </ul>
                    )}
                </div>
            )}
        </div>
    );
}

// ── Main dialog ──

interface AIMemoryImportDialogProps {
    open: boolean;
    onOpenChange: (open: boolean) => void;
    onImported?: () => void;
}

export function AIMemoryImportDialog({ open, onOpenChange, onImported }: AIMemoryImportDialogProps) {
    const { t } = useTranslation('settings');
    const [sources, setSources] = useState<main.AIMemorySource[]>([]);
    const [scanning, setScanning] = useState(false);
    const [scanError, setScanError] = useState<string | null>(null);
    const [selectedFileIds, setSelectedFileIds] = useState<Set<string>>(new Set());
    const [expandedSources, setExpandedSources] = useState<Set<string>>(new Set());
    const [importing, setImporting] = useState(false);
    const [importResult, setImportResult] = useState<main.AIMemoryImportResult | null>(null);

    // Reset key forces PromptImportCard remount on dialog reopen
    const [resetKey, setResetKey] = useState(0);

    useEffect(() => {
        if (!open) return;
        setImportResult(null);
        setResetKey((k) => k + 1);
        setScanning(true);
        setScanError(null);
        ScanAIMemories()
            .then((result) => {
                setSources(result ?? []);
                const newIds = new Set<string>();
                for (const src of result ?? []) {
                    for (const f of src.files ?? []) {
                        if (!f.imported) {
                            newIds.add(f.id);
                        }
                    }
                }
                setSelectedFileIds(newIds);
                // Auto-collapse sources where all files are already imported
                const expanded = new Set<string>();
                for (const src of result ?? []) {
                    const files = src.files ?? [];
                    const allImported = files.length > 0 && files.every((f) => f.imported);
                    if (!allImported) {
                        expanded.add(src.id);
                    }
                }
                setExpandedSources(expanded);
            })
            .catch((err: unknown) => {
                setScanError(String(err));
            })
            .finally(() => {
                setScanning(false);
            });
    }, [open]);

    const totalSelected = selectedFileIds.size;
    const totalFiles = useMemo(() => {
        let count = 0;
        for (const src of sources) {
            count += (src.files ?? []).length;
        }
        return count;
    }, [sources]);

    const toggleSource = useCallback((sourceId: string) => {
        setExpandedSources((prev) => {
            const next = new Set(prev);
            if (next.has(sourceId)) {
                next.delete(sourceId);
            } else {
                next.add(sourceId);
            }
            return next;
        });
    }, []);

    const toggleSourceSelection = useCallback((source: main.AIMemorySource, checked: boolean) => {
        setSelectedFileIds((prev) => {
            const next = new Set(prev);
            for (const f of source.files ?? []) {
                if (checked) {
                    next.add(f.id);
                } else {
                    next.delete(f.id);
                }
            }
            return next;
        });
    }, []);

    const toggleFileSelection = useCallback((fileId: string, checked: boolean) => {
        setSelectedFileIds((prev) => {
            const next = new Set(prev);
            if (checked) {
                next.add(fileId);
            } else {
                next.delete(fileId);
            }
            return next;
        });
    }, []);

    const handleImport = useCallback(async () => {
        if (totalSelected === 0) return;
        setImporting(true);
        try {
            const ids = Array.from(selectedFileIds);
            const result = await ImportAIMemories(ids);
            setImportResult(result);
            if (result.imported > 0) {
                const importedSet = new Set(ids);
                setSources((prev) =>
                    prev.map((src) => ({
                        ...src,
                        files: (src.files ?? []).map((f) =>
                            importedSet.has(f.id) ? { ...f, imported: true } : f,
                        ),
                    })) as main.AIMemorySource[],
                );
                setSelectedFileIds(new Set());
                // Collapse sources that are now fully imported
                setExpandedSources((prev) => {
                    const next = new Set(prev);
                    for (const src of sources) {
                        const files = src.files ?? [];
                        const allNowImported = files.every((f) => importedSet.has(f.id) || f.imported);
                        if (allNowImported) {
                            next.delete(src.id);
                        }
                    }
                    return next;
                });
                onImported?.();
            }
        } catch (err: unknown) {
            setImportResult({
                imported: 0,
                skipped: 0,
                errors: [String(err)],
            } as main.AIMemoryImportResult);
        } finally {
            setImporting(false);
        }
    }, [selectedFileIds, totalSelected, onImported, sources]);

    return (
        <Dialog open={open} onOpenChange={onOpenChange}>
            <DialogContent className="w-[600px] max-w-[90vw]">
                <DialogHeader>
                    <div className="flex-1">
                        <DialogTitle>{t('aiMemoryImportDialog.dialogTitle')}</DialogTitle>
                        <DialogDescription>
                            {t('aiMemoryImportDialog.dialogDescription')}
                        </DialogDescription>
                    </div>
                    <DialogCloseButton />
                </DialogHeader>

                <div className="flex flex-col gap-4 px-6 py-4 overflow-y-auto max-h-[60vh]">
                    {/* Scanning state */}
                    {scanning && (
                        <div className="flex items-center gap-3 py-8 justify-center text-muted">
                            <Loader2 className="h-5 w-5 animate-spin" />
                            <span className="text-sm">{t('aiMemoryImportDialog.scanning')}</span>
                        </div>
                    )}

                    {scanError && (
                        <div className="rounded-lg border border-destructive/30 bg-destructive/5 p-4 text-sm text-destructive">
                            {t('aiMemoryImportDialog.scanFailed', { error: scanError })}
                        </div>
                    )}

                    {!scanning && !scanError && sources.length === 0 && (
                        <div className="flex flex-col items-center gap-3 py-8 text-muted">
                            <FileText className="h-10 w-10 opacity-40" />
                            <p className="text-sm">{t('aiMemoryImportDialog.noFilesFound')}</p>
                        </div>
                    )}

                    {/* File-based sources (auto-scanned) */}
                    {!scanning && sources.length > 0 && (
                        <div className="flex flex-col gap-2">
                            {sources.map((source) => {
                                const SourceIcon = SOURCE_ICON[source.id];
                                const isExpanded = expandedSources.has(source.id);
                                const sourceFiles = source.files ?? [];
                                const selectedInSource = sourceFiles.filter((f) =>
                                    selectedFileIds.has(f.id),
                                ).length;
                                const allSelected = selectedInSource === sourceFiles.length;
                                const allImported = sourceFiles.length > 0 && sourceFiles.every((f) => f.imported);

                                return (
                                    <div key={source.id} className="flex flex-col">
                                        <div className={`flex items-center gap-3 p-3 rounded-lg border bg-panel transition-colors ${
                                            allImported ? 'border-success/20' : 'border-border hover:bg-background-accent/30'
                                        }`}>
                                            <input
                                                type="checkbox"
                                                checked={allSelected && sourceFiles.length > 0}
                                                onChange={(e) => toggleSourceSelection(source, e.target.checked)}
                                                className="h-4 w-4 rounded border-border text-accent focus:ring-accent shrink-0"
                                            />
                                            <button
                                                type="button"
                                                onClick={() => toggleSource(source.id)}
                                                className="flex items-center gap-2 flex-1 min-w-0 text-left"
                                            >
                                                {SourceIcon && (
                                                    <div className="w-8 h-8 rounded-md bg-background-accent/40 flex items-center justify-center shrink-0">
                                                        <SourceIcon className="h-4 w-4" />
                                                    </div>
                                                )}
                                                <div className="flex-1 min-w-0">
                                                    <div className="flex items-center gap-2">
                                                        <span className="text-sm font-semibold text-ink truncate">{source.name}</span>
                                                        <span className="text-[10px] text-muted bg-border/50 rounded-full px-1.5 py-0.5 shrink-0">
                                                            {t('aiMemoryImportDialog.fileCount', { count: sourceFiles.length })}
                                                        </span>
                                                        {allImported && (
                                                            <span className="text-[10px] text-success bg-success-bg rounded-full px-1.5 py-0.5 shrink-0">{t('aiMemoryImportDialog.allImported')}</span>
                                                        )}
                                                        <span className="text-[10px] text-muted shrink-0">
                                                            {formatBytes(source.totalSize)}
                                                        </span>
                                                    </div>
                                                    <p className="text-xs text-muted mt-0.5 truncate">{source.description}</p>
                                                </div>
                                                {isExpanded ? (
                                                    <ChevronDown className="h-4 w-4 text-muted shrink-0" />
                                                ) : (
                                                    <ChevronRight className="h-4 w-4 text-muted shrink-0" />
                                                )}
                                            </button>
                                        </div>

                                        {isExpanded && sourceFiles.length > 0 && (
                                            <div className="ml-6 mt-1 border-l-2 border-border pl-3 flex flex-col gap-0.5">
                                                {sourceFiles.map((file) => (
                                                    <label
                                                        key={file.id}
                                                        className="flex items-start gap-2.5 py-1.5 px-2 rounded hover:bg-background-accent/30 transition-colors cursor-pointer"
                                                    >
                                                        <input
                                                            type="checkbox"
                                                            checked={selectedFileIds.has(file.id)}
                                                            onChange={(e) => toggleFileSelection(file.id, e.target.checked)}
                                                            className="h-3.5 w-3.5 rounded border-border text-accent focus:ring-accent mt-0.5 shrink-0"
                                                        />
                                                        <div className="flex-1 min-w-0">
                                                            <div className="flex items-center gap-2">
                                                                <span className={`text-xs font-medium truncate ${file.imported ? 'text-muted' : 'text-ink'}`}>{file.title}</span>
                                                                {file.imported && (
                                                                    <span className="text-[10px] text-success bg-success-bg rounded-full px-1.5 py-0.5 shrink-0">{t('aiMemoryImportDialog.importedBadge')}</span>
                                                                )}
                                                                <span className="text-[10px] text-muted shrink-0">{formatBytes(file.size)}</span>
                                                            </div>
                                                            {file.preview && (
                                                                <p className="text-[11px] text-muted/70 mt-0.5 line-clamp-2 leading-relaxed">{file.preview}</p>
                                                            )}
                                                            <p className="text-[10px] text-muted/50 mt-0.5 font-mono truncate">{file.relPath}</p>
                                                        </div>
                                                    </label>
                                                ))}
                                            </div>
                                        )}
                                    </div>
                                );
                            })}
                        </div>
                    )}

                    {/* Prompt-based import providers (ChatGPT, Claude Desktop, etc.) */}
                    {!scanning && PROMPT_PROVIDERS.map((provider) => (
                        <PromptImportCard
                            key={`${provider.id}-${resetKey}`}
                            provider={provider}
                            onImported={onImported}
                        />
                    ))}

                    {/* File import result */}
                    {importResult && (
                        <div className={`rounded-lg border p-3 text-sm ${
                            importResult.imported > 0
                                ? 'border-success/30 bg-success-bg text-success'
                                : 'border-destructive/30 bg-destructive/5 text-destructive'
                        }`}>
                            <div className="flex items-center gap-2">
                                {importResult.imported > 0 && <CheckCircle2 className="h-4 w-4 shrink-0" />}
                                <span>
                                    {importResult.imported > 0
                                        ? t('aiMemoryImportDialog.fileImportSuccess', { count: importResult.imported })
                                        : t('aiMemoryImportDialog.fileImportNone')}
                                    {importResult.skipped > 0 && t('aiMemoryImportDialog.skippedSuffix', { count: importResult.skipped })}
                                </span>
                            </div>
                            {importResult.errors && importResult.errors.length > 0 && (
                                <ul className="mt-1 text-xs text-destructive list-disc pl-4">
                                    {importResult.errors.map((err, i) => (
                                        <li key={i}>{err}</li>
                                    ))}
                                </ul>
                            )}
                        </div>
                    )}
                </div>

                {/* Footer */}
                <div className="flex items-center justify-end gap-3 px-6 py-4 border-t border-border shrink-0">
                    {totalSelected > 0 && !importing && totalFiles > 0 && (
                        <Button size="sm" onClick={handleImport}>
                            <Download className="h-3.5 w-3.5 mr-1.5" />
                            {t('aiMemoryImportDialog.importButton', { count: totalSelected })}
                        </Button>
                    )}
                    {importing && (
                        <Button size="sm" disabled>
                            <Loader2 className="h-3.5 w-3.5 mr-1.5 animate-spin" />
                            {t('aiMemoryImportDialog.importing')}
                        </Button>
                    )}
                </div>
            </DialogContent>
        </Dialog>
    );
}
