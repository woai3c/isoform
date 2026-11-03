import { afterEach, expect, it, vi } from "vitest";
import { GetNeedsYou } from "./wails-shim-app";

const originalGo = (window as any).go;
afterEach(() => { (window as any).go = originalGo; });

it("forwards the preview limit and retains the total and exact target", async () => {
    const page = { total: 4, items: [{ id: "moment", kind: "work_blocked", sentence: "A question is waiting", createdAt: 1, targetKind: "chat_item", targetId: "row", chatId: "main" }] };
    const binding = vi.fn().mockResolvedValue(page);
    (window as any).go = { main: { App: { GetNeedsYou: binding } } };
    await expect(GetNeedsYou(3)).resolves.toEqual(page);
    expect(binding).toHaveBeenCalledWith(3);
});
