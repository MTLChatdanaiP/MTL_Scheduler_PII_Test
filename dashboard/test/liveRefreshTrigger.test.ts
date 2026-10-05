import { describe, it, expect, vi, beforeEach } from "vitest";

let connectLiveArgs: any[] = [];
let liveHandlers: any = null;
const stopSpy = vi.fn();

vi.mock("../src/lib/liveClient", () => ({
    connectLive: (handoff: any, handlers: any) => {
        connectLiveArgs.push({ handoff, handlers });
        liveHandlers = handlers;
        return stopSpy;
    },
}));

vi.mock("../src/lib/api", () => ({
    fetchSnapshotForLiveHandoff: vi.fn(async () => ({ overview: {}, watermark: 42 })),
}));

import { startLiveRefreshTrigger } from "../src/lib/liveRefreshTrigger";

const flush = () => new Promise((r) => setTimeout(r, 0));

beforeEach(() => {
    connectLiveArgs = [];
    liveHandlers = null;
    stopSpy.mockClear();
    vi.useRealTimers();
});

describe("startLiveRefreshTrigger", () => {
    it("connects to /live/activity using a real snapshot handoff", async () => {
        startLiveRefreshTrigger({ prefixes: ["worker."], onMatch: () => {} });
        await flush();
        expect(connectLiveArgs).toHaveLength(1);
        expect(connectLiveArgs[0].handoff.watermark).toBe(42);
        expect(connectLiveArgs[0].handlers.path).toBe("/live/activity");
    });

    it("calls onMatch for a matching prefix, ignores everything else", async () => {
        const onMatch = vi.fn();
        startLiveRefreshTrigger({ prefixes: ["worker."], onMatch, debounceMs: 10 });
        await flush();

        liveHandlers.onEvent({ id: 1, type: "task.created", subject: "x", at: "t" });
        liveHandlers.onEvent({ id: 2, type: "worker.offline", subject: "Consumer-a", at: "t" });
        await new Promise((r) => setTimeout(r, 30));

        expect(onMatch).toHaveBeenCalledTimes(1);
    });

    it("matches any of several prefixes", async () => {
        const onMatch = vi.fn();
        startLiveRefreshTrigger({ prefixes: ["worker.", "queue."], onMatch, debounceMs: 5 });
        await flush();

        liveHandlers.onEvent({ id: 1, type: "queue.degraded", subject: "tasks:stream", at: "t" });
        await new Promise((r) => setTimeout(r, 20));

        expect(onMatch).toHaveBeenCalledTimes(1);
    });

    it("debounces several matching events in one tick into a single reload", async () => {
        const onMatch = vi.fn();
        startLiveRefreshTrigger({ prefixes: ["worker."], onMatch, debounceMs: 20 });
        await flush();

        liveHandlers.onEvent({ id: 1, type: "worker.degraded", subject: "a", at: "t" });
        liveHandlers.onEvent({ id: 2, type: "worker.offline", subject: "b", at: "t" });
        liveHandlers.onEvent({ id: 3, type: "worker.online", subject: "c", at: "t" });
        await new Promise((r) => setTimeout(r, 40));

        expect(onMatch).toHaveBeenCalledTimes(1);
    });

    it("does not require an exact type match, only a prefix match (worker.online, worker.degraded, worker.offline all count)", async () => {
        const onMatch = vi.fn();
        startLiveRefreshTrigger({ prefixes: ["worker."], onMatch, debounceMs: 5 });
        await flush();

        for (const type of ["worker.online", "worker.degraded", "worker.offline"]) {
            liveHandlers.onEvent({ id: Math.random(), type, subject: "x", at: "t" });
            await new Promise((r) => setTimeout(r, 15));
        }

        expect(onMatch).toHaveBeenCalledTimes(3);
    });

    it("forwards connection state to onState", async () => {
        const onState = vi.fn();
        startLiveRefreshTrigger({ prefixes: ["queue."], onMatch: () => {}, onState });
        await flush();

        liveHandlers.onState("LIVE");
        expect(onState).toHaveBeenCalledWith("LIVE");
    });

    it("stop() calls the underlying connection's stop function", async () => {
        const stop = startLiveRefreshTrigger({ prefixes: ["worker."], onMatch: () => {} });
        await flush();
        stop();
        expect(stopSpy).toHaveBeenCalledTimes(1);
    });

    it("stop() before the snapshot resolves prevents connectLive from ever being called", async () => {
        const stop = startLiveRefreshTrigger({ prefixes: ["worker."], onMatch: () => {} });
        stop(); // synchronous, before the snapshot promise settles
        await flush();
        expect(connectLiveArgs).toHaveLength(0);
    });

    it("a pending debounced reload does not fire after stop()", async () => {
        const onMatch = vi.fn();
        const stop = startLiveRefreshTrigger({ prefixes: ["worker."], onMatch, debounceMs: 20 });
        await flush();

        liveHandlers.onEvent({ id: 1, type: "worker.offline", subject: "a", at: "t" });
        stop();
        await new Promise((r) => setTimeout(r, 40));

        expect(onMatch).not.toHaveBeenCalled();
    });
});
