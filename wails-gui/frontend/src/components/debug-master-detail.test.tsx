import { fireEvent, render, screen } from "@testing-library/react";
import { useEffect, useState } from "react";
import { describe, expect, it, vi } from "vitest";
import {
    DebugMasterDetail,
    debugDetailOwnsEscape,
} from "./debug-master-detail";

function Harness({ onSettingsClose }: { onSettingsClose: () => void }) {
    const [detailOpen, setDetailOpen] = useState(true);

    useEffect(() => {
        const closeSettings = (event: KeyboardEvent) => {
            if (event.key === "Escape" && !event.defaultPrevented) {
                onSettingsClose();
            }
        };
        window.addEventListener("keydown", closeSettings);
        return () => window.removeEventListener("keydown", closeSettings);
    }, [onSettingsClose]);

    return (
        <DebugMasterDetail
            list={<div>Profile list</div>}
            detail={<button type="button">Profile detail action</button>}
            detailKey="profile-1"
            compactDetailOpen={detailOpen}
            onBack={() => setDetailOpen(false)}
            listLabel="Work Profile revisions"
            detailLabel="Work Profile revision details"
        />
    );
}

describe("DebugMasterDetail", () => {
    it("consumes Escape before the containing Settings surface can close", () => {
        const onSettingsClose = vi.fn();
        render(<Harness onSettingsClose={onSettingsClose} />);

        const detail = screen.getByRole("region", {
            name: "Work Profile revision details",
        });
        const action = screen.getByRole("button", {
            name: "Profile detail action",
        });
        action.focus();

        expect(debugDetailOwnsEscape()).toBe(true);
        fireEvent.keyDown(detail, { key: "Escape" });

        expect(onSettingsClose).not.toHaveBeenCalled();
        expect(debugDetailOwnsEscape()).toBe(false);
        expect(
            screen.getByRole("region", { name: "Work Profile revisions" }),
        ).toHaveClass("block");
        expect(detail).toHaveClass("hidden", "xl:block");
    });
});
