import { Component, type ErrorInfo, type ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";
import { SettingsCard } from "@/components/settings/settings-primitives";

/**
 * Keeps one broken Settings page from blanking the whole window. When a page
 * throws while rendering, the page area shows a short message with Try again
 * instead, and the sidebar keeps working. Keyed by page by the caller, so
 * moving to another page starts fresh.
 */
export class SettingsPageBoundary extends Component<
    { children: ReactNode },
    { failed: boolean }
> {
    state = { failed: false };

    static getDerivedStateFromError() {
        return { failed: true };
    }

    componentDidCatch(error: Error, info: ErrorInfo) {
        console.error("[settings] page failed to render", error, info.componentStack);
    }

    render() {
        if (this.state.failed) {
            return <PageFailed onRetry={() => this.setState({ failed: false })} />;
        }
        return this.props.children;
    }
}

function PageFailed({ onRetry }: { onRetry: () => void }) {
    const { t } = useTranslation("settings");
    return (
        <SettingsCard title={t("pageFailed.title")} description={t("pageFailed.description")}>
            <div className="flex justify-end">
                <Button size="sm" variant="outline" onClick={onRetry}>
                    {t("pageFailed.retry")}
                </Button>
            </div>
        </SettingsCard>
    );
}
