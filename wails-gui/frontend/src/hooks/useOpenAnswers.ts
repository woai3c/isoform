import { useCallback, useEffect, useSyncExternalStore } from "react";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { ListOpenAnswers } from "@/lib/wails-shim-app";
import { onPagePrivacyWithdrawal } from "@/lib/page-privacy-withdrawal";
import { parseOpenAnswer, type OpenAnswer } from "@/lib/open-answers";

let records: OpenAnswer[] = [];
let started = false;
let refreshAgain = false;
let currentRefresh: Promise<OpenAnswer[]> | null = null;
let generation = 0;
let writeVersion = 0;
const delivered = new Map<string, number>();
const listeners = new Set<() => void>();

interface OpenAnswersSnapshot {
    answers: OpenAnswer[];
    generation: number;
    delivered: ReadonlyMap<string, number>;
}

let snapshot: OpenAnswersSnapshot = {
    answers: records,
    generation,
    delivered: new Map(),
};

function subscribe(listener: () => void) {
    listeners.add(listener);
    return () => listeners.delete(listener);
}

function publishSnapshot() {
    snapshot = { answers: records, generation, delivered: new Map(delivered) };
    for (const listener of listeners) listener();
}

function publish(next: OpenAnswer[]) {
    records = next;
    publishSnapshot();
}

export function getOpenAnswersGeneration() {
    return generation;
}

export function beginOpenAnswerDelivery() {
    startStore();
    return generation;
}

export function registerDeliveredOpenAnswers(
    values: readonly unknown[],
    startedGeneration: number,
) {
    startStore();
    if (startedGeneration !== generation) return false;
    let changed = false;
    for (const value of values) {
        const answer = parseOpenAnswer(value);
        if (!answer || delivered.get(answer.id) === startedGeneration) continue;
        delivered.set(answer.id, startedGeneration);
        changed = true;
    }
    if (changed) publishSnapshot();
    return changed;
}

export async function refreshOpenAnswers(expectedGeneration = generation): Promise<OpenAnswer[]> {
    startStore();
    if (expectedGeneration !== generation) return records;
    if (currentRefresh) {
        refreshAgain = true;
        return currentRefresh;
    }
    const requestGeneration = generation;
    const requestWriteVersion = writeVersion;
    let request!: Promise<OpenAnswer[]>;
    request = Promise.resolve().then(async () => {
        try {
            if (requestGeneration !== generation) return records;
            if (requestWriteVersion !== writeVersion) {
                refreshAgain = true;
                return records;
            }
            const result = await ListOpenAnswers();
            if (requestGeneration !== generation) return records;
            if (requestWriteVersion !== writeVersion) {
                refreshAgain = true;
                return records;
            }
            const next = Array.isArray(result)
                ? result.map(parseOpenAnswer).filter((answer): answer is OpenAnswer => Boolean(answer))
                : [];
            registerDeliveredOpenAnswers(next, requestGeneration);
            publish(next);
            return next;
        } catch {
            return records;
        } finally {
            if (currentRefresh === request) {
                currentRefresh = null;
                if (refreshAgain) {
                    refreshAgain = false;
                    void refreshOpenAnswers(generation);
                }
            }
        }
    });
    currentRefresh = request;
    return request;
}

function resetForPrivacyWithdrawal() {
    generation += 1;
    delivered.clear();
    refreshAgain = false;
    currentRefresh = null;
    publish([]);
    void refreshOpenAnswers(generation);
}

function startStore() {
    if (started) return;
    started = true;
    EventsOn("open-answers:changed", () => void refreshOpenAnswers());
    onPagePrivacyWithdrawal(resetForPrivacyWithdrawal);
    EventsOn("session:deleted", resetForPrivacyWithdrawal);
}

export function useOpenAnswers() {
    startStore();
    const current = useSyncExternalStore(subscribe, () => snapshot, () => snapshot);
    const refresh = useCallback(() => refreshOpenAnswers(), []);
    useEffect(() => {
        if (records.length === 0) void refreshOpenAnswers();
    }, []);
    return {
        answers: current.answers,
        generation: current.generation,
        delivered: current.delivered,
        refresh,
    };
}

export function updateOpenAnswer(answer: OpenAnswer, expectedGeneration: number) {
    if (expectedGeneration !== generation) return false;
    writeVersion += 1;
    publish(records.some((entry) => entry.id === answer.id)
        ? records.map((entry) => entry.id === answer.id ? answer : entry)
        : [...records, answer]);
    return true;
}
