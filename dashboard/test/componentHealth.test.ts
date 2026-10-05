import { describe, it, expect } from "vitest";
import { verdictFromAge, verdictFromCount, worstVerdict, staticVerdict, isZeroTimestamp } from "../src/lib/componentHealth";

describe("verdictFromAge", () => {
    it("null age means unknown, not offline", () => {
        expect(verdictFromAge(null, 60, 300)).toEqual({ status: "unknown", reason: "no last signal observed yet" });
    });
    it("under the degraded threshold is healthy", () => {
        expect(verdictFromAge(10, 60, 300).status).toBe("healthy");
    });
    it("between the two thresholds is degraded", () => {
        expect(verdictFromAge(120, 60, 300).status).toBe("degraded");
    });
    it("past the offline threshold is offline", () => {
        expect(verdictFromAge(400, 60, 300).status).toBe("offline");
    });
    it("exactly at a threshold does not cross it (strictly greater than)", () => {
        expect(verdictFromAge(60, 60, 300).status).toBe("healthy");
        expect(verdictFromAge(300, 60, 300).status).toBe("degraded");
    });
    it("reason includes the label and both the age and the threshold", () => {
        const v = verdictFromAge(400, 60, 300, "heartbeat");
        expect(v.reason).toContain("heartbeat");
        expect(v.reason).toContain("offline after");
    });
});

describe("verdictFromCount", () => {
    it("under the degraded threshold is healthy", () => {
        expect(verdictFromCount(0, 1, null).status).toBe("healthy");
    });
    it("at the degraded threshold is degraded", () => {
        expect(verdictFromCount(1, 1, null).status).toBe("degraded");
    });
    it("at the unhealthy threshold is unhealthy, taking priority over degraded", () => {
        expect(verdictFromCount(5, 1, 5).status).toBe("unhealthy");
    });
    it("works with no unhealthy tier at all", () => {
        expect(verdictFromCount(1000, 1, null).status).toBe("degraded");
    });
});

describe("worstVerdict", () => {
    it("picks the single worst of several verdicts", () => {
        const healthy = staticVerdict("healthy", "a");
        const degraded = staticVerdict("degraded", "b");
        const offline = staticVerdict("offline", "c");
        expect(worstVerdict(healthy, degraded).status).toBe("degraded");
        expect(worstVerdict(healthy, degraded, offline).status).toBe("offline");
    });
    it("unknown outranks degraded but not unhealthy or offline", () => {
        expect(worstVerdict(staticVerdict("degraded", "x"), staticVerdict("unknown", "y")).status).toBe("unknown");
        expect(worstVerdict(staticVerdict("unknown", "x"), staticVerdict("unhealthy", "y")).status).toBe("unhealthy");
    });
    it("ignores not-built inputs entirely", () => {
        expect(worstVerdict(staticVerdict("healthy", "a"), staticVerdict("not-built", "b")).status).toBe("healthy");
    });
    it("combining zero real verdicts is unknown, not healthy", () => {
        expect(worstVerdict().status).toBe("unknown");
        expect(worstVerdict(staticVerdict("not-built", "a")).status).toBe("unknown");
    });
});

describe("staticVerdict", () => {
    it("passes status and reason straight through", () => {
        expect(staticVerdict("healthy", "self-evident")).toEqual({ status: "healthy", reason: "self-evident" });
    });
});

describe("verdictFromAge with no offline tier (activity-driven signals)", () => {
    it("never reports offline, however old, when offlineAfter is null", () => {
        expect(verdictFromAge(10_000_000, 60, null).status).toBe("degraded");
    });
    it("still reports healthy under the degraded threshold", () => {
        expect(verdictFromAge(10, 60, null).status).toBe("healthy");
    });
    it("still reports unknown for a null age", () => {
        expect(verdictFromAge(null, 60, null).status).toBe("unknown");
    });
});

describe("isZeroTimestamp", () => {
    it("recognises Go's zero-value time.Time", () => {
        expect(isZeroTimestamp("0001-01-01T00:00:00Z")).toBe(true);
    });
    it("recognises it regardless of fractional seconds", () => {
        expect(isZeroTimestamp("0001-01-01T00:00:00.000Z")).toBe(true);
    });
    it("treats null, undefined and empty string as zero too", () => {
        expect(isZeroTimestamp(null)).toBe(true);
        expect(isZeroTimestamp(undefined)).toBe(true);
        expect(isZeroTimestamp("")).toBe(true);
    });
    it("does not flag a real timestamp", () => {
        expect(isZeroTimestamp("2026-09-30T13:19:06Z")).toBe(false);
    });
});