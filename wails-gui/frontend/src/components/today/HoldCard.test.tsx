import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, expect, it, vi } from "vitest";
import i18n from "@/i18n";
import { HoldCard } from "./HoldCard";

const mocks = vi.hoisted(() => ({ record: vi.fn(async () => undefined), settings: vi.fn(), propose: vi.fn(), shape: vi.fn(), hold: vi.fn(), status: vi.fn(), undo: vi.fn(), remove: vi.fn(), restore: vi.fn(), changeCalendar: vi.fn(), dismiss: vi.fn(), undoDismiss: vi.fn(), openSettings: vi.fn(), openCalendar: vi.fn(), openURL: vi.fn(), free: vi.fn() }));
vi.mock("@/lib/wails-shim-app", () => ({ RecordTodayAction: mocks.record, OpenPermissionSettings: mocks.openSettings, OpenExternalURL: mocks.openCalendar, DismissHoldOffer: mocks.dismiss, UndoDismissHoldOffer: mocks.undoDismiss, GetHoldSettings: mocks.settings, ProposeHold: mocks.propose, GetHoldDayShape: mocks.shape, HoldTime: mocks.hold, GetHoldStatus: mocks.status, UndoHold: mocks.undo, RemoveHold: mocks.remove, UndoRemoveHold: mocks.restore, SetHoldCalendar: mocks.changeCalendar, GetFindTimeDetails: async () => ({ ranges: await mocks.free(), checkedCalendars: [], browserOnly: [] }) }));
vi.mock("@/lib/wails-shim-runtime", () => ({ BrowserOpenURL: mocks.openURL, EventsOn: () => () => undefined }));
vi.mock("./TodayDayStrip", () => ({ TodayDayStrip: ({ shape }: { shape: { hold: { start: string } } }) => <div data-testid="hold-strip">{shape.hold?.start}</div> }));
const source = { thing: { title: "Q4 plan", sourceKind: "brief", sourceId: "commitments:q4" }, reason: "Due Monday." };
const first = { start: "2026-11-02T10:00:00Z", end: "2026-11-02T11:00:00Z" };
const second = { start: "2026-11-02T11:00:00Z", end: "2026-11-02T12:00:00Z" };
const proposal = { id: "hold-1", thing: source.thing, calendarId: "local", slots: [first, second] };
beforeEach(async () => {
    vi.resetAllMocks(); mocks.status.mockResolvedValue(undefined); await i18n.changeLanguage("en");
    mocks.settings.mockResolvedValue({ granted: true, offerHolds: true, calendarId: "local", calendars: [{ id: "local", title: "Home", sourceId: "local", sourceTitle: "On My Mac", sourceType: "local", isDefault: true }] });
    mocks.propose.mockResolvedValue(proposal);
    mocks.shape.mockResolvedValue({ calendarConnected: true, rangeStart: first.start, rangeEnd: second.end, now: first.start, events: [], hold: null });
    mocks.hold.mockResolvedValue({ id: "hold-1", state: "pending_write", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "2026-11-02T09:00:05Z", removedAt: "" });
    mocks.undo.mockResolvedValue(undefined);
    mocks.dismiss.mockResolvedValue("dismissal-id");
    mocks.undoDismiss.mockResolvedValue(undefined);
});
it.each(["en", "zh-CN"])("offers a specific time and cancels before the write in %s", async (language) => {
    await i18n.changeLanguage(language);
    const close = vi.fn();
    render(<HoldCard source={source} onClose={close} />);
    const card = await screen.findByTestId("hold-card");
    expect(card).toHaveTextContent("Q4 plan");
    expect(card).toHaveTextContent(language === "en" ? "Due Monday. · From your brief" : "Due Monday. · 来自你的简报");
    expect(await screen.findByTestId("hold-strip")).toHaveTextContent(first.start);
    fireEvent.click(screen.getByRole("button", { name: language === "en" ? /^Hold / : /^留出/ }));
    await waitFor(() => expect(mocks.hold).toHaveBeenCalledWith(proposal, first, "local"));
    fireEvent.click(await screen.findByRole("button", { name: language === "en" ? "Undo" : "撤销" }));
    await waitFor(() => expect(mocks.undo).toHaveBeenCalledWith("hold-1"));
    expect(close).toHaveBeenCalled();
});
it("uses a selected alternative and does not write on selection", async () => {
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: "Other times" }));
    fireEvent.click(screen.getByRole("button", { name: /^Monday/ }));
    expect(mocks.hold).not.toHaveBeenCalled();
    expect(screen.getByRole("button", { name: /^Monday/ })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    await waitFor(() => expect(mocks.hold).toHaveBeenCalledWith(proposal, second, "local"));
});
it("shows shared busy only for the meetings account", async () => {
    mocks.settings.mockResolvedValue({ granted: true, offerHolds: true, calendarId: "local", calendars: [{ id: "local", title: "Work", sourceId: "work", sourceTitle: "Work", sourceType: "exchange", isDefault: true, meetingsAccount: true }] });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    expect(await screen.findByText(/Others see you as busy once it's held/)).toBeInTheDocument();
});
it("passes a supplied work estimate to the proposal", async () => {
    render(<HoldCard source={{ ...source, lengthMinutes: 90 }} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    expect(mocks.propose).toHaveBeenCalledWith(source.thing, "", 90);
});
it("reads the strip for an alternative beyond tomorrow", async () => {
    const later = { start: "2026-11-05T10:00:00Z", end: "2026-11-05T11:00:00Z" };
    mocks.propose.mockResolvedValue({ ...proposal, slots: [first, later] });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: "Other times" }));
    fireEvent.click(screen.getByRole("button", { name: /^Thursday/ }));
    await waitFor(() => expect(mocks.shape).toHaveBeenCalledWith("2026-11-05"));
    expect(mocks.hold).not.toHaveBeenCalled();
});
it("keeps the offered slot, and explains when it is no longer available", async () => {
    const { unmount } = render(<HoldCard source={{ ...source, preferred: second }} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    await waitFor(() => expect(mocks.hold).toHaveBeenCalledWith(proposal, second, "local"));
    unmount();
    mocks.propose.mockResolvedValue({ ...proposal, slots: [first] });
    render(<HoldCard source={{ ...source, preferred: second }} onClose={vi.fn()} />);
    expect(await screen.findByText(/That time is no longer offered/)).toBeInTheDocument();
});
it("answers in words when offers are off", async () => {
    mocks.settings.mockResolvedValue({ granted: true, offerHolds: false, calendarId: "", calendars: [] });
    mocks.free.mockResolvedValue([first]);
    render(<HoldCard source={source} onClose={vi.fn()} />);
    expect(await screen.findByText(/You're free/)).toBeInTheDocument();
    expect(mocks.propose).not.toHaveBeenCalled();
    expect(mocks.hold).not.toHaveBeenCalled();
});
it("answers in words instead of offering a hold when a browser calendar is uncovered", async () => {
    mocks.settings.mockResolvedValue({ granted: true, offerHolds: true, calendarId: "local", calendars: [], browserOnly: ["google"] });
    mocks.free.mockResolvedValue([first]);
    render(<HoldCard source={source} onClose={vi.fn()} />);
    expect(await screen.findByText(/You're free/)).toBeInTheDocument();
    expect(mocks.propose).not.toHaveBeenCalled();
    expect(mocks.hold).not.toHaveBeenCalled();
});
it("does not mention holds when calendar access is off", async () => {
    mocks.settings.mockResolvedValue({ granted: false, offerHolds: true, calendarId: "", calendars: [] });
    mocks.free.mockRejectedValue(new Error("unavailable"));
    render(<HoldCard source={source} onClose={vi.fn()} />);
    expect(await screen.findByText("Couldn't check your calendar. Try again later.")).toBeInTheDocument();
    expect(screen.queryByTestId("hold-card")).not.toBeInTheDocument();
    expect(mocks.propose).not.toHaveBeenCalled();
});

it.each(["en", "zh-CN"])("lets a person choose an available calendar when there is no default in %s", async (language) => {
    await i18n.changeLanguage(language);
    const calendar = { id: "local", title: "Home", sourceId: "local", sourceTitle: "On My Mac", sourceType: "local", isDefault: false };
    const unselected = { granted: true, offerHolds: true, calendarId: "", calendars: [calendar] };
    mocks.settings.mockResolvedValue(unselected);
    mocks.propose.mockRejectedValueOnce(new Error("no calendar")).mockResolvedValue(proposal);
    mocks.changeCalendar.mockImplementation(async () => { mocks.settings.mockResolvedValue({ ...unselected, calendarId: "local" }); });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    const picker = await screen.findByRole("combobox", { name: language === "en" ? "Holds go to" : "时间块放在" });
    expect(screen.queryByText(language === "en" ? "No calendar is available for holds." : "没有可用于留出时间的日历。")).not.toBeInTheDocument();
    expect(mocks.hold).not.toHaveBeenCalled();
    fireEvent.change(picker, { target: { value: "local" } });
    await waitFor(() => expect(mocks.changeCalendar).toHaveBeenCalledWith("local"));
    expect(await screen.findByTestId("hold-card")).toBeInTheDocument();
    expect(mocks.hold).not.toHaveBeenCalled();
});

it("keeps the calendar picker usable after a selection fails", async () => {
    const calendar = { id: "local", title: "Home", sourceId: "local", sourceTitle: "On My Mac", sourceType: "local", isDefault: false };
    const unselected = { granted: true, offerHolds: true, calendarId: "", calendars: [calendar] };
    mocks.settings.mockResolvedValue(unselected);
    mocks.propose.mockRejectedValueOnce(new Error("no calendar")).mockResolvedValue(proposal);
    mocks.changeCalendar.mockRejectedValueOnce(new Error("offline")).mockImplementationOnce(async () => { mocks.settings.mockResolvedValue({ ...unselected, calendarId: "local" }); });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    const picker = await screen.findByRole("combobox", { name: "Holds go to" });
    fireEvent.change(picker, { target: { value: "local" } });
    expect(await screen.findByText("Couldn't change the calendar. Try again.")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Holds go to" })).toBeInTheDocument();
    fireEvent.change(picker, { target: { value: "local" } });
    expect(await screen.findByTestId("hold-card")).toBeInTheDocument();
    expect(mocks.changeCalendar).toHaveBeenCalledTimes(2);
    expect(mocks.hold).not.toHaveBeenCalled();
});

it("sets aside an offer for the day and can undo it", async () => {
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: "Not now" }));
    expect(await screen.findByText(/Not now · Q4 plan/)).toBeInTheDocument();
    expect(mocks.dismiss).toHaveBeenCalledWith(source.thing);
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    await waitFor(() => expect(mocks.undoDismiss).toHaveBeenCalledWith("dismissal-id"));
    expect(await screen.findByTestId("hold-card")).toBeInTheDocument();
    expect(mocks.hold).not.toHaveBeenCalled();
});

it("does not show an offer again after it was set aside", async () => {
    mocks.propose.mockResolvedValue({ dismissed: true, slots: [] });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    expect(await screen.findByText("Not now")).toBeInTheDocument();
    expect(screen.queryByTestId("hold-card")).not.toBeInTheDocument();
});

it("explains how to remove a hold when calendar access has been revoked", async () => {
    mocks.hold.mockResolvedValue({ id: "hold-1", state: "held", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" });
    mocks.remove.mockRejectedValue(new Error("denied"));
    mocks.settings.mockResolvedValueOnce({ granted: true, offerHolds: true, calendarId: "local", calendars: [{ id: "local", title: "Home", sourceId: "local", sourceTitle: "On My Mac", sourceType: "local", isDefault: true }] }).mockResolvedValueOnce({ granted: false, offerHolds: true, calendarId: "", calendars: [] });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    fireEvent.click(await screen.findByRole("button", { name: "Remove the hold" }));
    expect(await screen.findByText(/can't reach your calendar/)).toBeInTheDocument();
    expect(mocks.openSettings).not.toHaveBeenCalled();
    expect(mocks.openCalendar).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Open System Settings" }));
    expect(mocks.openSettings).toHaveBeenCalledWith("calendars");
});
it("offers Calendar and another attempt after a removal failure", async () => {
    mocks.hold.mockResolvedValue({ id: "hold-1", state: "held", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" });
    mocks.remove.mockRejectedValueOnce(new Error("offline"));
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    fireEvent.click(await screen.findByRole("button", { name: "Remove the hold" }));
    expect(await screen.findByText(/Couldn't remove it/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Open in Calendar" }));
    expect(mocks.openCalendar).toHaveBeenCalledWith("/System/Applications/Calendar.app");
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
});
it("replaces the removal error after a second failure and keeps retry", async () => {
    mocks.hold.mockResolvedValue({ id: "hold-1", state: "held", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" });
    mocks.remove.mockRejectedValue(new Error("offline"));
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    fireEvent.click(await screen.findByRole("button", { name: "Remove the hold" }));
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Still didn't work.")).toBeInTheDocument();
    expect(screen.queryByText(/Couldn't remove it/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send a report" })).not.toBeInTheDocument();
});
it("sends an event with unsupported edits to Calendar without removing it", async () => {
    mocks.hold.mockResolvedValue({ id: "hold-1", state: "held", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" });
    mocks.remove.mockRejectedValue(new Error("HOLD_CALENDAR_EDITED"));
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    fireEvent.click(await screen.findByRole("button", { name: "Remove the hold" }));
    expect(await screen.findByText("You changed it in Calendar, so I left it there.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Open in Calendar" }));
    expect(mocks.openCalendar).toHaveBeenCalledWith("/System/Applications/Calendar.app");
    expect(mocks.restore).not.toHaveBeenCalled();
});

it("can retry a failed Undo of removal", async () => {
    const now = Date.now();
    mocks.hold.mockResolvedValue({ id: "hold-1", state: "held", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" });
    mocks.remove.mockResolvedValue(undefined);
    mocks.status.mockResolvedValue({ id: "hold-1", state: "removed", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: new Date(now).toISOString() });
    mocks.restore.mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(undefined);
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    fireEvent.click(await screen.findByRole("button", { name: "Remove the hold" }));
    fireEvent.click(await screen.findByRole("button", { name: "Undo" }));
    expect(await screen.findByText(/Couldn't put it back/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(mocks.restore).toHaveBeenCalledTimes(2));
});
it("replaces the restore error after a second failure", async () => {
    const now = Date.now();
    mocks.hold.mockResolvedValue({ id: "hold-1", state: "held", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" });
    mocks.remove.mockResolvedValue(undefined);
    mocks.status.mockResolvedValue({ id: "hold-1", state: "removed", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: new Date(now).toISOString() });
    mocks.restore.mockRejectedValue(new Error("offline"));
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    fireEvent.click(await screen.findByRole("button", { name: "Remove the hold" }));
    fireEvent.click(await screen.findByRole("button", { name: "Undo" }));
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Still didn't work.")).toBeInTheDocument();
    expect(screen.queryByText(/Couldn't put it back/)).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
});
it("reopens a held event rather than offering another one", async () => {
    mocks.propose.mockResolvedValue({ ...proposal, existing: { id: "hold-1", state: "held", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" } });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    expect(await screen.findByRole("button", { name: "Remove the hold" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /^Hold / })).not.toBeInTheDocument();
    expect(mocks.hold).not.toHaveBeenCalled();
});

it("directs an unconfirmed write to Calendar without trying another write", async () => {
    mocks.propose.mockResolvedValue({ ...proposal, existing: { id: "hold-1", state: "failed", failureReason: "unconfirmed", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" } });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    expect(await screen.findByText("Couldn't confirm the hold. Open in Calendar.")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Try again" })).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Open in Calendar" }));
    expect(mocks.openCalendar).toHaveBeenCalled();
    expect(mocks.hold).not.toHaveBeenCalled();
});

it("closes the card when the hold starts without removing the event", async () => {
    const now = vi.spyOn(Date, "now").mockReturnValue(Date.parse(first.start));
    const close = vi.fn();
    mocks.propose.mockResolvedValue({ ...proposal, existing: { id: "hold-1", state: "held", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" } });
    render(<HoldCard source={source} onClose={close} />);
    await waitFor(() => expect(close).toHaveBeenCalled());
    expect(mocks.remove).not.toHaveBeenCalled();
    now.mockRestore();
});

it("follows the moved event's day and leaves Remove available", async () => {
    const moved = { start: "2026-11-03T10:00:00Z", end: "2026-11-03T11:00:00Z" };
    mocks.propose.mockResolvedValue({ ...proposal, existing: { id: "hold-1", state: "moved", calendarId: "local", title: "Q4 plan", ...moved, writeAfter: "", removedAt: "" } });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    expect(await screen.findByText(/Moved to .* in Calendar/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Remove the hold" })).toBeInTheDocument();
    await waitFor(() => expect(mocks.shape).toHaveBeenCalledWith("2026-11-03"));
});

it("offers another checked time if the original fills", async () => {
    mocks.hold.mockResolvedValueOnce({ state: "conflict", ...second });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    expect(await screen.findByText(/That time just filled up/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    await waitFor(() => expect(mocks.hold).toHaveBeenLastCalledWith(proposal, second, "local"));
});
it("asks before switching to a different writable calendar", async () => {
    mocks.hold.mockResolvedValueOnce({ state: "calendar_unavailable", calendarId: "replacement" });
    mocks.settings.mockResolvedValueOnce({ granted: true, offerHolds: true, calendarId: "local", calendars: [{ id: "local", title: "Home", sourceId: "local", sourceTitle: "On My Mac", sourceType: "local", isDefault: true }] }).mockResolvedValueOnce({ granted: true, offerHolds: true, calendarId: "replacement", calendars: [{ id: "replacement", title: "Personal", sourceId: "local", sourceTitle: "On My Mac", sourceType: "local", isDefault: true }] });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    expect(await screen.findByText(/Home calendar can't be changed/)).toBeInTheDocument();
    expect(mocks.hold).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Hold on Personal" }));
    await waitFor(() => expect(mocks.hold).toHaveBeenLastCalledWith(proposal, first, "replacement"));
});

it("asks before using another calendar after EventKit refuses a write", async () => {
    const replacement = { id: "other", title: "Personal", sourceId: "other", sourceTitle: "Personal", sourceType: "local", isDefault: true };
    const original = { id: "local", title: "Home", sourceId: "local", sourceTitle: "On My Mac", sourceType: "local", isDefault: false };
    mocks.settings.mockResolvedValue({ granted: true, offerHolds: true, calendarId: "local", calendars: [original, replacement] });
    mocks.hold.mockResolvedValueOnce({ id: "hold-1", state: "failed", failureReason: "calendar_unavailable", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" });
    mocks.propose.mockResolvedValueOnce(proposal).mockResolvedValueOnce({ ...proposal, id: "hold-2" });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    expect(await screen.findByText(/Home calendar can't be changed/)).toBeInTheDocument();
    expect(mocks.hold).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Hold on Personal" }));
    await waitFor(() => expect(mocks.hold).toHaveBeenLastCalledWith({ ...proposal, id: "hold-2" }, first, "other"));
});

it("rechecks and retries a definite write failure on Try again", async () => {
    const failed = { id: "hold-1", state: "failed", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" };
    mocks.hold.mockResolvedValueOnce(failed).mockResolvedValueOnce({ ...failed, id: "hold-2", state: "pending_write" });
    mocks.propose.mockResolvedValueOnce(proposal).mockResolvedValueOnce({ ...proposal, id: "hold-2" });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    await waitFor(() => expect(mocks.hold).toHaveBeenLastCalledWith({ ...proposal, id: "hold-2" }, first, "local"));
    expect(mocks.hold).toHaveBeenCalledTimes(2);
});

it("offers another retry without a report link after a second failure", async () => {
    const failed = { id: "hold-1", state: "failed", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" };
    mocks.hold.mockResolvedValueOnce(failed).mockResolvedValueOnce({ ...failed, id: "hold-2" });
    mocks.propose.mockResolvedValueOnce(proposal).mockResolvedValueOnce({ ...proposal, id: "hold-2" });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    expect(await screen.findByText("Still didn't work.")).toBeInTheDocument();
    expect(screen.queryByText("Couldn't hold it. Try again.")).not.toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Try again" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Send a report" })).not.toBeInTheDocument();
    expect(mocks.openURL).not.toHaveBeenCalled();
});

it("does not retry on a different time without another Hold", async () => {
    const failed = { id: "hold-1", state: "failed", calendarId: "local", title: "Q4 plan", ...first, writeAfter: "", removedAt: "" };
    mocks.hold.mockResolvedValueOnce(failed);
    mocks.propose.mockResolvedValueOnce(proposal).mockResolvedValueOnce({ ...proposal, id: "hold-2", slots: [second] });
    render(<HoldCard source={source} onClose={vi.fn()} />);
    await screen.findByTestId("hold-card");
    fireEvent.click(screen.getByRole("button", { name: /^Hold / }));
    fireEvent.click(await screen.findByRole("button", { name: "Try again" }));
    expect(await screen.findByText(/That time is no longer offered/)).toBeInTheDocument();
    expect(mocks.hold).toHaveBeenCalledTimes(1);
});


it.each(["T14", "T84", "T31", "D01"] as const)("records hold and undo once for %s without the title", async (action) => {
    render(<HoldCard source={{ ...source, telemetryAction: action }} onClose={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: /^Hold / }));
    await screen.findByRole("button", { name: "Undo" });
    expect(mocks.record).toHaveBeenCalledTimes(1);
    expect(mocks.record).toHaveBeenLastCalledWith("today", action, "reach", "done", expect.any(Number));
    fireEvent.click(screen.getByRole("button", { name: "Undo" }));
    await waitFor(() => expect(mocks.record).toHaveBeenCalledTimes(2));
    expect(mocks.record).toHaveBeenLastCalledWith("today", action, "reach", "undone", expect.any(Number));
    expect(JSON.stringify(mocks.record.mock.calls)).not.toContain("Q4 plan");
});

it("records a failed hold once without the provider error or title", async () => {
    mocks.hold.mockRejectedValueOnce(new Error("private provider details"));
    render(<HoldCard source={{ ...source, telemetryAction: "T14" }} onClose={vi.fn()} />);
    fireEvent.click(await screen.findByRole("button", { name: /^Hold / }));
    await waitFor(() => expect(mocks.record).toHaveBeenCalledTimes(1));
    expect(mocks.record).toHaveBeenCalledWith("today", "T14", "reach", "failed", expect.any(Number));
    expect(JSON.stringify(mocks.record.mock.calls)).not.toMatch(/private provider|Q4 plan/);
});
