import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import i18n from "@/i18n";
import zh from "@/i18n/locales/zh-CN/activity.json";
import { SnapshotImage } from "./snapshot-image";

describe("SnapshotImage", () => {
    beforeEach(async () => {
        await i18n.changeLanguage("en");
    });

    it("shows a quiet placeholder instead of a broken image when the file is missing", () => {
        render(<SnapshotImage src="http://127.0.0.1:1/api/activity/snapshot?path=a.jpg" alt="Snapshot 1" showLabel />);

        fireEvent.error(screen.getByAltText("Snapshot 1"));

        expect(screen.queryByAltText("Snapshot 1")).toBeNull();
        expect(screen.getByRole("img", { name: "No snapshot available" })).toBeTruthy();
        expect(screen.getByText("No snapshot available")).toBeTruthy();
    });

    it("tries again when it moves to another frame", () => {
        const { rerender } = render(<SnapshotImage src="http://127.0.0.1:1/a.jpg" alt="frame" />);
        fireEvent.error(screen.getByAltText("frame"));
        expect(screen.queryByAltText("frame")).toBeNull();

        rerender(<SnapshotImage src="http://127.0.0.1:1/b.jpg" alt="frame" />);
        expect(screen.getByAltText("frame").getAttribute("src")).toBe("http://127.0.0.1:1/b.jpg");

        // Coming back retries: the file may have landed since.
        rerender(<SnapshotImage src="http://127.0.0.1:1/a.jpg" alt="frame" />);
        expect(screen.getByAltText("frame").getAttribute("src")).toBe("http://127.0.0.1:1/a.jpg");
    });

    it("shows the placeholder when there is no URL (screenshot marked missing)", () => {
        render(<SnapshotImage src={undefined} alt="frame" showLabel />);

        expect(screen.queryByAltText("frame")).toBeNull();
        expect(screen.getByRole("img", { name: "No snapshot available" })).toBeTruthy();
    });

    it("labels the placeholder in Chinese", async () => {
        await i18n.changeLanguage("zh-CN");
        render(<SnapshotImage src="http://127.0.0.1:1/a.jpg" alt="frame" showLabel />);

        fireEvent.error(screen.getByAltText("frame"));

        expect(screen.getByText(zh.session.noSnapshot)).toBeTruthy();
    });
});
