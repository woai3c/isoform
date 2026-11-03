import type { Session } from '@/types';

export interface SidebarSessionGroups {
    pinnedSessions: Session[];
    recentSessions: Session[];
    effectiveSessions: Session[];
}

export function groupSidebarSessions(sessions: Session[]): SidebarSessionGroups {
    const pinnedSessions = sessions
        .filter((session) => session.pinned)
        .sort((a, b) => a.pinnedAt - b.pinnedAt);
    const recentSessions = sessions
        .filter((session) => !session.pinned)
        .sort((a, b) => b.updatedAt - a.updatedAt);

    return {
        pinnedSessions,
        recentSessions,
        effectiveSessions: [...pinnedSessions, ...recentSessions],
    };
}
