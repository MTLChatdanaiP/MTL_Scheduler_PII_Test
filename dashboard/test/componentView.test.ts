import { describe, it, expect } from "vitest";
import { serverHealthToStatus, healthClass, dependencyClass, revisionLabel, mixedRevisions, formatThroughput } from "../src/lib/componentView";

describe("serverHealthToStatus", () => {
    it("maps the backend's verdicts to the worker list's vocabulary", () => {
        expect(serverHealthToStatus("HEALTHY")).toBe("online");
        expect(serverHealthToStatus("DEGRADED")).toBe("degraded");
        expect(serverHealthToStatus("OFFLINE")).toBe("offline");
        expect(serverHealthToStatus("UNKNOWN")).toBe("unknown");
        expect(serverHealthToStatus("healthy")).toBe("online");
    });

    it("returns undefined when an older backend sends nothing, so the caller can fall back", () => {
        expect(serverHealthToStatus(undefined)).toBeUndefined();
        expect(serverHealthToStatus("")).toBeUndefined();
        expect(serverHealthToStatus("SOMETHING_NEW")).toBeUndefined();
    });
});

describe("healthClass and dependencyClass", () => {
    it("never invents a state", () => {
        expect(healthClass("HEALTHY")).toBe("healthy");
        expect(healthClass("OFFLINE")).toBe("offline");
        expect(healthClass("???")).toBe("unknown");
        expect(healthClass(null)).toBe("unknown");
        expect(dependencyClass("OK")).toBe("ok");
        expect(dependencyClass("UNAVAILABLE")).toBe("unavailable");
        expect(dependencyClass("whatever")).toBe("unknown");
    });
});

describe("revisionLabel", () => {
    it("never renders an empty cell", () => {
        expect(revisionLabel("")).toBe("unknown");
        expect(revisionLabel(undefined)).toBe("unknown");
        expect(revisionLabel("  ")).toBe("unknown");
        expect(revisionLabel("abc123def456")).toBe("abc123def456");
    });
});

describe("mixedRevisions", () => {
    it("lists the distinct known builds, sorted", () => {
        expect(mixedRevisions([{ build_revision: "b" }, { build_revision: "a" }, { build_revision: "b" }])).toEqual(["a", "b"]);
    });

    it("does not count 'unknown' or blank as a version, so an unlabeled dev run is not a mixed fleet", () => {
        expect(mixedRevisions([{ build_revision: "a" }, { build_revision: "unknown" }, { build_revision: "" }])).toEqual(["a"]);
        expect(mixedRevisions(undefined)).toEqual([]);
        expect(mixedRevisions([])).toEqual([]);
    });
});

describe("formatThroughput", () => {
    it("renders unknown as a dash, zero as zero, and keeps one decimal while small", () => {
        expect(formatThroughput(undefined)).toBe("—");
        expect(formatThroughput(null)).toBe("—");
        expect(formatThroughput(Number.NaN)).toBe("—");
        expect(formatThroughput(0)).toBe("0/min");
        expect(formatThroughput(0.6)).toBe("0.6/min");
        expect(formatThroughput(9.94)).toBe("9.9/min");
        expect(formatThroughput(12.4)).toBe("12/min");
    });
});
