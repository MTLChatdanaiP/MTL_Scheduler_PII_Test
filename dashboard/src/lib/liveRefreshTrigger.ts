import { connectLive, type LiveEvent, type LiveState } from "./liveClient";
import { fetchSnapshotForLiveHandoff } from "./api";

// A page-level "something in my category changed, go reload" trigger.
// Reuses the ONE existing /live/activity route (job.read, carries every
// non-noise event) rather than opening a second connection per widget --
// this file just filters client-side by prefix and calls back on a match.
// Multiple components on the SAME page (a Cards widget and a List widget)
// should share one call to this, not each open their own.

export interface LiveRefreshOptions {
    prefixes: string[];       // e.g. ["worker."] or ["queue."]
    onMatch: () => void;      // called (debounced) when a matching event arrives
    onState?: (state: LiveState) => void;
    debounceMs?: number;      // default 300 -- several events in one sweep tick collapse into one reload

    // RFC-010 §27 Fallback Behavior: when the live stream is unavailable, fall
    // back to BOUNDED polling rather than silently going quiet. Opt-in per
    // page; omitted means today's behaviour exactly (no fallback loop).
    fallback?: FallbackOptions;
}

export interface FallbackOptions {
    // How often to re-fetch while the live stream is unusable.
    intervalMs?: number;     // default 10000
    // How long to keep polling before giving up entirely. "Bounded" is the
    // point: a tab left open overnight on a dead backend must not poll forever.
    maxDurationMs?: number;  // default 300000 (5 minutes)
    // How long the connection must stay bad before polling starts, so a
    // one-second blip does not trigger a redundant fetch storm.
    graceMs?: number;        // default 5000
}

// The connection states that mean "this screen can no longer be trusted to be
// current" -- the same set the connection badge treats as not-live.
const UNUSABLE_STATES = ["RECONNECTING", "STALE", "DEGRADED", "CLOSED"];

// startBoundedFallback takes a refetch function plus timing options, and
// returns a pair of functions: one to call when the connection goes bad, one
// to call when it recovers. It polls at most once per intervalMs, never for
// longer than maxDurationMs in total, and stops immediately on recovery.
export function startBoundedFallback(refetch: () => void, opts: FallbackOptions = {}) {
    const intervalMs = opts.intervalMs ?? 10_000;
    const maxDurationMs = opts.maxDurationMs ?? 300_000;
    const graceMs = opts.graceMs ?? 5_000;

    let pollTimer: ReturnType<typeof setInterval> | undefined;
    let graceTimer: ReturnType<typeof setTimeout> | undefined;
    let stopTimer: ReturnType<typeof setTimeout> | undefined;

    function stopPolling() {
        clearTimeout(graceTimer);
        clearInterval(pollTimer);
        clearTimeout(stopTimer);
        graceTimer = undefined;
        pollTimer = undefined;
        stopTimer = undefined;
    }

    function onBad() {
        // already polling, or already waiting out the grace period
        if (pollTimer !== undefined || graceTimer !== undefined) return;

        graceTimer = setTimeout(() => {
            graceTimer = undefined;
            refetch(); // one immediate catch-up fetch, then settle into the interval
            pollTimer = setInterval(refetch, intervalMs);
            stopTimer = setTimeout(stopPolling, maxDurationMs);
        }, graceMs);
    }

    return { onBad, onGood: stopPolling, stop: stopPolling };
}

function matchesAny(type: string, prefixes: string[]): boolean {
    return prefixes.some((p) => type.startsWith(p));
}

export function startLiveRefreshTrigger(opts: LiveRefreshOptions): () => void {
    const debounceMs = opts.debounceMs ?? 300;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let stopped = false;
    let stopLive: (() => void) | null = null;

    // RFC-010 §27: only created when the page opted in.
    const fallback = opts.fallback ? startBoundedFallback(opts.onMatch, opts.fallback) : null;

    function scheduleReload() {
        clearTimeout(timer);
        timer = setTimeout(opts.onMatch, debounceMs);
    }

    (async () => {
        try {
            const handoff = await fetchSnapshotForLiveHandoff();
            if (stopped) return;

            stopLive = connectLive(handoff, {
                path: "/live/activity",
                onEvent: (e: LiveEvent) => {
                    if (matchesAny(e.type, opts.prefixes)) scheduleReload();
                },
                onState: (s) => {
                    opts.onState?.(s);
                    // RFC-010 §27: live -> bounded polling -> give up. The
                    // operator still sees the real state via the badge; this
                    // only decides whether we keep fetching behind it.
                    if (fallback) {
                        if (UNUSABLE_STATES.includes(s)) fallback.onBad();
                        else fallback.onGood();
                    }
                },
                onResync: fetchSnapshotForLiveHandoff,
            });
        } catch {
            // The initial snapshot failed, so the live stream never started.
            // With a fallback configured this is exactly the case it exists
            // for: start polling instead of leaving the page silently stale.
            fallback?.onBad();
        }
    })();

    return () => {
        stopped = true;
        clearTimeout(timer);
        stopLive?.();
        fallback?.stop();
    };
}