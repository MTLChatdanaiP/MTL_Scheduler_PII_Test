import { describe, it, expect } from "vitest";
import { categoryOf, filterEvents, countByCategory, bucketEvents, connectionBadge, formatAge } from "../src/lib/activity";
import type { LiveEvent } from "../src/lib/liveClient";

const ev = (id: number, type: string, at: string, subject = "subj"): LiveEvent => ({ id, type, subject, at });

const SHOW_ALL = { task: true, alert: true, pii: true, other: true };

describe("categoryOf", () => {
    it("groups by the prefix before the first dot", () => {
        expect(categoryOf("task.created")).toBe("task");
        expect(categoryOf("alert.opened")).toBe("alert");
        expect(categoryOf("pii.detected")).toBe("pii");
    });
    it("puts everything else in 'other'", () => {
        expect(categoryOf("worker.online")).toBe("other");
        expect(categoryOf("")).toBe("other");
    });
});

describe("filterEvents", () => {
    const events = [
        ev(1, "task.created", "2026-09-28T05:00:00Z", "01ABCDEF"),
        ev(2, "alert.opened", "2026-09-28T05:00:01Z", "01XYZ"),
        ev(3, "pii.detected", "2026-09-28T05:00:02Z", "01abcdef"),
    ];

    it("returns everything when nothing is filtered", () => {
        expect(filterEvents(events, SHOW_ALL, "")).toHaveLength(3);
    });
    it("hides a category", () => {
        const r = filterEvents(events, { ...SHOW_ALL, alert: false }, "");
        expect(r.map((e) => e.id)).toEqual([1, 3]);
    });
    it("matches subject as a case-insensitive substring", () => {
        expect(filterEvents(events, SHOW_ALL, "ABCDEF").map((e) => e.id)).toEqual([1, 3]);
    });
    it("ignores surrounding spaces in the subject filter", () => {
        expect(filterEvents(events, SHOW_ALL, "  xyz ").map((e) => e.id)).toEqual([2]);
    });
    it("combines category and subject filters", () => {
        expect(filterEvents(events, { ...SHOW_ALL, pii: false }, "abcdef").map((e) => e.id)).toEqual([1]);
    });
});

describe("countByCategory", () => {
    it("counts per category", () => {
        const c = countByCategory([ev(1, "task.a", "x"), ev(2, "task.b", "x"), ev(3, "pii.d", "x")]);
        expect(c).toEqual({ task: 2, alert: 0, pii: 1, other: 0 });
    });
});

describe("bucketEvents", () => {
    // 10s buckets, 3 of them. now = 00:00:25, so the buckets start at :00, :10 and :20
    const T = (s: number) => Date.UTC(2026, 8, 28, 5, 0, s);
    const iso = (s: number) => new Date(T(s)).toISOString();

    it("makes the requested number of buckets, newest last, aligned to bucketMs", () => {
        const b = bucketEvents([], T(25), 10_000, 3);
        expect(b.map((x) => x.start)).toEqual([T(0), T(10), T(20)]);
    });

    it("puts events in the bucket that contains their timestamp", () => {
        const b = bucketEvents([ev(1, "task.a", iso(1)), ev(2, "task.b", iso(9)), ev(3, "alert.x", iso(10)), ev(4, "pii.d", iso(29))], T(25), 10_000, 3);
        expect(b[0].counts.task).toBe(2);
        expect(b[1].counts.alert).toBe(1);
        expect(b[2].counts.pii).toBe(1);
        expect(b.map((x) => x.total)).toEqual([2, 1, 1]);
    });

    it("ignores events older than the window", () => {
        const b = bucketEvents([ev(1, "task.a", iso(-5))], T(25), 10_000, 3);
        expect(b.reduce((s, x) => s + x.total, 0)).toBe(0);
    });

    it("clamps events from the future into the newest bucket (clock skew)", () => {
        const b = bucketEvents([ev(1, "task.a", iso(40))], T(25), 10_000, 3);
        expect(b[2].total).toBe(1);
    });

    it("skips events whose timestamp can't be parsed", () => {
        const b = bucketEvents([ev(1, "task.a", "not-a-date")], T(25), 10_000, 3);
        expect(b.reduce((s, x) => s + x.total, 0)).toBe(0);
    });

    it("parses both timestamp shapes this backend emits (Z with 7 fraction digits, and +07:00)", () => {
        const now = Date.parse("2026-09-28T05:47:05Z");
        const b = bucketEvents(
            [ev(1, "task.started", "2026-09-28T05:47:01.5024527Z"), ev(2, "task.queued", "2026-09-28T12:47:02.123456+07:00")],
            now, 10_000, 3,
        );
        expect(b[2].total).toBe(2);
    });
});


describe("formatAge", () => {
    it("uses seconds, then minutes, then hours", () => {
        expect(formatAge(0)).toBe("0s");
        expect(formatAge(47.9)).toBe("47s");
        expect(formatAge(60)).toBe("1m");
        expect(formatAge(179)).toBe("2m");
        expect(formatAge(3600)).toBe("1h");
    });
    it("never goes negative (clock skew)", () => {
        expect(formatAge(-5)).toBe("0s");
    });
});

describe("connectionBadge", () => {
    const NOW = 1_000_000_000;
    const ago = (s: number) => NOW - s * 1000;
    const badge = (over: Partial<Parameters<typeof connectionBadge>[0]>) =>
        connectionBadge({ paused: false, state: "LIVE", lastConfirmedAt: ago(1), now: NOW, ...over });

    it("LIVE shows how long ago it was last confirmed", () => {
        expect(badge({ lastConfirmedAt: ago(3) })).toEqual({ state: "LIVE", label: "● LIVE — last confirmed 3s ago" });
    });

    it("LIVE with nothing confirmed yet has no age", () => {
        expect(badge({ lastConfirmedAt: null }).label).toBe("● LIVE");
    });

    it("a quiet but pinged connection stays LIVE (ping every 20s keeps age under 30s)", () => {
        expect(badge({ lastConfirmedAt: ago(19) }).state).toBe("LIVE");
    });

    it("LIVE degrades after a missed ping, then goes STALE", () => {
        expect(badge({ lastConfirmedAt: ago(29) }).state).toBe("LIVE");
        expect(badge({ lastConfirmedAt: ago(30) })).toEqual({ state: "DEGRADED", label: "⚠ DEGRADED — no confirmation for 30s" });
        expect(badge({ lastConfirmedAt: ago(59) }).state).toBe("DEGRADED");
        expect(badge({ lastConfirmedAt: ago(60) }).state).toBe("STALE");
    });

    it("RECONNECTING reports the last confirmed update, like the RFC example", () => {
        expect(badge({ state: "RECONNECTING", lastConfirmedAt: ago(47) })).toEqual({
            state: "RECONNECTING",
            label: "↻ RECONNECTING — last confirmed 47s ago",
        });
    });

    it("RECONNECTING turns STALE once the last confirmation is old enough", () => {
        expect(badge({ state: "RECONNECTING", lastConfirmedAt: ago(125) })).toEqual({
            state: "STALE",
            label: "⚠ STALE — last confirmed 2m ago",
        });
    });

    it("RECONNECTING with nothing ever confirmed has no age and never goes STALE", () => {
        expect(badge({ state: "RECONNECTING", lastConfirmedAt: null })).toEqual({ state: "RECONNECTING", label: "↻ RECONNECTING" });
    });

    it("RESYNCING is its own state", () => {
        expect(badge({ state: "RESYNCING" }).state).toBe("RESYNCING");
    });

    it("CONNECTING and CLOSED both read as CONNECTING", () => {
        expect(badge({ state: "CONNECTING", lastConfirmedAt: null }).state).toBe("CONNECTING");
        expect(badge({ state: "CLOSED", lastConfirmedAt: null }).state).toBe("CONNECTING");
    });

    it("PAUSED and FORBIDDEN override everything else", () => {
        expect(badge({ paused: true, lastConfirmedAt: ago(500) }).state).toBe("PAUSED");
        expect(badge({ state: "FORBIDDEN", lastConfirmedAt: ago(500) }).state).toBe("FORBIDDEN");
    });
});