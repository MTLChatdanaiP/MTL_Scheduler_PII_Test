import { describe, it, expect } from "vitest";
import { statusOf, statusClass, statusLabel, statusDisagrees, RUN_STATUSES } from "../src/lib/status";

describe("statusOf: the task's own status is the fact", () => {
    it("shows Task.Status even when the projection is blank (the failed-run case)", () => {
        expect(statusOf({ Status: "Failed", current_status: "" })).toBe("Failed");
    });

    it("prefers Task.Status over a stale projection", () => {
        expect(statusOf({ Status: "Failed", current_status: "Running" })).toBe("Failed");
    });

    it("falls back to the projection only when the task has no status", () => {
        expect(statusOf({ Status: "", current_status: "Completed" })).toBe("Completed");
        expect(statusOf({ current_status: "Queued" })).toBe("Queued");
    });

    it("is empty when neither is known, and never throws on null", () => {
        expect(statusOf({})).toBe("");
        expect(statusOf({ Status: null, current_status: null })).toBe("");
    });

    it("trims whitespace", () => {
        expect(statusOf({ Status: "  Running  " })).toBe("Running");
    });
});

describe("statusClass: every status the backend can produce gets a style", () => {
    it("maps all six statuses, including Blocked (which the dashboard had never heard of)", () => {
        const expected: Record<string, string> = {
            Pending: "pending", Queued: "queued", Running: "running", Completed: "completed", Failed: "failed", Blocked: "blocked",
        };
        for (const s of RUN_STATUSES) expect(statusClass(s)).toBe(expected[s]);
    });

    it("is case-insensitive and falls back to unknown", () => {
        expect(statusClass("FAILED")).toBe("failed");
        expect(statusClass("failed ")).toBe("failed");
        expect(statusClass("SomethingNew")).toBe("unknown");
        expect(statusClass("")).toBe("unknown");
    });
});

describe("statusLabel", () => {
    it("never renders an empty badge", () => {
        expect(statusLabel("")).toBe("Unknown");
        expect(statusLabel("Blocked")).toBe("Blocked");
    });
});

describe("statusDisagrees: the projection out of step with the task", () => {
    it("flags a real disagreement", () => {
        expect(statusDisagrees({ Status: "Failed", current_status: "Running" })).toBe(true);
    });

    it("does not flag agreement, nor an unpopulated projection", () => {
        expect(statusDisagrees({ Status: "Failed", current_status: "Failed" })).toBe(false);
        expect(statusDisagrees({ Status: "Failed", current_status: "" })).toBe(false);
        expect(statusDisagrees({ Status: "", current_status: "Failed" })).toBe(false);
        expect(statusDisagrees({})).toBe(false);
    });
});

describe("RUN_STATUSES", () => {
    it("is exactly what the backend produces", () => {
        expect([...RUN_STATUSES]).toEqual(["Pending", "Queued", "Running", "Completed", "Failed", "Blocked"]);
    });
});
