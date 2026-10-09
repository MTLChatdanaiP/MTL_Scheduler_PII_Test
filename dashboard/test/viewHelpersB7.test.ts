import { describe, it, expect } from "vitest";
import { latestOnly, isAbort } from "../src/lib/latestOnly";
import { parseSort, formatSort, sortBy } from "../src/lib/sortView";
import { visibleSlice } from "../src/lib/progressive";
import { activateOnKey, isActivateKey } from "../src/lib/a11y";
import { workerStatusOf, queueDegraded } from "../src/lib/thresholds";
import { pipelineLag, formatLag } from "../src/lib/monitoringView";
import { formatTelemetrySnapshot } from "../src/lib/telemetry";

describe("latestOnly (RFC-009 §20)", () => {
    it("a newer request makes the older one stale and aborts it", () => {
        const l = latestOnly();
        const a = l.start();
        expect(a.isCurrent()).toBe(true);
        const b = l.start();
        expect(a.isCurrent()).toBe(false);
        expect(a.signal.aborted).toBe(true);
        expect(b.isCurrent()).toBe(true);
        expect(b.signal.aborted).toBe(false);
    });

    it("a slow old answer never overwrites a newer one", async () => {
        const l = latestOnly();
        let shown = "";
        async function search(q: string, delay: number) {
            const req = l.start();
            await new Promise((r) => setTimeout(r, delay));
            if (!req.isCurrent()) return;
            shown = q;
        }
        await Promise.all([search("old", 30), search("new", 5)]);
        expect(shown).toBe("new");
    });

    it("recognises an abort", () => {
        expect(isAbort(new DOMException("x", "AbortError"))).toBe(true);
        expect(isAbort(new Error("x"))).toBe(false);
    });
});

describe("sortView (RFC-009 §5)", () => {
    const rows = [{ n: "b", v: 2 }, { n: "a", v: 3 }, { n: "c", v: null as number | null }, { n: "d", v: 2 }];
    const acc = { n: (r: (typeof rows)[0]) => r.n, v: (r: (typeof rows)[0]) => r.v };
    it("parses and round-trips ?sort=", () => {
        expect(parseSort("-v", ["n", "v"])).toEqual({ key: "v", desc: true });
        expect(parseSort("n", ["n", "v"])).toEqual({ key: "n", desc: false });
        expect(parseSort("zzz", ["n", "v"])).toBeNull();
        expect(parseSort(null, ["n"])).toBeNull();
        expect(formatSort(parseSort("-v", ["v"]))).toBe("-v");
        expect(formatSort(null)).toBe("");
    });
    it("sorts ascending and descending, stable, missing last", () => {
        expect(sortBy(rows, { key: "v", desc: false }, acc).map((r) => r.n)).toEqual(["b", "d", "a", "c"]);
        expect(sortBy(rows, { key: "v", desc: true }, acc).map((r) => r.n)).toEqual(["a", "b", "d", "c"]);
        expect(sortBy(rows, { key: "n", desc: false }, acc).map((r) => r.n)).toEqual(["a", "b", "c", "d"]);
    });
    it("no spec means the incoming order", () => {
        expect(sortBy(rows, null, acc)).toBe(rows);
    });
});

describe("progressive drawing", () => {
    it("draws the first step then reports what is left", () => {
        const all = Array.from({ length: 450 }, (_, i) => i);
        expect(visibleSlice(all, 200)).toMatchObject({ remaining: 250 });
        expect(visibleSlice(all, 200).items).toHaveLength(200);
        expect(visibleSlice(all, 1000)).toMatchObject({ remaining: 0 });
    });
});

describe("a11y activation", () => {
    it("Enter and Space activate; other keys do not", () => {
        expect(isActivateKey("Enter")).toBe(true);
        expect(isActivateKey(" ")).toBe(true);
        expect(isActivateKey("a")).toBe(false);
    });
    it("only when pressed on the row itself, and Space does not scroll", () => {
        let n = 0;
        const row = {};
        let prevented = false;
        const ev = (key: string, target: object) => ({ key, target, currentTarget: row, preventDefault: () => (prevented = true) }) as unknown as KeyboardEvent;
        activateOnKey(ev(" ", row), () => n++);
        expect(n).toBe(1);
        expect(prevented).toBe(true);
        activateOnKey(ev(" ", {}), () => n++); // typed into a child input
        activateOnKey(ev("x", row), () => n++);
        expect(n).toBe(1);
    });
});

describe("thresholds (server verdict first)", () => {
    const now = Date.now();
    it("the server's health wins over the heartbeat age", () => {
        const old = new Date(now - 3600_000).toISOString();
        expect(workerStatusOf({ LastHeartbeat: old, health: "HEALTHY" }, now)).toBe("online");
        expect(workerStatusOf({ LastHeartbeat: old }, now)).toBe("offline");
        expect(workerStatusOf({ LastHeartbeat: new Date(now - 90_000).toISOString() }, now)).toBe("degraded");
    });
    it("queue verdict: server first, 20 only as fallback", () => {
        expect(queueDegraded({ PendingCount: 5, health: "DEGRADED" })).toBe(true);
        expect(queueDegraded({ PendingCount: 500, health: "HEALTHY" })).toBe(false);
        expect(queueDegraded({ PendingCount: 21 })).toBe(true);
        expect(queueDegraded({ PendingCount: 20 })).toBe(false);
    });
});

describe("pipelineLag (RFC-010 §22)", () => {
    const res: any = {
        subsystems: [
            { subsystem: "Event Ingestion", available: true, lag_seconds: 3 },
            { subsystem: "Projection", available: true, lag_seconds: 400 },
        ],
        live_pipeline: { last_delivery_lag_ms: 250 },
    };
    it("reports ingestion, projection and live stream with a slow flag", () => {
        const items = pipelineLag(res);
        expect(items.map((i) => [i.label, i.level])).toEqual([["Event Ingestion", "ok"], ["Projection", "slow"], ["Live stream", "ok"]]);
        expect(items[1].text).toBe("7m");
    });
    it("missing data says so; no response shows nothing", () => {
        expect(pipelineLag({ subsystems: [] } as any).every((i) => i.level === "unknown")).toBe(true);
        expect(pipelineLag(null)).toEqual([]);
        expect(formatLag(0.2)).toBe("<1s");
    });
});

describe("telemetry snapshot text", () => {
    it("is JSON with a capture time and the counters", () => {
        const text = formatTelemetrySnapshot({ page_views: 3, exception_count: 0 } as any, "2026-01-01T00:00:00Z");
        const parsed = JSON.parse(text);
        expect(parsed.captured_at).toBe("2026-01-01T00:00:00Z");
        expect(parsed.page_views).toBe(3);
    });
});
