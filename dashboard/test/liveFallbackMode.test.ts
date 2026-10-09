import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { get } from "svelte/store";

let liveHandlers: any = null;
let connectArgs: any = null;
vi.mock("../src/lib/liveClient", () => ({
    connectLive: (handoff: any, handlers: any) => {
        connectArgs = { handoff, handlers };
        liveHandlers = handlers;
        return () => {};
    },
}));
vi.mock("../src/lib/api", () => ({
    fetchSnapshotForLiveHandoff: vi.fn(async () => ({ overview: {}, watermark: 1 })),
}));

import { startBoundedFallback, startLiveRefreshTrigger } from "../src/lib/liveRefreshTrigger";
import { liveFallback, fallbackNotice, initialFallback } from "../src/lib/liveFallback";

beforeEach(() => {
    vi.useFakeTimers();
    liveHandlers = null;
    connectArgs = null;
    liveFallback.set(initialFallback);
});
afterEach(() => vi.useRealTimers());

const settle = async () => {
    await vi.advanceTimersByTimeAsync(0);
};

describe("fallback modes are reported -- RFC-010 §27 'visible to the operator'", () => {
    it("reports polling after the grace period, then stopped when the bounded period ends, then live on recovery", () => {
        const modes: string[] = [];
        const fb = startBoundedFallback(() => {}, { graceMs: 1000, intervalMs: 1000, maxDurationMs: 5000, onMode: (m) => modes.push(m) });

        fb.onBad();
        vi.advanceTimersByTime(999);
        expect(modes).toEqual([]); // a short blip says nothing
        vi.advanceTimersByTime(1);
        expect(modes).toEqual(["polling"]);
        vi.advanceTimersByTime(5000);
        expect(modes).toEqual(["polling", "stopped"]);
        fb.onGood();
        expect(modes).toEqual(["polling", "stopped", "live"]);
    });

    it("a blip that recovers inside the grace period never reports a mode", () => {
        const modes: string[] = [];
        const fb = startBoundedFallback(() => {}, { graceMs: 5000, onMode: (m) => modes.push(m) });
        fb.onBad();
        vi.advanceTimersByTime(2000);
        fb.onGood();
        expect(modes).toEqual([]);
    });

    it("once the bounded period ran out, a further 'still bad' report does NOT start another five minutes of polling", () => {
        const refetch = vi.fn();
        const fb = startBoundedFallback(refetch, { graceMs: 100, intervalMs: 1000, maxDurationMs: 3000 });
        fb.onBad();
        vi.advanceTimersByTime(5000);
        const callsAtGiveUp = refetch.mock.calls.length;

        fb.onBad(); // the connection state flaps while still down
        vi.advanceTimersByTime(600_000);
        expect(refetch).toHaveBeenCalledTimes(callsAtGiveUp);

        fb.onGood(); // a real recovery re-arms it
        fb.onBad();
        vi.advanceTimersByTime(200);
        expect(refetch.mock.calls.length).toBeGreaterThan(callsAtGiveUp);
    });

    it("stop() clears the notice so it cannot outlive the page", () => {
        const modes: string[] = [];
        const fb = startBoundedFallback(() => {}, { graceMs: 100, onMode: (m) => modes.push(m) });
        fb.onBad();
        vi.advanceTimersByTime(200);
        fb.stop();
        expect(modes).toEqual(["polling", "live"]);
    });
});

describe("fallbackNotice", () => {
    it("says nothing while live", () => {
        expect(fallbackNotice(initialFallback)).toBeNull();
    });
    it("names the polling interval", () => {
        expect(fallbackNotice({ mode: "polling", intervalMs: 10_000, lastLiveAt: null })).toContain("every 10s");
    });
    it("tier 3 says everything stopped and names the last live contact", () => {
        const at = new Date(2026, 9, 9, 14, 2, 11).getTime();
        const text = fallbackNotice({ mode: "stopped", intervalMs: 10_000, lastLiveAt: at })!;
        expect(text).toContain("stopped");
        expect(text).toContain("14:02:11");
    });
    it("tier 3 without a known last contact still says it stopped", () => {
        expect(fallbackNotice({ mode: "stopped", intervalMs: 10_000, lastLiveAt: null })).toContain("stopped");
    });
});

describe("startLiveRefreshTrigger -- scopes, recovery and the global notice", () => {
    it("passes its scopes to the live connection", async () => {
        startLiveRefreshTrigger({ prefixes: ["worker."], scopes: ["workers"], onMatch: () => {} });
        await settle();
        expect(connectArgs.handlers.scopes).toEqual(["workers"]);
    });

    it("refetches ONCE when the connection comes back after an outage", async () => {
        const onMatch = vi.fn();
        startLiveRefreshTrigger({ prefixes: ["worker."], onMatch, debounceMs: 10 });
        await settle();

        liveHandlers.onState("CONNECTING");
        liveHandlers.onState("LIVE");
        await vi.advanceTimersByTimeAsync(50);
        expect(onMatch).not.toHaveBeenCalled(); // the first connect is not a recovery

        liveHandlers.onState("RECONNECTING");
        await vi.advanceTimersByTimeAsync(50);
        expect(onMatch).not.toHaveBeenCalled(); // nothing to fetch yet, the backend is away
        liveHandlers.onState("LIVE");
        await vi.advanceTimersByTimeAsync(50);
        expect(onMatch).toHaveBeenCalledTimes(1);

        liveHandlers.onState("LIVE");
        await vi.advanceTimersByTimeAsync(50);
        expect(onMatch).toHaveBeenCalledTimes(1);
    });

    it("refetches after a resync too", async () => {
        const onMatch = vi.fn();
        startLiveRefreshTrigger({ prefixes: ["worker."], onMatch, debounceMs: 10 });
        await settle();
        liveHandlers.onState("LIVE");
        liveHandlers.onState("RESYNCING");
        liveHandlers.onState("LIVE");
        await vi.advanceTimersByTimeAsync(50);
        expect(onMatch).toHaveBeenCalledTimes(1);
    });

    it("publishes polling and stopped to the global notice store, and clears it on recovery", async () => {
        startLiveRefreshTrigger({
            prefixes: ["worker."], onMatch: () => {},
            fallback: { graceMs: 1000, intervalMs: 2000, maxDurationMs: 6000 },
        });
        await settle();

        liveHandlers.onState("LIVE");
        liveHandlers.onState("RECONNECTING");
        await vi.advanceTimersByTimeAsync(1000);
        expect(get(liveFallback).mode).toBe("polling");
        expect(get(liveFallback).intervalMs).toBe(2000);
        expect(get(liveFallback).lastLiveAt).not.toBeNull(); // it had been live before

        await vi.advanceTimersByTimeAsync(6000);
        expect(get(liveFallback).mode).toBe("stopped");

        liveHandlers.onState("LIVE");
        expect(get(liveFallback).mode).toBe("live");
    });

    it("stopping the page clears a standing notice", async () => {
        const stop = startLiveRefreshTrigger({ prefixes: ["worker."], onMatch: () => {}, fallback: { graceMs: 100 } });
        await settle();
        liveHandlers.onState("RECONNECTING");
        await vi.advanceTimersByTimeAsync(200);
        expect(get(liveFallback).mode).toBe("polling");
        stop();
        expect(get(liveFallback).mode).toBe("live");
    });
});
