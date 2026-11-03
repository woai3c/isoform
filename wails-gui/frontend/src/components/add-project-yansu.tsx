import { useState, useMemo, useEffect } from 'react';
import { SearchIcon } from '@/assets/icons/SearchIcon';
import { Combobox } from '@/components/ui/combobox';
import type { RemoteOrg, RemoteProduct, RemoteProject } from '@/types';

interface AddProjectYansuProps {
    orgs: RemoteOrg[];
    products: RemoteProduct[];
    projects: RemoteProject[];
    selectedOrg: string;
    selectedProduct: string;
    onSelectOrg: (org: string) => void;
    onSelectProduct: (product: string) => void;
    onCloneProject: (proj: RemoteProject) => void;
    loading: boolean;
    error: string | null;
}

export function AddProjectYansu({
    orgs,
    products,
    projects,
    selectedOrg,
    selectedProduct,
    onSelectOrg,
    onSelectProduct,
    onCloneProject,
    loading,
    error,
}: AddProjectYansuProps) {
    const [projectQuery, setProjectQuery] = useState('');

    // Clear search when product selection changes
    useEffect(() => {
        setProjectQuery('');
    }, [selectedProduct]);

    const filteredProjects = useMemo(() => {
        if (!projectQuery.trim()) return projects;
        const q = projectQuery.toLowerCase();
        return projects.filter(
            (p) =>
                (p.display_name || p.name).toLowerCase().includes(q) ||
                p.name.toLowerCase().includes(q) ||
                p.coding_branch?.toLowerCase().includes(q)
        );
    }, [projects, projectQuery]);

    return (
        <>
            {error && (
                <p className="mx-6 mt-3 px-3.5 py-2.5 rounded-md bg-error-bg text-error text-[13px] border border-error-border">
                    {error}
                </p>
            )}

            <div className="p-6 pt-5 overflow-y-auto flex flex-col gap-4">
                {/* Selects row */}
                <div className="flex gap-3">
                    <div className="flex-1 flex flex-col gap-1.5">
                        <label className="text-xs font-semibold text-muted uppercase tracking-wider">
                            Organization
                        </label>
                        <Combobox
                            options={orgs.map((org) => ({
                                value: org.name,
                                label: org.display_name || org.name,
                            }))}
                            value={selectedOrg}
                            onValueChange={onSelectOrg}
                            placeholder="Select organization..."
                            searchPlaceholder="Search organizations..."
                            disabled={loading && orgs.length === 0}
                            portal={false}
                        />
                    </div>

                    <div className="flex-1 flex flex-col gap-1.5">
                        <label className="text-xs font-semibold text-muted uppercase tracking-wider">
                            Product
                        </label>
                        <Combobox
                            options={products.map((p) => ({
                                value: p.name,
                                label: p.display_name || p.name,
                            }))}
                            value={selectedProduct}
                            onValueChange={onSelectProduct}
                            placeholder="Select product..."
                            searchPlaceholder="Search products..."
                            disabled={
                                !selectedOrg ||
                                (loading && products.length === 0)
                            }
                            portal={false}
                        />
                    </div>
                </div>

                {loading && (
                    <p className="m-0 text-muted text-[13px] animate-pulse-slow">
                        Loading...
                    </p>
                )}

                {/* Project search + list */}
                {projects.length > 0 && (
                    <div className="flex flex-col gap-2">
                        <div className="flex items-center gap-2 rounded-lg border border-border bg-panel px-2.5 transition-colors focus-within:border-accent-2/40">
                            <SearchIcon className="h-3.5 w-3.5 shrink-0 opacity-40" />
                            <input
                                type="text"
                                value={projectQuery}
                                onChange={(e) =>
                                    setProjectQuery(e.target.value)
                                }
                                placeholder="Search projects..."
                                className="flex h-9 w-full bg-transparent text-[13px] text-ink outline-none placeholder:text-muted"
                            />
                        </div>

                        {filteredProjects.map((proj) => (
                            <div
                                key={proj.uuid}
                                className="flex items-center justify-between px-3.5 py-3 rounded-xl border border-border bg-panel-warm transition-all hover:border-accent-2/30 hover:bg-accent-2/[0.03]"
                            >
                                <div className="flex flex-col gap-1">
                                    <span className="font-semibold text-sm">
                                        {proj.display_name || proj.name}
                                    </span>
                                    {proj.coding_branch && (
                                        <span className="text-xs text-muted font-mono">
                                            {proj.coding_branch}
                                        </span>
                                    )}
                                </div>
                                <button
                                    className="px-3.5 py-1.5 rounded-lg border border-[var(--color-sample-border)] bg-background text-[var(--color-sample-text)] text-xs font-semibold cursor-pointer whitespace-nowrap transition-all hover:bg-[var(--color-sample-text)]/[0.06]"
                                    onClick={() => onCloneProject(proj)}
                                >
                                    Clone
                                </button>
                            </div>
                        ))}

                        {filteredProjects.length === 0 && (
                            <p className="m-0 text-muted text-[13px] text-center py-4">
                                No matching projects.
                            </p>
                        )}
                    </div>
                )}

                {selectedProduct &&
                    !loading &&
                    projects.length === 0 && (
                        <p className="m-0 text-muted text-[13px] text-center py-6">
                            No projects found.
                        </p>
                    )}
            </div>
        </>
    );
}
