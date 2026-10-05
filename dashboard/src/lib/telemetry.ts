// RFC-009 §23 Frontend Observability -- small version.
//
// Pure in-memory counters, reset on page reload, nothing sent anywhere.
// A real ingestion pipeline needs a new backend endpoint, which per §25
// ("changes that require new backend facts... must be proposed against
// RFC-008 rather than implemented as frontend-only inference") is separate,
// later work, not something to build unprompted here.
//
// Every counter key is either a fixed, small, closed set of values (an HTTP
// status, a live connection state, a banner kind) or an ENDPOINT PATTERN
// (endpointPattern() below) -- never a raw id, search term, or full query
// string. §23 explicitly forbids run_id/attempt_id/raw search text/
// unrestricted query strings as metric labels.

import { currentPath } from "./router";

interface Counter { [key: string]: number; }

const state = {
    apiFailuresByEndpoint: {} as Counter,
    apiLatencySumMsByEndpoint: {} as Counter,
    apiLatencyCountByEndpoint: {} as Counter,
    bannerShownByKind: {} as Counter,
    liveDisconnectsByState: {} as Counter,
    pageViews: 0,
    exceptionCount: 0,
};

function bump(bucket: Counter, key: string, by = 1) {
    bucket[key] = (bucket[key] ?? 0) + by;
}

// Every static route keyword this API actually uses (from api.ts). Anything
// else in a path segment is a real identifier -- a run id, a queue name, a
// worker id -- and must never become a label, so it is redacted to ":id".
// An UNKNOWN static word also redacts to ":id" rather than being kept
// as-is: a false-positive redaction is safe, a false-negative leak is not.
const STATIC_SEGMENTS = new Set([
    "alerts", "acknowledge", "runs", "execution-chains", "timeline",
    "queues", "workers", "schedules", "pii", "findings",
    "monitoring", "health", "overview", "live", "activity",
]);

export function endpointPattern(path: string): string {
    const withoutQuery = path.split("?")[0]; // the query string may hold filter text -- drop it entirely, never label with it
    const segments = withoutQuery.split("/").filter(Boolean);
    const pattern = segments.map((seg) => (STATIC_SEGMENTS.has(seg) ? seg : ":id")).join("/");
    return "/" + pattern;
}

export function recordApiCall(path: string, ok: boolean, durationMs: number) {
    const pattern = endpointPattern(path);
    bump(state.apiLatencySumMsByEndpoint, pattern, durationMs);
    bump(state.apiLatencyCountByEndpoint, pattern);
    if (!ok) bump(state.apiFailuresByEndpoint, pattern);
}

export function recordBannerShown(kind: "ERROR" | "EMPTY" | "FORBIDDEN" | "STALE" | "DEGRADED") {
    bump(state.bannerShownByKind, kind);
}

// Only the 3 states that represent an actual disconnect/degradation are
// counted -- CONNECTING/LIVE/RESYNCING/PAUSED/CLOSED are normal operation,
// not a "live-update disconnect" event.
export function recordLiveState(s: string) {
    if (s === "RECONNECTING" || s === "STALE" || s === "FORBIDDEN") {
        bump(state.liveDisconnectsByState, s);
    }
}

export function recordException() {
    state.exceptionCount++;
}

export function getTelemetrySnapshot() {
    const avgLatency: Counter = {};
    for (const k of Object.keys(state.apiLatencyCountByEndpoint)) {
        avgLatency[k] = Math.round(state.apiLatencySumMsByEndpoint[k] / state.apiLatencyCountByEndpoint[k]);
    }
    return {
        api_failures_by_endpoint: { ...state.apiFailuresByEndpoint },
        api_avg_latency_ms_by_endpoint: avgLatency,
        api_call_count_by_endpoint: { ...state.apiLatencyCountByEndpoint },
        banner_shown_by_kind: { ...state.bannerShownByKind },
        live_disconnects_by_state: { ...state.liveDisconnectsByState },
        page_views: state.pageViews,
        exception_count: state.exceptionCount,
    };
}

// Installed once, application-wide, the moment this module is first
// imported -- an ES module's top-level code runs exactly once no matter how
// many files import it, so this needs no separate init() call from
// App.svelte. api.ts imports this module, and every page imports api.ts, so
// this always runs before the first real request.
if (typeof window !== "undefined") {
    window.addEventListener("error", () => recordException());
    window.addEventListener("unhandledrejection", () => recordException());
    currentPath.subscribe(() => { state.pageViews++; });
}
