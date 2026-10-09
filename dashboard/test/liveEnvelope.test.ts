import { describe, it, expect } from "vitest";
import { buildLivePath, createStaleGuard } from "../src/lib/liveEnvelope";

describe("buildLivePath -- RFC-010 §15", () => {
    it("with no scopes it is exactly the old URL", () => {
        expect(buildLivePath("/live/activity", 12)).toBe("/live/activity?after=12");
    });

    it("repeats scope for each subscription and encodes odd characters", () => {
        const url = buildLivePath("/live/activity", 5, ["workers", "queue:orders", "worker:a b&c"]);
        const q = new URL(url, "http://x").searchParams;
        expect(q.get("after")).toBe("5");
        expect(q.getAll("scope")).toEqual(["workers", "queue:orders", "worker:a b&c"]);
        expect(url).not.toContain("a b&c"); // the raw value never reaches the URL
    });
});

describe("createStaleGuard -- RFC-010 §10 stale-update rejection", () => {
    const e = (id: number, seq: number, type = "QUEUE", rid = "orders") => ({ id, change_seq: seq, resource_type: type, resource_id: rid });

    it("accepts newer updates for a resource and rejects an older or repeated one", () => {
        const g = createStaleGuard();
        expect(g.accept(e(10, 10))).toBe(true);
        expect(g.accept(e(12, 12))).toBe(true);
        expect(g.accept(e(11, 11))).toBe(false); // arrived late: older than what is already reflected
        expect(g.accept(e(12, 12))).toBe(false); // the same update again
        expect(g.accept(e(13, 13))).toBe(true);
    });

    it("compares per resource: a high change_seq on one resource never rejects another", () => {
        const g = createStaleGuard();
        expect(g.accept(e(100, 100, "QUEUE", "a"))).toBe(true);
        expect(g.accept(e(5, 5, "QUEUE", "b"))).toBe(true);
        expect(g.accept(e(6, 6, "WORKER", "a"))).toBe(true); // same id, different resource class
    });

    it("accepts events without the envelope (older backend) and id-0 summaries every time", () => {
        const g = createStaleGuard();
        expect(g.accept({ id: 5 })).toBe(true);
        expect(g.accept({ id: 5 })).toBe(true);
        expect(g.accept({ id: 0, change_seq: 0, resource_type: "PLATFORM_SUMMARY", resource_id: "platform" })).toBe(true);
        expect(g.accept({ id: 0, change_seq: 0, resource_type: "PLATFORM_SUMMARY", resource_id: "platform" })).toBe(true);
    });

    it("reset() forgets everything (used after a resync)", () => {
        const g = createStaleGuard();
        g.accept(e(50, 50));
        g.reset();
        expect(g.accept(e(40, 40))).toBe(true);
    });
});
