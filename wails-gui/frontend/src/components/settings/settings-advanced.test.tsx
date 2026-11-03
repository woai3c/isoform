import { useRef } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import {
    SETTINGS_ADVANCED_ANCHOR,
    SettingsAdvanced,
    SettingsAdvancedRevealContext,
    advancedStorageKey,
    findSettingsAnchor,
    settingsAnchor,
    useSettingsAnchorScroll,
} from "@/components/settings/settings-advanced";

function RevealedPage({ reveal }: { reveal: number }) {
    return (
        <SettingsAdvancedRevealContext.Provider value={reveal}>
            <SettingsAdvanced pageId="ai-provider">
                <p>Own API key</p>
            </SettingsAdvanced>
        </SettingsAdvancedRevealContext.Provider>
    );
}

function AnchoredPage({ anchor, trigger, showLate }: { anchor?: string; trigger: number; showLate: boolean }) {
    const ref = useRef<HTMLDivElement>(null);
    useSettingsAnchorScroll(ref, anchor, trigger);
    return (
        <div ref={ref}>
            <div {...settingsAnchor("everyday")}>Everyday</div>
            {showLate && <div {...settingsAnchor("search-index")}>Search index</div>}
        </div>
    );
}

describe("SettingsAdvanced", () => {
    beforeEach(async () => {
        window.localStorage.clear();
        await i18n.changeLanguage("en");
    });

    afterEach(() => {
        vi.restoreAllMocks();
    });

    it("starts closed with the label and hint, and opens on click", () => {
        render(
            <SettingsAdvanced pageId="general" hint="Keyboard shortcuts and voices">
                <p>Hidden detail</p>
            </SettingsAdvanced>,
        );

        const trigger = screen.getByRole("button", { name: /Advanced/ });
        expect(trigger).toHaveTextContent("Keyboard shortcuts and voices");
        expect(trigger).toHaveAttribute("aria-expanded", "false");
        expect(screen.queryByText("Hidden detail")).not.toBeInTheDocument();

        fireEvent.click(trigger);

        expect(trigger).toHaveAttribute("aria-expanded", "true");
        expect(screen.getByText("Hidden detail")).toBeInTheDocument();
        expect(window.localStorage.getItem(advancedStorageKey("general"))).toBe("1");
    });

    it("remembers the open state per page", () => {
        window.localStorage.setItem(advancedStorageKey("memory"), "1");

        render(
            <>
                <SettingsAdvanced pageId="memory">
                    <p>Memory detail</p>
                </SettingsAdvanced>
                <SettingsAdvanced pageId="security">
                    <p>Privacy detail</p>
                </SettingsAdvanced>
            </>,
        );

        expect(screen.getByText("Memory detail")).toBeInTheDocument();
        expect(screen.queryByText("Privacy detail")).not.toBeInTheDocument();
    });

    it("still works when storage throws", () => {
        vi.spyOn(Storage.prototype, "getItem").mockImplementation(() => {
            throw new Error("blocked");
        });
        vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
            throw new Error("blocked");
        });

        render(
            <SettingsAdvanced pageId="general" defaultOpen>
                <p>Visible detail</p>
            </SettingsAdvanced>,
        );

        const trigger = screen.getByRole("button", { name: /Advanced/ });
        expect(screen.getByText("Visible detail")).toBeInTheDocument();
        fireEvent.click(trigger);
        expect(trigger).toHaveAttribute("aria-expanded", "false");
    });

    it("opens for a search reveal without remembering it", () => {
        render(
            <SettingsAdvancedRevealContext.Provider value={1}>
                <SettingsAdvanced pageId="iphone">
                    <p>Server address</p>
                </SettingsAdvanced>
            </SettingsAdvancedRevealContext.Provider>,
        );

        expect(screen.getByRole("button", { name: /Advanced/ })).toHaveAttribute("aria-expanded", "true");
        expect(screen.getByText("Server address")).toBeInTheDocument();
        expect(window.localStorage.getItem(advancedStorageKey("iphone"))).toBeNull();
    });

    it("opens again for each new reveal after the person collapsed it", () => {
        const { rerender } = render(<RevealedPage reveal={1} />);
        const trigger = screen.getByRole("button", { name: /Advanced/ });
        expect(trigger).toHaveAttribute("aria-expanded", "true");

        fireEvent.click(trigger);
        expect(trigger).toHaveAttribute("aria-expanded", "false");

        // The same reveal re-rendered does not undo the person's choice ...
        rerender(<RevealedPage reveal={1} />);
        expect(trigger).toHaveAttribute("aria-expanded", "false");

        // ... a new search or deep link that asks for Advanced opens it again.
        rerender(<RevealedPage reveal={2} />);
        expect(trigger).toHaveAttribute("aria-expanded", "true");
        expect(screen.getByText("Own API key")).toBeInTheDocument();

        // No reveal leaves it as it is.
        fireEvent.click(trigger);
        rerender(<RevealedPage reveal={0} />);
        expect(trigger).toHaveAttribute("aria-expanded", "false");
    });

    it("marks the disclosure as the Advanced anchor", () => {
        const { container } = render(
            <SettingsAdvanced pageId="general">
                <p>detail</p>
            </SettingsAdvanced>,
        );
        expect(findSettingsAnchor(container, SETTINGS_ADVANCED_ANCHOR)).toContainElement(
            screen.getByRole("button", { name: /Advanced/ }),
        );
        expect(findSettingsAnchor(container, "missing")).toBeNull();
        expect(findSettingsAnchor(null, SETTINGS_ADVANCED_ANCHOR)).toBeNull();
    });

    it("scrolls to an anchor, waiting for it to render, on each new trigger", () => {
        vi.useFakeTimers();
        const scrolled: string[] = [];
        const original = Element.prototype.scrollIntoView;
        Element.prototype.scrollIntoView = function (this: Element) {
            scrolled.push(this.textContent ?? "");
        };
        try {
            const { rerender } = render(<AnchoredPage anchor="search-index" trigger={1} showLate={false} />);
            expect(scrolled).toEqual([]);

            rerender(<AnchoredPage anchor="search-index" trigger={1} showLate />);
            act(() => {
                vi.advanceTimersByTime(100);
            });
            expect(scrolled).toEqual(["Search index"]);

            rerender(<AnchoredPage anchor="search-index" trigger={2} showLate />);
            expect(scrolled).toEqual(["Search index", "Search index"]);

            // Without an anchor nothing scrolls.
            rerender(<AnchoredPage trigger={3} showLate />);
            expect(scrolled).toHaveLength(2);

            // It gives up quietly when the anchor never renders.
            rerender(<AnchoredPage anchor="nowhere" trigger={4} showLate />);
            act(() => {
                vi.advanceTimersByTime(5000);
            });
            expect(scrolled).toHaveLength(2);
        } finally {
            Element.prototype.scrollIntoView = original;
            vi.useRealTimers();
        }
    });

    it("uses the translated label", async () => {
        await i18n.changeLanguage("zh-CN");
        const { unmount } = render(
            <SettingsAdvanced pageId="general">
                <p>detail</p>
            </SettingsAdvanced>,
        );
        expect(screen.getByRole("button", { name: /高级/ })).toBeInTheDocument();
        unmount();
        await i18n.changeLanguage("en");
    });
});
