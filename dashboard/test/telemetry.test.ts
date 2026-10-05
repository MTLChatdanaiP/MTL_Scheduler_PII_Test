// @vitest-environment jsdom
import { describe, it, expect, beforeEach } from "vitest";
import { endpointPattern, recordApiCall, recordBannerShown, recordLiveState, getTelemetrySnapshot } from "../src/lib/telemetry";

describe("endpointPattern -- every real call shape in api.ts", () => {
    // One case per getXxx()/acknowledgeAlert() call in the real api.ts, using
    // REAL example ids exactly as they'd appear in a live call, to prove the
    // redaction actually strips them rather than just matching by luck.
    const cases: [string, string][] = [
        ["/alerts", "/alerts"],
        ["/alerts?severity=CRITICAL&status=OPEN", "/alerts"], // query dropped entirely, never labeled
        ["/alerts/01M3P3ZVQRDY98ZHCS1MHNXC0K/acknowledge", "/alerts/:id/acknowledge"],
        ["/runs", "/runs"],
        ["/runs?job_type=dummy&limit=50&offset=0", "/runs"],
        ["/runs/01M3K8TGE616SX5SEVMTFZWD7K", "/runs/:id"],
        ["/execution-chains/01M3K8TGE616SX5SEVMTFZWD7K/timeline", "/execution-chains/:id/timeline"],
        ["/queues", "/queues"],
        ["/queues/tasks%3Astream", "/queues/:id"], // encodeURIComponent'd queue name
        ["/workers", "/workers"],
        ["/workers/Consumer-a", "/workers/:id"],
        ["/schedules", "/schedules"],
        ["/schedules?enabled=true", "/schedules"],
        ["/schedules/TEST-SCHED-001", "/schedules/:id"],
        ["/pii/findings", "/pii/findings"],
        ["/pii/findings?run_id=01M3K8TGE616SX5SEVMTFZWD7K&type=EMAIL", "/pii/findings"],
        ["/monitoring/health", "/monitoring/health"],
        ["/overview", "/overview"],
        ["/live/activity?after=48", "/live/activity"],
        ["/live/alerts", "/live/alerts"],
    ];

    for (const [input, expected] of cases) {
        it(`${input} -> ${expected}`, () => {
            expect(endpointPattern(input)).toBe(expected);
        });
    }

    it("never leaks a raw run id anywhere in the output, for any real id shape", () => {
        const realIds = ["01M3K8TGE616SX5SEVMTFZWD7K", "Consumer-a", "TEST-SCHED-001", "tasks:stream"];
        for (const id of realIds) {
            expect(endpointPattern(`/runs/${id}`)).not.toContain(id);
        }
    });

    it("an unknown future static word is redacted too (safe failure direction)", () => {
        // A route this test doesn't know about yet must not leak whatever
        // value sits in that position -- false-positive redaction is safe,
        // false-negative leak is not.
        expect(endpointPattern("/newthing/abc123")).toBe("/:id/:id");
    });
});

describe("recordApiCall", () => {
    beforeEach(() => {
        // no reset export exists (by design -- these are meant to persist for
        // the page's lifetime), so each test uses a path unique to itself
    });

    it("counts a successful call and its latency under the redacted pattern", () => {
        const path = "/runs/UNIQUE-OK-1";
        recordApiCall(path, true, 120);
        const snap = getTelemetrySnapshot();
        expect(snap.api_call_count_by_endpoint["/runs/:id"]).toBeGreaterThanOrEqual(1);
        expect(snap.api_avg_latency_ms_by_endpoint["/runs/:id"]).toBeGreaterThan(0);
    });

    it("only failed calls increment the failure counter", () => {
        const before = getTelemetrySnapshot().api_failures_by_endpoint["/alerts"] ?? 0;
        recordApiCall("/alerts", true, 10);
        expect(getTelemetrySnapshot().api_failures_by_endpoint["/alerts"] ?? 0).toBe(before);
        recordApiCall("/alerts", false, 10);
        expect(getTelemetrySnapshot().api_failures_by_endpoint["/alerts"]).toBe(before + 1);
    });

    it("average latency is the mean across every recorded call for that pattern", () => {
        const path = "/workers/UNIQUE-AVG-TEST";
        recordApiCall(path, true, 100);
        recordApiCall(path, true, 200);
        expect(getTelemetrySnapshot().api_avg_latency_ms_by_endpoint["/workers/:id"]).toBe(150);
    });
});

describe("recordBannerShown", () => {
    it("counts each named banner kind independently", () => {
        const before = getTelemetrySnapshot().banner_shown_by_kind;
        recordBannerShown("FORBIDDEN");
        recordBannerShown("FORBIDDEN");
        recordBannerShown("ERROR");
        const after = getTelemetrySnapshot().banner_shown_by_kind;
        expect(after.FORBIDDEN).toBe((before.FORBIDDEN ?? 0) + 2);
        expect(after.ERROR).toBe((before.ERROR ?? 0) + 1);
    });
});

describe("recordLiveState", () => {
    it("counts a real disconnect state", () => {
        const before = getTelemetrySnapshot().live_disconnects_by_state.STALE ?? 0;
        recordLiveState("STALE");
        expect(getTelemetrySnapshot().live_disconnects_by_state.STALE).toBe(before + 1);
    });

    it("does not count normal operating states", () => {
        const before = getTelemetrySnapshot();
        recordLiveState("LIVE");
        recordLiveState("CONNECTING");
        recordLiveState("PAUSED");
        recordLiveState("CLOSED");
        recordLiveState("RESYNCING");
        const after = getTelemetrySnapshot();
        expect(after.live_disconnects_by_state).toEqual(before.live_disconnects_by_state);
    });
});

describe("global error capture", () => {
    it("an uncaught window error increments exceptionCount", () => {
        const before = getTelemetrySnapshot().exception_count;
        window.dispatchEvent(new ErrorEvent("error", { message: "boom" }));
        expect(getTelemetrySnapshot().exception_count).toBe(before + 1);
    });

    it("an unhandled promise rejection increments exceptionCount", () => {
        const before = getTelemetrySnapshot().exception_count;
        const ev = new Event("unhandledrejection") as any;
        ev.reason = new Error("boom");
        window.dispatchEvent(ev);
        expect(getTelemetrySnapshot().exception_count).toBe(before + 1);
    });
});

describe("page views", () => {
    it("a hash navigation increments page_views", async () => {
        const { currentPath } = await import("../src/lib/router");
        const before = getTelemetrySnapshot().page_views;
        currentPath.set("/some-new-page-" + Math.random());
        expect(getTelemetrySnapshot().page_views).toBe(before + 1);
    });
});
