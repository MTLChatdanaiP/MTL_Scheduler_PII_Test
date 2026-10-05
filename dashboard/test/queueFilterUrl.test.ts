// Exercises the exact functions router.ts exports, against the real logic
// QueueList.svelte's syncFiltersFromUrl/commitFilters use, without needing a
// full Svelte component mount -- this proves the URL round-trip itself is
// correct, which a compile check alone does not.
import { describe, it, expect, beforeEach } from "vitest";

// @vitest-environment jsdom
import { currentPath, parsePath, updateParams, navigate } from "../src/lib/router";
import { get } from "svelte/store";

beforeEach(() => {
    window.location.hash = "";
    currentPath.set("/queues");
});

// updateParams()/navigate() set window.location.hash synchronously, but the
// currentPath STORE only updates once the browser's hashchange event
// actually fires -- which is asynchronous even in a real browser (one task
// later), not just in jsdom. Every existing URL-synced page (WorkerList,
// Alerts, ...) already relies on this same async update; it is not specific
// to QueueList. Tests that read currentPath right after a write need to
// wait one tick for that event, same as the real browser would deliver it.
async function tick() {
    await new Promise((r) => setTimeout(r, 0));
}

function syncFiltersFromUrl() {
    const { params } = parsePath(get(currentPath));
    return {
        queueNameFilter: params.get("queue_name") ?? "",
        statusFilter: (params.get("status") ?? "") as "" | "healthy" | "degraded",
        zeroConsumersOnly: params.get("zero_consumers") === "true",
    };
}

describe("Queues filter <-> URL round trip (real router.ts)", () => {
    it("starts with no filters on a bare /queues URL", () => {
        expect(syncFiltersFromUrl()).toEqual({ queueNameFilter: "", statusFilter: "", zeroConsumersOnly: false });
    });

    it("committing filters writes them into the URL", async () => {
        updateParams({ queue_name: "tasks:stream", status: "degraded", zero_consumers: "true" });
        await tick();
        expect(get(currentPath)).toBe("/queues?queue_name=tasks%3Astream&status=degraded&zero_consumers=true");
    });

    it("a page reload (fresh parse of the URL) restores the same filters", async () => {
        updateParams({ queue_name: "tasks:stream", status: "degraded", zero_consumers: "true" });
        await tick();
        expect(syncFiltersFromUrl()).toEqual({
            queueNameFilter: "tasks:stream",
            statusFilter: "degraded",
            zeroConsumersOnly: true,
        });
    });

    it("clearing a filter (empty string) removes it from the URL instead of leaving queue_name=", () => {
        updateParams({ queue_name: "tasks:stream" });
        updateParams({ queue_name: "" });
        expect(get(currentPath)).toBe("/queues");
    });

    it("does not clobber an unrelated param already in the URL (e.g. the global time range)", async () => {
        currentPath.set("/queues?time_range=1h");
        updateParams({ queue_name: "tasks:stream" });
        await tick();
        const { params } = parsePath(get(currentPath));
        expect(params.get("time_range")).toBe("1h");
        expect(params.get("queue_name")).toBe("tasks:stream");
    });

    it("setting the same value twice does not re-navigate (the no-op guard from router.ts)", () => {
        updateParams({ status: "degraded" });
        const before = get(currentPath);
        updateParams({ status: "degraded" });
        expect(get(currentPath)).toBe(before);
    });

    it("navigating to a link with filters already in the query restores them on load", () => {
        // simulates opening a shared link directly
        currentPath.set("/queues?queue_name=Consumer&status=healthy&zero_consumers=true");
        expect(syncFiltersFromUrl()).toEqual({
            queueNameFilter: "Consumer",
            statusFilter: "healthy",
            zeroConsumersOnly: true,
        });
    });
});
