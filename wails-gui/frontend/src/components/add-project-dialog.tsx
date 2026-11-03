import { useState } from 'react';
import { FolderOpen, Github } from 'lucide-react';
import { YansuIcon } from '@/components/icons/app-icons';
import {
    Dialog,
    DialogContent,
    DialogHeader,
    DialogTitle,
    DialogCloseButton,
} from '@/components/ui/dialog';
import { AddProjectLocal } from './add-project-local';
import { AddProjectYansu } from './add-project-yansu';
import { AddProjectURL } from './add-project-url';
import type { Project, RemoteOrg, RemoteProduct, RemoteProject } from '@/types';

type AddProjectTab = 'local' | 'yansu' | 'url';

const tabs: { key: AddProjectTab; label: string; icon: React.ComponentType<{ className?: string }> }[] = [
    { key: 'yansu', label: 'Isoform', icon: YansuIcon },
    { key: 'url', label: 'GitHub', icon: Github },
    { key: 'local', label: 'Local', icon: FolderOpen },
];

interface AddProjectDialogProps {
    open: boolean;
    onClose: () => void;
    // Local tab
    onOpenLocal: () => Promise<Project | null>;
    onOpenPath: (path: string) => Promise<void>;
    // Isoform tab
    yansuOrgs: RemoteOrg[];
    yansuProducts: RemoteProduct[];
    yansuProjects: RemoteProject[];
    yansuSelectedOrg: string;
    yansuSelectedProduct: string;
    onYansuSelectOrg: (org: string) => void;
    onYansuSelectProduct: (product: string) => void;
    onYansuClone: (proj: RemoteProject) => void;
    yansuLoading: boolean;
    yansuError: string | null;
    // URL tab
    onCloneURL: (url: string) => void;
}

export function AddProjectDialog({
    open,
    onClose,
    onOpenLocal,
    onOpenPath,
    yansuOrgs,
    yansuProducts,
    yansuProjects,
    yansuSelectedOrg,
    yansuSelectedProduct,
    onYansuSelectOrg,
    onYansuSelectProduct,
    onYansuClone,
    yansuLoading,
    yansuError,
    onCloneURL,
}: AddProjectDialogProps) {
    const [activeTab, setActiveTab] = useState<AddProjectTab>('yansu');

    return (
        <Dialog open={open} onOpenChange={(v) => !v && onClose()}>
            <DialogContent>
                <DialogHeader>
                    <DialogTitle>Add Project</DialogTitle>
                    <DialogCloseButton />
                </DialogHeader>

                {/* Tab bar */}
                <div className="flex items-center gap-0 px-6 border-b border-border shrink-0">
                    {tabs.map(({ key, label, icon: Icon }) => (
                        <button
                            key={key}
                            className={`flex items-center gap-1.5 px-3 py-2.5 text-xs font-medium cursor-pointer border-b-2 transition-colors bg-transparent ${
                                activeTab === key
                                    ? 'border-accent-2 text-accent-2'
                                    : 'border-transparent text-muted hover:text-ink'
                            }`}
                            onClick={() => setActiveTab(key)}
                        >
                            <Icon className="h-3.5 w-3.5" />
                            {label}
                        </button>
                    ))}
                </div>

                {/* Tab content */}
                <div className="overflow-y-auto flex-1">
                    {activeTab === 'local' && (
                        <AddProjectLocal
                            onOpenLocal={onOpenLocal}
                            onOpenPath={onOpenPath}
                            onClose={onClose}
                        />
                    )}
                    {activeTab === 'yansu' && (
                        <AddProjectYansu
                            orgs={yansuOrgs}
                            products={yansuProducts}
                            projects={yansuProjects}
                            selectedOrg={yansuSelectedOrg}
                            selectedProduct={yansuSelectedProduct}
                            onSelectOrg={onYansuSelectOrg}
                            onSelectProduct={onYansuSelectProduct}
                            onCloneProject={onYansuClone}
                            loading={yansuLoading}
                            error={yansuError}
                        />
                    )}
                    {activeTab === 'url' && (
                        <AddProjectURL
                            onCloneURL={onCloneURL}
                        />
                    )}
                </div>
            </DialogContent>
        </Dialog>
    );
}
