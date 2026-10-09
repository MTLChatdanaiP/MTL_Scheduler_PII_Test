import { onMount, onDestroy } from "svelte";
import type { Writable } from "svelte/store";
import { startLiveRefreshTrigger } from "./liveRefreshTrigger";

// RFC-009 §17: one live connection per PAGE. A page calls usePageLive once; every data component on it listens to the tick store.
// `scope` is the RFC-010 §15 scope the server filters by; `prefixes` are the same event families, checked again client-side.
// The fallback is on for every page: if the stream goes away the page polls visibly (banner in the global bar), boundedly, and
// refetches once when the stream comes back.
export function usePageLive(opts: { scope: string; prefixes: string[]; tick: Writable<number> }) {
    let stop: (() => void) | undefined;

    onMount(() => {
        stop = startLiveRefreshTrigger({
            prefixes: opts.prefixes,
            scopes: [opts.scope],
            onMatch: () => opts.tick.update((n) => n + 1),
            fallback: {},
        });
    });

    onDestroy(() => stop?.());
}
