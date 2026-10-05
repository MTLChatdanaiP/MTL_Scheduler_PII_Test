// RFC-010 §9 Component Health Semantics.
//
// One shared vocabulary, and two small pure functions that every component's
// raw signal is normalized through, instead of each caller inventing its own
// good/bad logic. Callers keep whatever raw shape is natural to them (an age
// in seconds, a count) and get back the same {status, reason} shape.
//
// This module does NOT decide what to do about a bad status (alert, page
// someone, retry) -- it only standardizes what "bad" means and why.

import { formatAge } from "./activity";

export type ComponentStatus = "healthy" | "degraded" | "unhealthy" | "offline" | "unknown" | "not-built";

export interface Verdict {
    status: ComponentStatus;
    reason: string;
}

// Higher = worse. "unknown" sits between degraded and unhealthy: we are less
// sure something is fine than a confirmed "degraded", but a confirmed
// "unhealthy" or "offline" is still worse than simply not knowing.
const SEVERITY: Record<ComponentStatus, number> = {
    healthy: 0,
    degraded: 1,
    unknown: 2,
    unhealthy: 3,
    offline: 4,
    "not-built": -1, // never wins worstVerdict -- it means "nothing to combine"
};

export function staticVerdict(status: ComponentStatus, reason: string): Verdict {
    return { status, reason };
}

// For anything measured by "how long since we last heard from it": worker
// heartbeats, subsystem freshness, the live gateway's own heartbeat.
// ageSeconds === null means no observation exists at all -- that is
// "unknown", not "offline": offline is a confirmed absence, unknown is no
// signal to judge by yet.
// offlineAfter is nullable: some signals only advance when real work happens
// (an event-ingestion timestamp, say) rather than on a fixed timer. For those,
// staleness means "quiet", not "broken" -- pass null so it can read as
// degraded at worst, and never falsely claim something has stopped running.
export function verdictFromAge(
    ageSeconds: number | null,
    degradedAfter: number,
    offlineAfter: number | null,
    label = "last signal",
): Verdict {
    if (ageSeconds === null) {
        return { status: "unknown", reason: `no ${label} observed yet` };
    }
    const age = formatAge(ageSeconds);
    if (offlineAfter !== null && ageSeconds > offlineAfter) {
        return { status: "offline", reason: `${label} ${age} ago (offline after ${formatAge(offlineAfter)})` };
    }
    if (ageSeconds > degradedAfter) {
        return { status: "degraded", reason: `${label} ${age} ago (degraded after ${formatAge(degradedAfter)})` };
    }
    return { status: "healthy", reason: `${label} ${age} ago` };
}

// Go's zero-value time.Time ("0001-01-01T00:00:00Z") on the wire means "this
// field was never set", not a real, absurdly old timestamp. Treat it the same
// as no signal at all (unknown), not as a genuinely ancient one (offline).
export function isZeroTimestamp(iso: string | null | undefined): boolean {
    return !iso || iso.startsWith("0001-01-01T00:00:00");
}

// For anything measured by a count that should stay under a bound: degraded
// queues, offline workers, open schedule-missed alerts. Pass unhealthyAt as
// null when there is no second, worse tier.
export function verdictFromCount(
    count: number,
    degradedAt: number,
    unhealthyAt: number | null,
    label = "count",
): Verdict {
    if (unhealthyAt !== null && count >= unhealthyAt) {
        return { status: "unhealthy", reason: `${label} is ${count} (unhealthy at ${unhealthyAt}+)` };
    }
    if (count >= degradedAt) {
        return { status: "degraded", reason: `${label} is ${count} (degraded at ${degradedAt}+)` };
    }
    return { status: "healthy", reason: `${label} is ${count}` };
}

// Combines several verdicts into the single worst one. "not-built" inputs are
// skipped (there is nothing to combine there). Combining zero real verdicts
// is "unknown", not "healthy" -- silence is not the same as good news.
export function worstVerdict(...verdicts: Verdict[]): Verdict {
    const real = verdicts.filter((v) => v.status !== "not-built");
    if (real.length === 0) return { status: "unknown", reason: "no components to combine" };
    return real.reduce((worst, v) => (SEVERITY[v.status] > SEVERITY[worst.status] ? v : worst));
}