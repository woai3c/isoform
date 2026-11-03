import { FolderPlus } from 'lucide-react';
import {
    DropdownMenu,
    DropdownMenuContent,
    DropdownMenuItem,
    DropdownMenuSub,
    DropdownMenuSubContent,
    DropdownMenuSubTrigger,
    DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu';
import type { SidebarGroup } from '@/types';

interface GroupListProps {
    groups: SidebarGroup[];
    onSelect: (groupId: string) => void;
}

/** Renders group selection items (shared between sub-menu and standalone). */
function GroupListItems({ groups, onSelect }: GroupListProps) {
    if (groups.length === 0) {
        return (
            <DropdownMenuItem disabled>
                No groups yet
            </DropdownMenuItem>
        );
    }
    return (
        <>
            {groups.map((g) => (
                <DropdownMenuItem key={g.id} onClick={(e) => { e.stopPropagation(); onSelect(g.id); }}>
                    {g.name}
                </DropdownMenuItem>
            ))}
        </>
    );
}

/** Sub-menu variant: use inside an existing DropdownMenuContent. */
export function AddToGroupSubmenu({ groups, onSelect }: GroupListProps) {
    return (
        <DropdownMenuSub>
            <DropdownMenuSubTrigger>
                <FolderPlus className="h-3.5 w-3.5" />
                Add to Group
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent>
                <GroupListItems groups={groups} onSelect={onSelect} />
            </DropdownMenuSubContent>
        </DropdownMenuSub>
    );
}

interface AddToGroupButtonProps extends GroupListProps {
    className?: string;
}

/** Standalone button variant: renders its own DropdownMenu trigger. */
export function AddToGroupButton({ groups, onSelect, className }: AddToGroupButtonProps) {
    return (
        <DropdownMenu>
            <DropdownMenuTrigger asChild>
                <button
                    className={className ?? 'p-0.5 rounded text-muted hover:text-ink hover:bg-ink/10 transition-colors'}
                    title="Add to group"
                    onClick={(e) => e.stopPropagation()}
                >
                    <FolderPlus className="size-3" />
                </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" sideOffset={4}>
                <GroupListItems groups={groups} onSelect={onSelect} />
            </DropdownMenuContent>
        </DropdownMenu>
    );
}
