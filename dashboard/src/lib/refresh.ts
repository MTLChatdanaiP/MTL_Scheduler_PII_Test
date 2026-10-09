import { get, type Readable } from "svelte/store";
import { autoRefreshEnabled } from "./stores";

// RFC-009 §17 / RFC-010 §27: the ONE way a data component keeps itself fresh.
//
//  - It reloads when its page's live trigger bumps a tick (an event of its category arrived, the stream came back after a gap, or the
//    visible bounded fallback is polling). That is what makes a screen live.
//  - It also reloads every `gaugeEveryMs` as a safety net. Some screens show values that change without any event (a worker's "last
//    heartbeat 12s ago", queue depth), and a missed event must not leave a screen wrong for ever. The period is slow (30s) unless the
//    screen shows such a gauge.
//  - Both respect the Auto-refresh toggle in the global bar.
//
// Components must not call setInterval themselves (a lint test enforces it); the three local-clock timers are the only exceptions.
export interface RefreshOptions {
    ticks?: Readable<number>[];
    gaugeEveryMs?: number; // default 30000
}

export function startRefresh(load: () => void, opts: RefreshOptions = {}): () => void {
    const unsubscribes = (opts.ticks ?? []).map((store) => {
        let first = true; // a store calls back once on subscribe; that is not a change
        return store.subscribe(() => {
            if (first) {
                first = false;
                return;
            }
            if (get(autoRefreshEnabled)) load();
        });
    });

    const timer = setInterval(() => {
        if (get(autoRefreshEnabled)) load();
    }, opts.gaugeEveryMs ?? 30_000);

    return () => {
        unsubscribes.forEach((u) => u());
        clearInterval(timer);
    };
}
