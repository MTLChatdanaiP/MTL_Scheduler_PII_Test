import { describe, it, expect } from "vitest";
import { gapStart, isGapBucket } from "../src/lib/activity";

describe("gapStart -- which badge states make the recent graph 'unknown'", () => {
    const confirmed = 1_000_000;

    it("a healthy LIVE or CONNECTING feed has no gap", () => {
        expect(gapStart("LIVE", confirmed)).toBeNull();
        expect(gapStart("CONNECTING", confirmed)).toBeNull();
    });
    it("every state where the screen cannot be vouched for starts a gap at the last confirmation", () => {
        for (const s of ["DEGRADED", "STALE", "RECONNECTING", "RESYNCING", "PAUSED"] as const) {
            expect(gapStart(s, confirmed)).toBe(confirmed);
        }
    });
    it("FORBIDDEN is not a gap (the feed never started, there is no 'last confirmed' to measure from)", () => {
        expect(gapStart("FORBIDDEN", confirmed)).toBeNull();
    });
    it("nothing was ever confirmed -> no gap to mark, even in a bad state", () => {
        expect(gapStart("RECONNECTING", null)).toBeNull();
    });
});

describe("isGapBucket", () => {
    it("no gap -> never a gap bucket", () => {
        expect(isGapBucket(5_000, null)).toBe(false);
    });
    it("a bucket starting at or after the last confirmation is unknown", () => {
        expect(isGapBucket(20_000, 20_000)).toBe(true);
        expect(isGapBucket(30_000, 20_000)).toBe(true);
    });
    it("the bucket that CONTAINS the confirmation was partly observed, so it is drawn normally", () => {
        expect(isGapBucket(10_000, 14_000)).toBe(false);
    });
    it("older buckets are real data", () => {
        expect(isGapBucket(0, 20_000)).toBe(false);
    });
});
