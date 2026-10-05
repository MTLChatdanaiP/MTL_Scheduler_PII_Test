import type { LiveEvent, LiveState } from "./liveClient";

export type Category = "task" | "alert" | "pii" | "other";
export const CATEGORIES: Category[] = ["task", "alert", "pii", "other"];

export function categoryOf(type: string): Category {
    const c = type.split(".")[0];
    return c === "task" || c === "alert" || c === "pii" ? c : "other";
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
    const counts: Record<Category, number> = { task: 0, alert: 0, pii: 0, other: 0 };
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
            counts: { task: 0, alert: 0, pii: 0, other: 0 },
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