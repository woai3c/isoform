import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { DndContext } from "@dnd-kit/core";
import { beforeEach, describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import en from "@/i18n/locales/en/app.json";
import zhCN from "@/i18n/locales/zh-CN/app.json";
import type { SidebarGroup, SidebarGroupItem } from "@/types";
import { SidebarGroupSection } from "./sidebar-group-section";

function group(collapsed: boolean, items: SidebarGroupItem[] = []): SidebarGroup {
    return {
        id: "g1",
        name: "Trips",
        sortOrder: 0,
        collapsed,
        items,
        createdAt: 1,
        updatedAt: 1,
    };
}

function item(): SidebarGroupItem {
    return {
        id: "i1",
        groupId: "g1",
        itemType: "chat",
        refId: "chat-1",
        sortOrder: 0,
        addedAt: 1,
    };
}

function renderSection(initial: SidebarGroup) {
    const toggle = vi.fn();
    const clickItem = vi.fn();
    const noop = () => {};
    function Harness() {
        const [current, setCurrent] = useState(initial);
        return (
            <DndContext>
                <SidebarGroupSection
                    group={current}
                    onRename={noop}
                    onDelete={noop}
                    onToggleCollapse={(id, collapsed) => {
                        toggle(id, collapsed);
                        setCurrent((prev) => ({ ...prev, collapsed }));
                    }}
                    onRemoveItem={noop}
                    onClickItem={clickItem}
                    resolveItemLabel={(entry) => (entry.refId === "chat-1" ? "Grouped chat" : entry.refId)}
                />
            </DndContext>
        );
    }
    render(<Harness />);
    return { toggle, clickItem };
}

async function tabTo(user: ReturnType<typeof userEvent.setup>, target: HTMLElement) {
    for (let i = 0; i < 20 && document.activeElement !== target; i++) {
        await user.tab();
    }
    expect(document.activeElement).toBe(target);
}

describe("SidebarGroupSection keyboard access", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    it("folds and unfolds the group header with Space and Enter, aria-expanded in step", async () => {
        const user = userEvent.setup();
        const { toggle } = renderSection(group(false));
        const header = screen.getByRole("button", { name: "Trips" });
        expect(header.getAttribute("aria-expanded")).toBe("true");
        expect(header.getAttribute("title")).toBe(
            en.sidebar.toggleSection.replace("{{section}}", "Trips"),
        );
        expect(header.className).toMatch(/focus-visible:ring/);

        await tabTo(user, header);
        await user.keyboard(" ");
        expect(toggle).toHaveBeenLastCalledWith("g1", true);
        expect(header.getAttribute("aria-expanded")).toBe("false");
        await user.keyboard("{Enter}");
        expect(toggle).toHaveBeenLastCalledWith("g1", false);
        expect(header.getAttribute("aria-expanded")).toBe("true");
    });

    it("still folds and unfolds on click", () => {
        const { toggle } = renderSection(group(false));
        const header = screen.getByRole("button", { name: "Trips" });
        fireEvent.click(header);
        expect(toggle).toHaveBeenLastCalledWith("g1", true);
        expect(header.getAttribute("aria-expanded")).toBe("false");
        fireEvent.click(header);
        expect(toggle).toHaveBeenLastCalledWith("g1", false);
        expect(header.getAttribute("aria-expanded")).toBe("true");
    });

    it("opens a group item with Enter and Space and still opens it on click", async () => {
        const user = userEvent.setup();
        const { clickItem } = renderSection(group(false, [item()]));
        // The section's outer sortable node is also role=button, so find the
        // row as the nearest button-role ancestor of the item label.
        const row = screen.getByText("Grouped chat").closest('[role="button"]') as HTMLElement;
        expect(row).not.toBeNull();
        expect(row.getAttribute("tabindex")).toBe("0");

        await tabTo(user, row);
        await user.keyboard("{Enter}");
        expect(clickItem).toHaveBeenCalledTimes(1);
        expect(clickItem).toHaveBeenLastCalledWith(item());
        await user.keyboard(" ");
        expect(clickItem).toHaveBeenCalledTimes(2);

        fireEvent.click(row);
        expect(clickItem).toHaveBeenCalledTimes(3);
    });

    it("keeps the group menu button visible on keyboard focus, not only on hover", () => {
        renderSection(group(false));
        const trigger = document.querySelector('[aria-haspopup="menu"]') as HTMLElement;
        expect(trigger).not.toBeNull();
        const wrapper = trigger.closest("div") as HTMLElement;
        // Hidden until the header is hovered or something inside has focus.
        expect(wrapper.className).toMatch(/opacity-0/);
        expect(wrapper.className).toMatch(/group-hover\/group-header:opacity-100/);
        expect(wrapper.className).toMatch(/focus-within:opacity-100/);
    });

    it("names the group menu items from the locale, in English and Chinese", async () => {
        for (const [language, bundle] of [
            ["en", en],
            ["zh-CN", zhCN],
        ] as const) {
            await i18n.changeLanguage(language);
            const user = userEvent.setup();
            const { unmount } = render(
                <DndContext>
                    <SidebarGroupSection
                        group={group(false)}
                        onRename={() => {}}
                        onDelete={() => {}}
                        onToggleCollapse={() => {}}
                        onRemoveItem={() => {}}
                        onClickItem={() => {}}
                        resolveItemLabel={(entry) => entry.refId}
                    />
                </DndContext>,
            );
            const trigger = document.querySelector('[aria-haspopup="menu"]') as HTMLElement;
            await user.click(trigger);
            expect(await screen.findByText(bundle.sidebar.renameGroup)).toBeTruthy();
            expect(screen.getByText(bundle.sidebar.deleteGroup)).toBeTruthy();
            unmount();
        }
    });
});
