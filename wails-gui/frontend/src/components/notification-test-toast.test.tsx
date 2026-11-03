import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import en from "@/i18n/locales/en/settings.json";
import zhCN from "@/i18n/locales/zh-CN/settings.json";
import { notifyToast } from "@/lib/notify";
import { LongToastTestButton } from "./notification-test-toast";

vi.mock("@/lib/notify", () => ({ notifyToast: vi.fn() }));

describe("LongToastTestButton", () => {
    it.each([
        ["en", en.notifications],
        ["zh-CN", zhCN.notifications],
    ] as const)("uses localized, flavor-neutral sample text in %s", async (language, copy) => {
        vi.clearAllMocks();
        await i18n.changeLanguage(language);
        render(<LongToastTestButton />);
        fireEvent.click(screen.getByRole("button", { name: copy.testButton }));

        expect(notifyToast).toHaveBeenCalledWith(copy.longToast.sampleTitle, copy.longToast.sampleMessage, "error");
        expect(copy.longToast.sampleTitle).not.toContain(".yansu-agent");
        expect(copy.longToast.sampleMessage).not.toContain(".yansu-agent");
    });
});
