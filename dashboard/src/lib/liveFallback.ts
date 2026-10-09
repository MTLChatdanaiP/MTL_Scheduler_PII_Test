import { writable } from "svelte/store";

// RFC-010 §27: "Fallback must be visible to the operator."
//
//   live stream -> bounded polling -> last successful snapshot + STALE warning
//
// A page that opted in to the bounded fallback (see liveRefreshTrigger.ts) reports which of the three it is in here, and the global bar
// shows it. Without this the dashboard silently switched to polling, or silently stopped, and the operator had to infer it from a badge.

export type FallbackMode = "live" | "polling" | "stopped";

export interface FallbackState {
    mode: FallbackMode;
    intervalMs: number; // how often it is polling (only meaningful in "polling")
    lastLiveAt: number | null; // epoch ms of the last moment the live stream was known good
}

export const initialFallback: FallbackState = { mode: "live", intervalMs: 10_000, lastLiveAt: null };

export const liveFallback = writable<FallbackState>(initialFallback);

function clock(ms: number): string {
    return new Date(ms).toLocaleTimeString([], { hour12: false });
}

// The sentence the operator sees, or null while everything is live.
export function fallbackNotice(s: FallbackState): string | null {
    if (s.mode === "polling") {
        return `Live updates are unavailable. Refreshing every ${Math.round(s.intervalMs / 1000)}s instead.`;
    }
    if (s.mode === "stopped") {
        const since = s.lastLiveAt ? ` Showing data from before ${clock(s.lastLiveAt)}.` : "";
        return `Live updates and automatic refresh have stopped.${since} Reload the page to try again.`;
    }
    return null;
}
