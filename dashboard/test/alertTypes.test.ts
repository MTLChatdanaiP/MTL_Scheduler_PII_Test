import { describe, it, expect } from "vitest";
import { ALERT_TYPES, alertTypeLabel, isKnownAlertType } from "../src/lib/alertTypes";

describe("ALERT_TYPES", () => {
    it("lists all 17 RFC-007 §4 types, once each", () => {
        expect(ALERT_TYPES).toHaveLength(17);
        expect(new Set(ALERT_TYPES).size).toBe(17);
    });

    it("is sorted, so the filter is easy to scan", () => {
        expect([...ALERT_TYPES]).toEqual([...ALERT_TYPES].sort());
    });

    it("contains the five types that had no data source before Batch 3", () => {
        for (const t of ["RUN_FAILED", "RUN_RETRY_EXHAUSTED", "RUN_TIMEOUT", "PII_SCAN_FAILED", "SCHEDULE_DELAYED"]) {
            expect(isKnownAlertType(t)).toBe(true);
        }
    });
});

describe("alertTypeLabel", () => {
    it("turns the code into a readable label", () => {
        expect(alertTypeLabel("RUN_RETRY_EXHAUSTED")).toBe("Run retry exhausted");
        expect(alertTypeLabel("PII_DETECTED")).toBe("Pii detected");
        expect(alertTypeLabel("MONITORING_GAP")).toBe("Monitoring gap");
        expect(alertTypeLabel("")).toBe("");
    });
});

describe("isKnownAlertType", () => {
    it("rejects what is not an RFC type", () => {
        expect(isKnownAlertType("RUN_LOSTT")).toBe(false);
        expect(isKnownAlertType("")).toBe(false);
    });
});
