import { useEffect, useRef, useState } from "react";
import { GetHoldSettings, GetHoldStatus, HoldTime, ProposeHold, RemoveHold, UndoHold, UndoRemoveHold, type HoldStatus } from "@/lib/wails-shim-app";
import type { HoldCardSource } from "./HoldCard";

/** Submit only the exact slot the person saw; Go rechecks availability before writing. */
export function useDirectHold(source: HoldCardSource | null) {
    const [status, setStatus] = useState<HoldStatus | null>(null);
    const [checking, setChecking] = useState(false);
    const [failed, setFailed] = useState(false);
    const [clock, setClock] = useState(Date.now());
    const generation = useRef(0);
    const busy = useRef(false);
    const revision = useRef(0);
    useEffect(() => {
        generation.current++; busy.current = false;
        setStatus(null); setChecking(false); setFailed(false);
        return () => { generation.current++; };
    }, [source?.thing.sourceKind, source?.thing.sourceId, source?.thing.title, source?.preferred?.start, source?.preferred?.end]);
    useEffect(() => {
        if (!status || !["pending_write", "checking", "held", "moved", "removed"].includes(status.state)) return;
        let active = true;
        const timer = window.setInterval(() => {
            setClock(Date.now());
            const current = revision.current;
            void GetHoldStatus(status.id).then((next) => { if (active && !busy.current && revision.current === current) setStatus(next); }).catch(() => { if (active && revision.current === current) setFailed(true); });
        }, 1000);
        return () => { active = false; window.clearInterval(timer); };
    }, [status?.id, status?.state]);
    const hold = async () => {
        if (!source?.preferred || busy.current) return false;
        busy.current = true; revision.current++;
        const turn = generation.current;
        setChecking(true); setFailed(false);
        try {
            const settings = await GetHoldSettings();
            const proposal = await ProposeHold(source.thing, source.due ?? "", source.lengthMinutes ?? 0);
            const slot = proposal.slots.find((item) => item.start === source.preferred?.start && item.end === source.preferred?.end);
            if (turn !== generation.current) return false;
            if (!settings.granted || !settings.offerHolds || !settings.calendarId || settings.browserOnly?.length || !slot || proposal.dismissed || proposal.existing) throw new Error("offer changed");
            const next = await HoldTime(proposal, slot, settings.calendarId);
            if (!["pending_write", "checking", "held"].includes(next.state)) throw new Error("hold unavailable");
            if (turn === generation.current) { setClock(Date.now()); setStatus(next); }
            return true;
        } catch { if (turn === generation.current) setFailed(true); return false; }
        finally { if (turn === generation.current) { busy.current = false; setChecking(false); } }
    };
    const undo = async () => {
        if (!status || busy.current) return;
        busy.current = true; revision.current++;
        try { await UndoHold(status.id); setStatus(null); setFailed(false); }
        catch { setFailed(true); }
        finally { busy.current = false; }
    };
    const remove = async () => {
        if (!status || busy.current) return;
        if (status.state === "pending_write") { await undo(); return; }
        busy.current = true; revision.current++;
        try { await RemoveHold(status.id); setStatus(await GetHoldStatus(status.id)); setClock(Date.now()); setFailed(false); }
        catch { setFailed(true); }
        finally { busy.current = false; }
    };
    const restore = async () => {
        if (!status || busy.current) return;
        busy.current = true; revision.current++;
        try { await UndoRemoveHold(status.id); setStatus(await GetHoldStatus(status.id)); setFailed(false); }
        catch { setFailed(true); }
        finally { busy.current = false; }
    };
    const held = !!status && ["pending_write", "held", "moved"].includes(status.state);
    const canUndo = status?.state === "pending_write" && clock < Date.parse(status.writeAfter);
    const removed = status?.state === "removed";
    const canRestore = removed && clock < Date.parse(status.removedAt) + 5000;
    return { checking: checking || status?.state === "checking", status, held, canUndo, removed, canRestore, failed: failed || status?.state === "failed", hold, undo, remove, restore };
}
