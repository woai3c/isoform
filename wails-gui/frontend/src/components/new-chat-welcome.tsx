import type { ReactNode } from "react";
import { useTranslation } from "react-i18next";
import { LogoIcon } from "@/assets/icons/LogoIcon";

export interface RecommendedQuestion {
    title: string;
    prompt: string;
}

interface NewChatWelcomeProps {
    children?: ReactNode;
    /** Rendered under the composer: the main chat's hub strip. */
    aside?: ReactNode;
}

export function NewChatWelcome({ children, aside }: NewChatWelcomeProps) {
    const { t } = useTranslation("chat");
    return (
        <div
            className={`flex-1 flex flex-col items-center ${
                aside ? "overflow-y-auto pb-10" : "pb-[12vh]"
            }`}
        >
            {/* The empty space above the greeting drags the window like the
                title strip; without an aside it and the spacer at the end
                center the content. */}
            <div
                aria-hidden
                className={`window-drag w-full shrink-0 ${aside ? "h-[6vh]" : "flex-1"}`}
            />
            <div className="window-drag w-full flex flex-col items-center">
                <LogoIcon blink className="w-[52px] h-[52px] mb-6 text-accent" />
                {/* Serif for the one brand moment on this screen — everything
                    else stays in the quiet grotesque. */}
                <h1 className="font-serif text-[30px] font-normal tracking-[-0.01em] text-heading-strong mb-2.5">
                    {t("welcome.heading")}
                </h1>
                <p className="text-[14px] text-welcome-sub mb-8">
                    {t("welcome.subheading")}
                </p>
            </div>

            {children && (
                <div className="w-full max-w-[768px] px-4">
                    {children}
                </div>
            )}
            {aside ? (
                <div className="w-full max-w-[768px] px-4 mt-8">
                    {aside}
                </div>
            ) : (
                <div aria-hidden className="flex-1" />
            )}
        </div>
    );
}
