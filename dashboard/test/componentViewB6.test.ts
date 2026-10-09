import { describe, it, expect } from "vitest";
import type { ComponentHealthItem } from "../src/lib/api";
import { statusFromServer, derivedToRows, mergeComponentRows, latestPerName, summarizeHealth } from "../src/lib/componentView";
import { drillHref } from "../src/lib/drillLinks";

const item = (over: Partial<ComponentHealthItem>): ComponentHealthItem => ({
    component_type: "Worker", component_instance_id: "i", display_name: "w", build_revision: "r", started_at: "2026-10-09T00:00:00Z",
    last_heartbeat: null, health: "HEALTHY", reason: "ok", evidence: {}, ...over,
});

describe("statusFromServer", () => {
    it("maps the server's vocabulary, including UNHEALTHY", () => {
        expect(statusFromServer("HEALTHY")).toBe("healthy");
        expect(statusFromServer("unhealthy")).toBe("unhealthy");
        expect(statusFromServer("OFFLINE")).toBe("offline");
    });
    it("never shows an unrecognised verdict as healthy", () => {
        expect(statusFromServer("SOMETHING_NEW")).toBe("unknown");
        expect(statusFromServer(undefined)).toBe("unknown");
    });
});

describe("derivedToRows", () => {
    it("keeps the RFC-010 §9 lag and threshold in the expandable evidence", () => {
        const rows = derivedToRows([item({
            display_name: "Monitoring Projector", component_kind: "MONITORING_PROJECTOR", status: "DEGRADED", health: "DEGRADED",
            status_reason: "projection lag high", observed_lag_ms: 12750, threshold_ms: 5000, evidence: { busy: true },
        })]);
        expect(rows[0]).toMatchObject({ name: "Monitoring Projector", status: "degraded", reason: "projection lag high" });
        expect(rows[0].evidence).toMatchObject({ observed_lag_ms: 12750, threshold_ms: 5000, component_kind: "MONITORING_PROJECTOR", busy: true });
    });
    it("copes with an older item that has only health/reason", () => {
        expect(derivedToRows([item({ display_name: "X", health: "DEGRADED", reason: "r" })])[0]).toMatchObject({ status: "degraded", reason: "r" });
    });
    it("undefined (older backend) is no rows", () => {
        expect(derivedToRows(undefined)).toEqual([]);
    });
});

describe("mergeComponentRows", () => {
    const row = (name: string, status: any = "healthy") => ({ name, status, reason: name, evidence: null });

    it("prefers the server's row, keeps Scheduler/Workers from the client, and orders them", () => {
        const client = [row("API"), row("Scheduler"), row("Workers"), row("Redis Delivery", "unknown")];
        const server = [row("Redis Delivery", "degraded"), row("API", "healthy")];
        const merged = mergeComponentRows(client, server);
        expect(merged.map((r) => r.name)).toEqual(["API", "Scheduler", "Redis Delivery", "Workers"]);
        expect(merged.find((r) => r.name === "Redis Delivery")!.status).toBe("degraded");
    });

    it("drops the client's combined ingestor/projector row once the server supplies both", () => {
        const client = [row("Monitoring Ingestor / Projector")];
        const merged = mergeComponentRows(client, [row("Monitoring Ingestor"), row("Monitoring Projector")]);
        expect(merged.map((r) => r.name)).toEqual(["Monitoring Ingestor", "Monitoring Projector"]);
    });

    it("keeps the combined row when the server sent only one of the two", () => {
        const merged = mergeComponentRows([row("Monitoring Ingestor / Projector")], [row("Monitoring Ingestor")]);
        expect(merged.map((r) => r.name)).toContain("Monitoring Ingestor / Projector");
    });

    it("never drops a row only one side knows", () => {
        const merged = mergeComponentRows([row("Custom")], [row("API")]);
        expect(merged.map((r) => r.name).sort()).toEqual(["API", "Custom"]);
    });
});

describe("latestPerName -- a restarted worker is one component, not one per run", () => {
    it("keeps the newest started_at per type and name", () => {
        const items = [
            item({ component_instance_id: "old", display_name: "Consumer-a", started_at: "2026-10-08T00:00:00Z", health: "OFFLINE" }),
            item({ component_instance_id: "new", display_name: "Consumer-a", started_at: "2026-10-09T00:00:00Z" }),
            item({ component_instance_id: "sched", component_type: "Scheduler", display_name: "Schedule_Buddy" }),
        ];
        expect(latestPerName(items).map((i) => i.component_instance_id).sort()).toEqual(["new", "sched"]);
    });
});

describe("summarizeHealth -- the Overview card", () => {
    it("counts a deliberate stop apart from offline, and ranks what needs attention", () => {
        const s = summarizeHealth(
            [
                item({ display_name: "Consumer-a", health: "HEALTHY" }),
                item({ display_name: "Reclaimer", component_type: "Reclaimer", health: "OFFLINE", evidence: { stopped: true } }),
                item({ display_name: "Schedule_Buddy", component_type: "Scheduler", health: "OFFLINE", reason: "last heartbeat 9m ago" }),
            ],
            [
                item({ display_name: "Redis Delivery", health: "DEGRADED", status: "DEGRADED", reason: "1 queue has no consumer" }),
                item({ display_name: "Query API", health: "UNHEALTHY", status: "UNHEALTHY", reason: "database did not answer" }),
            ],
        );
        expect(s).toMatchObject({ healthy: 1, degraded: 1, unhealthy: 1, offline: 1, stopped: 1, unknown: 0 });
        expect(s.worst.map((w) => w.name)).toEqual(["Schedule_Buddy", "Query API", "Redis Delivery"]); // offline, unhealthy, degraded
    });

    it("an older backend with no derived list still summarizes the instances", () => {
        const s = summarizeHealth([item({ health: "HEALTHY" })], undefined);
        expect(s.healthy).toBe(1);
        expect(s.worst).toEqual([]);
    });
});

describe("drillHref -- RFC-010 §22 drill-through", () => {
    it("links each aggregate to its page", () => {
        expect(drillHref("workers")).toBe("#/workers");
        expect(drillHref("components")).toBe("#/components");
    });
    it("carries the filter that matches the aggregate where the page has one", () => {
        expect(drillHref("alerts")).toBe("#/alerts?status=OPEN");
    });
});
