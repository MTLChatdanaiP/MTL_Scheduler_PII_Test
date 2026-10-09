import { connectLive, type LiveEvent, type LiveState } from "./liveClient";
import { fetchSnapshotForLiveHandoff } from "./api";
import { liveFallback, type FallbackMode } from "./liveFallback";

// A page-level "something in my category changed, go reload" trigger.
// Reuses the ONE existing /live/activity route (job.read, carries every
// non-noise event) rather than opening a second connection per widget --
// this file just filters client-side by prefix and calls back on a match.
// Multiple components on the SAME page (a Cards widget and a List widget)
// should share one call to this, not each open their own.

export interface LiveRefreshOptions {
    prefixes: string[];       // e.g. ["worker."] or ["queue."]
    // RFC-010 §15: subscribe server-side to just what this page needs (e.g. ["workers"]) instead of receiving every event and
    // discarding most client-side. Omitted = today's behaviour. The prefixes still apply on top.
    scopes?: string[];
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
    // RFC-010 §27 "Fallback must be visible": told whenever the mode changes (live -> polling -> stopped, and back to live).
    onMode?: (mode: FallbackMode) => void;
    // How long the connection must stay bad before polling starts, so a
    // one-second blip does not trigger a redundant fetch storm.
    graceMs?: number;        // default 5000
}

// The connection states that mean "this screen can no longer be trusted to be
// current" -- the same set the connection badge treats as not-live.
const UNUSABLE_STATES = ["RECONNECTING", "STALE", "DEGRADED", "CLOSED"];

// After any of these, the screen may be missing changes that happened meanwhile (replay can fill the gap in the feed, but a page
// that only reloads on an event would otherwise sit on pre-outage data until the next one). Coming back to LIVE therefore refetches once.
const NEEDS_REFETCH_AFTER = [...UNUSABLE_STATES, "RESYNCING"];

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

    let mode: FallbackMode = "live";
    // Set when the bounded period ran out. The episode is then OVER until the connection recovers: a further "still bad" report must not
    // restart the clock, or a dead backend would be polled for ever in five-minute instalments.
    let gaveUp = false;

    function setMode(next: FallbackMode) {
        if (mode === next) return;
        mode = next;
        opts.onMode?.(next);
    }

    function stopPolling() {
        clearTimeout(graceTimer);
        clearInterval(pollTimer);
        clearTimeout(stopTimer);
        graceTimer = undefined;
        pollTimer = undefined;
        stopTimer = undefined;
    }

    function giveUp() {
        stopPolling();
        gaveUp = true;
        setMode("stopped");
    }

    function onBad() {
        // already polling, waiting out the grace period, or the bounded period already ran out
        if (pollTimer !== undefined || graceTimer !== undefined || gaveUp) return;

        graceTimer = setTimeout(() => {
            graceTimer = undefined;
            setMode("polling");
            refetch(); // one immediate catch-up fetch, then settle into the interval
            pollTimer = setInterval(refetch, intervalMs);
            stopTimer = setTimeout(giveUp, maxDurationMs);
        }, graceMs);
    }

    function onGood() {
        stopPolling();
        gaveUp = false;
        setMode("live");
    }

    function stop() {
        stopPolling();
        gaveUp = false;
        setMode("live"); // the page is going away: its notice must not outlive it
    }

    return { onBad, onGood, stop };
}

function matchesAny(type: string, prefixes: string[]): boolean {
    return prefixes.some((p) => type.startsWith(p));
}

export function startLiveRefreshTrigger(opts: LiveRefreshOptions): () => void {
    const debounceMs = opts.debounceMs ?? 300;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let stopped = false;
    let stopLive: (() => void) | null = null;

    let lastLiveAt: number | null = null; // the last moment the live stream was known good (event or LIVE state)
    let needsRefetch = false;

    // RFC-010 §27: only created when the page opted in. The mode is also published to the global bar so the operator can SEE it.
    const fallback = opts.fallback
        ? startBoundedFallback(opts.onMatch, {
              ...opts.fallback,
              onMode: (mode) => {
                  liveFallback.set({ mode, intervalMs: opts.fallback?.intervalMs ?? 10_000, lastLiveAt });
                  opts.fallback?.onMode?.(mode);
              },
          })
        : null;

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
                scopes: opts.scopes,
                onEvent: (e: LiveEvent) => {
                    if (matchesAny(e.type, opts.prefixes)) {
                        lastLiveAt = Date.now();
                        scheduleReload();
                    }
                },
                onState: (s) => {
                    opts.onState?.(s);
                    if (s === "LIVE") {
                        lastLiveAt = Date.now();
                        if (needsRefetch) {
                            needsRefetch = false;
                            scheduleReload(); // back after a gap: catch up once
                        }
                    } else if (NEEDS_REFETCH_AFTER.includes(s)) {
                        needsRefetch = true;
                    }
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