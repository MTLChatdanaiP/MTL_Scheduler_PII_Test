import type { LiveEvent, LiveState } from "./liveClient";

// RFC-005 §5 / RFC-010 §7: Batches 3 and 4 added real attempt.* and schedule.* events, and the feed used to file every
// family except task/alert/pii under "other", so those new events and every queue/worker/component event all landed in
// one undifferentiated bucket. The set is driven by this ONE list: the feed, the graph, the counters and the URL all
// derive from it, so a category added here cannot be forgotten somewhere else.
export type Category = "task" | "attempt" | "schedule" | "alert" | "pii" | "system" | "other";
export const CATEGORIES: Category[] = ["task", "attempt", "schedule", "alert", "pii", "system", "other"];

// the platform's own plumbing, as opposed to the work it is doing
const SYSTEM_FAMILIES = ["queue", "worker", "component", "monitoring", "redis", "delivery", "retention"];

export function categoryOf(type: string): Category {
    const family = type.split(".")[0];
    // run.* (run.lost, ...) describes a run, the same thing task.* does
    if (family === "task" || family === "run") return "task";
    if (family === "attempt" || family === "schedule" || family === "alert" || family === "pii") return family;
    if (SYSTEM_FAMILIES.includes(family)) return "system";
    return "other";
}

export function emptyCounts(): Record<Category, number> {
    return Object.fromEntries(CATEGORIES.map((c) => [c, 0])) as Record<Category, number>;
}

export function allVisible(): Record<Category, boolean> {
    return Object.fromEntries(CATEGORIES.map((c) => [c, true])) as Record<Category, boolean>;
}

// The URL records which categories are HIDDEN, not which are shown: an absent param means "everything visible", and a
// category added to CATEGORIES later is automatically visible on an old shared link instead of silently hidden.
export function showFromHidden(hiddenParam: string | null | undefined): Record<Category, boolean> {
    const hidden = new Set((hiddenParam ?? "").split(",").filter(Boolean));
    return Object.fromEntries(CATEGORIES.map((c) => [c, !hidden.has(c)])) as Record<Category, boolean>;
}

export function hiddenFromShow(show: Record<Category, boolean>): string {
    return CATEGORIES.filter((c) => !show[c]).join(",");
}

// Pure so it can be unit-tested. `show` says which categories are visible;
// `subject` is a case-insensitive substring match on the event's subject id.
export function filterEvents(
    events: LiveEvent[],
    show: Record<Category, boolean>,
    subject: string,
): LiveEvent[] {
    const q = subject.trim().toLowerCase();
    return events.filter((e) => {
        if (!show[categoryOf(e.type)]) return false;
        return q === "" || e.subject.toLowerCase().includes(q);
    });
}

export function countByCategory(events: LiveEvent[]): Record<Category, number> {
    const counts = emptyCounts();
    for (const e of events) counts[categoryOf(e.type)]++;
    return counts;
}

export interface Bucket {
    start: number; // ms since epoch, inclusive start of this bucket
    counts: Record<Category, number>;
    total: number;
}

// Fixed-width time buckets ending at `now`. The last bucket is the one that
// contains `now`. Buckets are aligned to bucketMs boundaries, so bars only
// jump when a new bucket begins instead of shifting a little every second.
// Events are placed by their own timestamp (`at`), not arrival time, so
// events replayed after a reconnect land in the bucket where they happened.
export function bucketEvents(
    events: LiveEvent[],
    now: number,
    bucketMs: number,
    bucketCount: number,
): Bucket[] {
    const lastStart = Math.floor(now / bucketMs) * bucketMs;
    const firstStart = lastStart - (bucketCount - 1) * bucketMs;

    const buckets: Bucket[] = [];
    for (let i = 0; i < bucketCount; i++) {
        buckets.push({
            start: firstStart + i * bucketMs,
            counts: emptyCounts(),
            total: 0,
        });
    }

    for (const e of events) {
        let t = Date.parse(e.at);
        if (Number.isNaN(t) || t < firstStart) continue; // unparseable, or older than the window
        // A server clock slightly ahead of the browser would otherwise make
        // fresh events vanish; clamp "future" events into the newest bucket.
        if (t >= lastStart + bucketMs) t = lastStart;
        const b = buckets[Math.floor((t - firstStart) / bucketMs)];
        b.counts[categoryOf(e.type)]++;
        b.total++;
    }

    return buckets;
}

// ---------------------------------------------------------------------------
// Connection badge (RFC-010 §18). Pure so it can be unit-tested.
//
// "Last confirmed" = the last time ANY bytes arrived from the server, events
// or keepalive pings. The server pings every 20s, so a healthy connection is
// re-confirmed at least that often even when nothing is happening.
// ---------------------------------------------------------------------------

export type BadgeState =
    | "CONNECTING" | "LIVE" | "DEGRADED" | "RECONNECTING"
    | "STALE" | "RESYNCING" | "PAUSED" | "FORBIDDEN";

export interface Badge {
    state: BadgeState;
    label: string;
}

export const DEGRADED_AFTER_S = 30; // one missed 20s ping while still connected
export const STALE_AFTER_S = 60; // too old to trust what is on screen

export function formatAge(seconds: number): string {
    const s = Math.max(0, Math.floor(seconds));
    if (s < 60) return `${s}s`;
    if (s < 3600) return `${Math.floor(s / 60)}m`;
    return `${Math.floor(s / 3600)}h`;
}

export function connectionBadge(input: {
    paused: boolean;
    state: LiveState;
    lastConfirmedAt: number | null;
    now: number;
}): Badge {
    const { paused, state, lastConfirmedAt, now } = input;

    if (paused) return { state: "PAUSED", label: "⏸ PAUSED" };
    if (state === "FORBIDDEN") return { state: "FORBIDDEN", label: "🔒 NO ACCESS" };

    const age = lastConfirmedAt === null ? null : Math.max(0, (now - lastConfirmedAt) / 1000);
    const ago = age === null ? "" : formatAge(age);

    // We did confirm something once, but not recently enough to trust the screen.
    if (age !== null && age >= STALE_AFTER_S) {
        return { state: "STALE", label: `⚠ STALE — last confirmed ${ago} ago` };
    }

    if (state === "LIVE") {
        if (age !== null && age >= DEGRADED_AFTER_S) {
            return { state: "DEGRADED", label: `⚠ DEGRADED — no confirmation for ${ago}` };
        }
        return { state: "LIVE", label: age === null ? "● LIVE" : `● LIVE — last confirmed ${ago} ago` };
    }

    if (state === "RESYNCING") return { state: "RESYNCING", label: "↻ RESYNCING" };

    if (state === "RECONNECTING") {
        return {
            state: "RECONNECTING",
            label: age === null ? "↻ RECONNECTING" : `↻ RECONNECTING — last confirmed ${ago} ago`,
        };
    }

    return { state: "CONNECTING", label: "◌ CONNECTING" }; // CONNECTING and CLOSED
}

const NO_DATA_STATES: BadgeState[] = ["DEGRADED", "STALE", "RECONNECTING", "RESYNCING", "PAUSED"];

export function gapStart(badgeState: BadgeState, lastConfirmedAt: number | null): number | null {
    return NO_DATA_STATES.includes(badgeState) ? lastConfirmedAt : null;
}

export function isGapBucket(bucketStart: number, gapFrom: number | null): boolean {
    return gapFrom !== null && bucketStart >= gapFrom;
}