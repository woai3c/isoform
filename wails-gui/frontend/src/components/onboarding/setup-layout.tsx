import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { Button } from "@/components/ui/button";

export function SetupBody({ children }: { children: ReactNode }) {
    return (
        <div className="setup-body">
            <div className="setup-column">{children}</div>
        </div>
    );
}

export function SetupHeading({
    title,
    description,
}: {
    title: string;
    description: string;
}) {
    return (
        <>
            <h1 className="setup-heading">{title}</h1>
            <p className="setup-subtitle">{description}</p>
        </>
    );
}

export function SetupFooter({
    onBack,
    children,
}: {
    onBack?: () => void;
    children: ReactNode;
}) {
    const { t } = useTranslation("onboarding");
    return (
        <div className="setup-footer">
            {onBack && (
                <Button
                    variant="quiet"
                    data-testid="onboarding-back"
                    onClick={onBack}
                >
                    {t("actions.back")}
                </Button>
            )}
            <span className="flex-1" />
            {children}
        </div>
    );
}
