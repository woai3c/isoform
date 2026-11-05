import { noteBindingSource } from './page-switch-timing';

/** Renderer-only reads. Values never leave this process or confer authority. */
type Entry = {
    name: string;
    value?: unknown;
    ready: boolean;
    at: number;
    invalid: boolean;
    generation: number;
    pending?: Promise<unknown>;
    error?: unknown;
    settled?: boolean;
    load?: () => Promise<unknown>;
    ttl?: number;
    /** When the current or last read started. */
    startedAt?: number;
};

export class QueryCache {
    private entries = new Map<string, Entry>();
    private epoch = 0;
    private revisions = new Map<string, number>();
    private retained = new Map<string, number>();
    private retainedNames = new Map<string, number>();
    private listeners = new Set<(name: string, args?: readonly unknown[]) => void>();

    constructor(private readonly capacity = 64, private readonly now = () => Date.now()) {}
    private waitUntilActive?: () => Promise<void> | undefined;
    setActivityGate(gate: () => Promise<void> | undefined) { this.waitUntilActive = gate; }

    private key(name: string, args: readonly unknown[]) { return JSON.stringify([name, args]); }

    peek<T>(name: string, args: readonly unknown[] = []): T | undefined {
        const entry = this.entries.get(this.key(name, args));
        return entry?.ready ? entry.value as T : undefined;
    }

    subscribe(listener: (name: string, args?: readonly unknown[]) => void): () => void {
        this.listeners.add(listener);
        return () => { this.listeners.delete(listener); };
    }

    private notify(name: string, args?: readonly unknown[]) { for (const listener of [...this.listeners]) listener(name, args); }

    invalidate(names?: readonly string[], clear = true, args?: readonly unknown[]) {
        const changed = new Set<string>(names);
        if (!names) this.epoch++;
        else for (const name of names) this.revisions.set(name, (this.revisions.get(name) ?? 0) + 1);
        for (const [key, entry] of this.entries) {
            if (args && key !== this.key(entry.name, args)) continue;
            if (names && !names.includes(entry.name)) continue;
            entry.invalid = true;
            if (clear) { entry.ready = false; entry.value = undefined; }
            entry.error = undefined;
            entry.settled = false;
            entry.generation++;
            entry.pending = undefined;
            changed.add(entry.name);
        }
        // Subscribers that filter by argument need the arguments of a targeted invalidation.
        for (const name of changed) args ? this.notify(name, args) : this.notify(name);
    }

    state(name: string, args: readonly unknown[] = []) { return this.entries.get(this.key(name, args)); }

    retain(name: string, args: readonly unknown[]) {
        const key = this.key(name, args);
        this.retained.set(key, (this.retained.get(key) ?? 0) + 1);
        return () => {
            const count = (this.retained.get(key) ?? 1) - 1;
            if (count > 0) this.retained.set(key, count);
            else this.retained.delete(key);
        };
    }

    retainNames(names: readonly string[]) {
        for (const name of names) this.retainedNames.set(name, (this.retainedNames.get(name) ?? 0) + 1);
        return () => {
            for (const name of names) {
                const count = (this.retainedNames.get(name) ?? 1) - 1;
                if (count) this.retainedNames.set(name, count);
                else this.retainedNames.delete(name);
            }
            this.trim();
        };
    }

    set<T>(name: string, args: readonly unknown[], value: T) {
        const key = this.key(name, args);
        const old = this.entries.get(key);
        if (old) old.generation++;
        this.entries.delete(key);
        this.entries.set(key, { load: old?.load, ttl: old?.ttl, name, value, ready: true, at: this.now(), invalid: false, generation: (old?.generation ?? 0) + 1, settled: true });
        this.trim();
        this.notify(name, args);
    }

    private trim() {
        while (this.entries.size > this.capacity) {
            const oldest = [...this.entries].find(([key, entry]) => !this.retained.has(key) && !this.retainedNames.has(entry.name) && !entry.pending)?.[0];
            if (!oldest) break;
            this.entries.delete(oldest);
        }
    }

    refreshSubscribers(names: readonly string[]) { for (const name of names) this.notify(name); }

    revalidate(names?: readonly string[]) {
        for (const [key, entry] of [...this.entries]) {
            if (!entry.load || (names && !names.includes(entry.name))) continue;
            const [, args] = JSON.parse(key);
            void this.read(entry.name, args, entry.load, entry.ttl).catch(() => {});
        }
    }

    clear() { this.invalidate(); this.entries.clear(); }
    get size() { return this.entries.size; }

    read<T>(name: string, args: readonly unknown[], load: () => Promise<T>, ttl = 60_000): Promise<T> {
        const key = this.key(name, args);
        // A ready value (fresh, or stale while it revalidates) is what the
        // page paints first; anything else waits on the loader.
        noteBindingSource(name, this.entries.get(key)?.ready ? 'cache' : 'network');
        if (name === 'LoadStreamItemsPage' && Number(args[1]) >= 0) {
            const secondPage = [...this.entries].some(([candidate, entry]) => {
                const [binding, params] = JSON.parse(candidate);
                return binding === name && params[0] === args[0] && params[1] === -1 && params[2] === args[2]
                    && (entry.value as { nextCursor?: number } | undefined)?.nextCursor === args[1];
            });
            if (!secondPage) return load();
        }
        let entry = this.entries.get(key);
        if (!entry) {
            entry = { name, ready: false, at: 0, invalid: false, generation: 0 };
        }
        this.entries.delete(key);
        this.entries.set(key, entry);
        // Keep at most two stream pages per session, including alternate page
        // sizes. Older history still reads normally; it simply leaves the LRU.
        if (name === 'LoadStreamItemsPage') {
            const pages = [...this.entries.keys()].filter((candidate) => {
                const [binding, params] = JSON.parse(candidate);
                return binding === name && params[0] === args[0];
            });
            while (pages.length > 2) this.entries.delete(pages.shift()!);
        }
        entry.load = load;
        entry.ttl = ttl;
        if (entry.ready && !entry.invalid && this.now() - entry.at < ttl) return Promise.resolve(entry.value as T);
        const target = entry;
        if (!target.pending) {
            target.startedAt = this.now();
            const generation = target.generation;
            const epoch = this.epoch;
            const revision = this.revisions.get(name);
            const latest = (): Promise<T> => {
                const current = this.entries.get(key);
                // A binding-wide revision can supersede another argument's
                // in-flight read. Never join this promise to itself.
                if (current?.pending === pending) {
                    current.pending = undefined;
                    current.generation++;
                    return this.read(name, args, load, ttl);
                }
                if (current?.pending) return current.pending as Promise<T>;
                if (current?.ready && !current.invalid) return Promise.resolve(current.value as T);
                if (current?.settled && !current.invalid) return Promise.reject(current.error);
                return this.read(name, args, load, ttl);
            };
            const superseded = () => target.generation !== generation || epoch !== this.epoch || revision !== this.revisions.get(name);
            const pending: Promise<T> = Promise.resolve().then(() => {
                const wait = this.waitUntilActive?.();
                if (wait) return wait.then(() => !superseded() ? load() : latest());
                return !superseded() ? load() : latest();
            }).then((value) => {
                // An event during a read supersedes that read for ALL callers,
                // including callers awaiting the old promise.
                if (superseded()) return latest();
                target.value = value;
                target.ready = true;
                target.invalid = false;
                target.at = this.now();
                target.pending = undefined;
                target.error = undefined;
                target.settled = true;
                this.notify(name, args);
                this.trim();
                return value;
            }, (error) => {
                if (superseded()) return latest();
                target.pending = undefined;
                target.invalid = false;
                target.error = error;
                target.settled = true;
                throw error;
            });
            target.pending = pending;
        }
        this.trim();
        if (target.ready && !target.invalid) {
            // Stale-while-revalidate failures retain the last successful value.
            void target.pending.catch(() => {});
            return Promise.resolve(target.value as T);
        }
        return target.pending as Promise<T>;
    }
}

export const pageQueryCache = new QueryCache();
