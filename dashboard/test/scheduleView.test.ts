import { describe, it, expect } from "vitest";
import { formatLateness, outcomeClass, outcomeLabel, countOutcomes, resultDistribution, skippedLabel } from "../src/lib/scheduleView";
import type { ScheduleOccurrence } from "../src/lib/api";

const occ = (outcome: string): ScheduleOccurrence => ({
    schedule_id: "S", occurrence_id: "S:1", expected_at: "2026-10-06T12:00:00Z", outcome, run_id: "",
    creation_lateness_seconds: null, start_lateness_seconds: null, recorded_at: "2026-10-06T12:00:00Z", last_event_at: "2026-10-06T12:00:00Z",
});

describe("formatLateness", () => {
    it("renders unknown as a dash, because unknown is not the same as on time", () => {
        expect(formatLateness(null)).toBe("—");
        expect(formatLateness(undefined)).toBe("—");
        expect(formatLateness(Number.NaN)).toBe("—");
    });

    it("renders seconds, minutes and hours compactly", () => {
        expect(formatLateness(0)).toBe("0s");
        expect(formatLateness(0.4)).toBe("0s");
        expect(formatLateness(42.6)).toBe("43s");
        expect(formatLateness(60)).toBe("1m");
        expect(formatLateness(125)).toBe("2m 5s");
        expect(formatLateness(3600)).toBe("1h");
        expect(formatLateness(5400)).toBe("1h 30m");
    });

    it("never shows a negative lateness", () => {
        expect(formatLateness(-5)).toBe("0s");
    });
});

describe("outcomes", () => {
    it("classifies the three outcomes, case-insensitively", () => {
        expect(outcomeClass("CREATED")).toBe("created");
        expect(outcomeClass("skipped")).toBe("skipped");
        expect(outcomeClass("Due")).toBe("due");
        expect(outcomeClass("")).toBe("unknown");
        expect(outcomeClass(null)).toBe("unknown");
        expect(outcomeClass("WHATEVER")).toBe("unknown");
    });

    it("says what each outcome means", () => {
        expect(outcomeLabel("SKIPPED")).toBe("Skipped (no run)");
        expect(outcomeLabel("CREATED")).toBe("Run created");
        expect(outcomeLabel("DUE")).toContain("no run");
        expect(outcomeLabel("x")).toBe("Unknown");
    });

    it("counts each outcome", () => {
        expect(countOutcomes([occ("CREATED"), occ("SKIPPED"), occ("SKIPPED"), occ("DUE"), occ("???")])).toEqual({ created: 1, skipped: 2, due: 1 });
        expect(countOutcomes(null)).toEqual({ created: 0, skipped: 0, due: 0 });
        expect(countOutcomes([])).toEqual({ created: 0, skipped: 0, due: 0 });
    });
});

// the bug: "Succeeded" was counted, but no run ever has that status
describe("resultDistribution", () => {
    it("counts Completed runs as ok", () => {
        expect(resultDistribution([{ status: "Completed" }, { status: "Completed" }, { status: "Failed" }])).toBe("2 ok / 1 failed");
    });

    it("still counts the legacy Succeeded spelling", () => {
        expect(resultDistribution([{ status: "Succeeded" }])).toBe("1 ok / 0 failed");
    });

    it("reports everything else as other", () => {
        expect(resultDistribution([{ status: "Completed" }, { status: "Running" }, { status: "Blocked" }])).toBe("1 ok / 0 failed / 2 other");
    });

    it("shows an ellipsis while there is nothing yet", () => {
        expect(resultDistribution([])).toBe("…");
        expect(resultDistribution(undefined)).toBe("…");
    });
});

describe("skippedLabel", () => {
    it("says none or how many", () => {
        expect(skippedLabel(0)).toBe("none");
        expect(skippedLabel(null)).toBe("none");
        expect(skippedLabel(7)).toBe("7 skipped");
    });
});
