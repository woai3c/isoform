import { Toaster } from "sonner";
import { useTheme } from "@/hooks/useTheme";

/**
 * AppToaster renders the Sonner toast surface with the app's theming. It is the
 * single source of truth for toast styling and must be mounted in every
 * top-level render branch that can fire a toast — including onboarding, which
 * renders before the main app shell. Without it,
 * toast.* calls (e.g. the AI provider "Test connection" result) are silently
 * dropped because no Toaster exists in that subtree.
 */
export function AppToaster() {
    const { theme: currentTheme } = useTheme();
    return (
        <Toaster
            position="bottom-right"
            theme={currentTheme.isDark ? "dark" : "light"}
            toastOptions={{
                style: {
                    background: "var(--color-panel)",
                    color: "var(--color-ink)",
                    border: "1px solid var(--color-border)",
                    fontFamily: "var(--font-sans)",
                    boxShadow: "var(--shadow-soft)",
                },
                classNames: {
                    title: "line-clamp-2 break-words",
                    description: "line-clamp-3 break-words",
                },
            }}
        />
    );
}
