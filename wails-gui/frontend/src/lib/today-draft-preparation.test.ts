import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { TFunction } from "i18next";
import { TODAY_CHAT_PAUSED_REF, todayBriefCardRef, type TodayChatContextRef, type TodayChatPreview } from "./today-chat-navigation";
import { resetTodayChatDrafts } from "./today-chat-prepared";
import { prepareTodayDraft, type TodayDraftBox } from "./today-draft-preparation";

const prepare = vi.hoisted(() => vi.fn());
vi.mock("../../wailsjs/go/main/App", () => ({
    PrepareTodayChatDraft: (ref: unknown) => prepare(ref),
    PrepareTodayChatDrafts: vi.fn(),
}));

const t = ((key: string) => key) as unknown as TFunction<"today">;

/** A box that records what the flow told it, the way both real boxes do. */
function fakeBox(edits: { edited?: boolean; gone?: boolean } = {}) {
    const state = { text: "", context: undefined as TodayChatContextRef | undefined, preview: undefined as TodayChatPreview | undefined, cleared: false, cardGone: 0 };
    const box: TodayDraftBox = {
        fill: (text, context, preview) => { state.text = text; state.context = context; state.preview = preview; },
        update: (patch) => {
            if ("context" in patch) state.context = patch.context;
            if (patch.preview) state.preview = patch.preview;
            if (patch.text !== undefined) state.text = patch.text;
        },
        clear: () => { state.cleared = true; state.text = ""; state.context = undefined; state.preview = undefined; },
        stillApplies: () => !edits.gone,
        unedited: () => !edits.edited,
        onCardGone: () => { state.cardGone++; },
    };
    return { box, state };
}

const card = todayBriefCardRef("2026-10-22-morning", "hold", "Hold Thursday afternoon.");
const draft = { prefill: { kind: "brief_card", title: "Hold Thursday" }, counts: { memories: 1, activities: 2, people: 0 }, fingerprint: "fp-1", message: "Hold Thursday 1:00 to 4:00 PM." };

describe("prepareTodayDraft, shared by the main chat box and Today's panel", () => {
    beforeEach(() => {
        resetTodayChatDrafts();
        prepare.mockReset();
    });

    it("fills the card's words at once with the context line loading, then the computer's current words and the full reference", async () => {
        let settle: (value: unknown) => void = () => undefined;
        prepare.mockReturnValueOnce(new Promise((resolve) => { settle = resolve; }));
        const { box, state } = fakeBox();
        const done = prepareTodayDraft(box, "Hold Thursday afternoon.", card, {}, t, "en");
        expect(state).toMatchObject({ text: "Hold Thursday afternoon.", context: card, preview: { status: "loading" } });
        settle(draft);
        await done;
        expect(state.text).toBe("Hold Thursday 1:00 to 4:00 PM.");
        expect(state.context).toMatchObject({ sourceKind: "brief_card", sourceId: "hold", generatedTitle: "Hold Thursday", generatedFingerprint: "fp-1", generatedMessage: "Hold Thursday 1:00 to 4:00 PM." });
        expect(state.preview).toEqual({ status: "ready", counts: draft.counts });
    });

    it("keeps edited card words and the words they were shown, so Send still asks Go", async () => {
        prepare.mockResolvedValueOnce(draft);
        const { box, state } = fakeBox({ edited: true });
        await prepareTodayDraft(box, "Hold Thursday afternoon.", card, {}, t, "en");
        expect(state.text).toBe("Hold Thursday afternoon.");
        expect(state.context?.generatedMessage).toBe("Hold Thursday afternoon.");
        expect(state.context?.generatedFingerprint).toBe("fp-1");
    });

    it("checks the stored brief card when observation is paused", async () => {
        prepare.mockResolvedValueOnce(draft);
        const { box, state } = fakeBox();
        await prepareTodayDraft(box, "Hold Thursday afternoon.", card, { paused: true }, t, "en");
        expect(prepare).toHaveBeenCalledWith(card);
        expect(state.context).toMatchObject({ sourceKind: "brief_card", sourceId: "hold", briefId: card.briefId });
    });

    it("keeps non-brief paused drafts without reading their sources", async () => {
        const { box, state } = fakeBox();
        const ref = { sourceKind: "activity", sourceId: "other" };
        await prepareTodayDraft(box, "Help with this.", ref, { paused: true }, t, "en");
        expect(prepare).not.toHaveBeenCalled();
        expect(state).toMatchObject({ text: "Help with this.", context: TODAY_CHAT_PAUSED_REF, preview: { status: "paused" } });
    });

    it("clears untouched words of a card that left the brief and says so; edited ones keep the card", async () => {
        prepare.mockRejectedValue(new Error("TODAY_CHAT_CONTEXT_UNAVAILABLE"));
        const gone = fakeBox();
        await prepareTodayDraft(gone.box, "Hold Thursday afternoon.", card, {}, t, "en");
        expect(gone.state.cleared).toBe(true);
        expect(gone.state.cardGone).toBe(1);
        const edited = fakeBox({ edited: true });
        await prepareTodayDraft(edited.box, "Hold Thursday afternoon.", card, {}, t, "en");
        expect(edited.state).toMatchObject({ cleared: false, context: card, preview: { status: "unavailable" } });
    });

    it("drops a result once the box moved on", async () => {
        prepare.mockResolvedValueOnce(draft);
        const { box, state } = fakeBox({ gone: true });
        await prepareTodayDraft(box, "Hold Thursday afternoon.", card, {}, t, "en");
        expect(state.text).toBe("Hold Thursday afternoon.");
        expect(state.preview).toEqual({ status: "loading" });
    });
});

describe('brief chip verb parity', () => {
    it('mirrors every backend label verb', async () => {
        const { BRIEF_CARD_LABEL_VERBS } = await import('./today-draft-preparation');
        const go = readFileSync(resolve(__dirname, '../../../mobile_brief_cards.go'), 'utf8');
        const body = go.match(/var mobileBriefCardLabelVerbs = map\[string\]bool\{([^}]+)\}/)?.[1];
        expect(body).toBeTruthy();
        const backend = [...body!.matchAll(/"([a-z]+)":\s*true/g)].map((match) => match[1]).sort();
        expect([...BRIEF_CARD_LABEL_VERBS].sort()).toEqual(backend);
    });
});
