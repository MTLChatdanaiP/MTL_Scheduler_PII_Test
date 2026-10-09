import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { writable } from "svelte/store";
import { startRefresh } from "../src/lib/refresh";
import { autoRefreshEnabled } from "../src/lib/stores";

beforeEach(() => {
    vi.useFakeTimers();
    autoRefreshEnabled.set(true);
});
afterEach(() => vi.useRealTimers());

describe("startRefresh (RFC-009 §17)", () => {
    it("reloads when a tick changes, but not for the initial subscribe", () => {
        const tick = writable(0);
        const load = vi.fn();
        const stop = startRefresh(load, { ticks: [tick] });
        expect(load).not.toHaveBeenCalled();
        tick.set(1);
        expect(load).toHaveBeenCalledTimes(1);
        stop();
    });

    it("reloads on the gauge interval", () => {
        const load = vi.fn();
        const stop = startRefresh(load, { gaugeEveryMs: 1000 });
        vi.advanceTimersByTime(3100);
        expect(load).toHaveBeenCalledTimes(3);
        stop();
    });

    it("defaults to a slow 30s safety net", () => {
        const load = vi.fn();
        const stop = startRefresh(load);
        vi.advanceTimersByTime(29_000);
        expect(load).not.toHaveBeenCalled();
        vi.advanceTimersByTime(1_500);
        expect(load).toHaveBeenCalledTimes(1);
        stop();
    });

    it("does nothing while Auto-refresh is switched off", () => {
        const tick = writable(0);
        const load = vi.fn();
        const stop = startRefresh(load, { ticks: [tick], gaugeEveryMs: 1000 });
        autoRefreshEnabled.set(false);
        tick.set(1);
        vi.advanceTimersByTime(5000);
        expect(load).not.toHaveBeenCalled();
        stop();
    });

    it("stops listening and polling once cleaned up", () => {
        const tick = writable(0);
        const load = vi.fn();
        const stop = startRefresh(load, { ticks: [tick], gaugeEveryMs: 1000 });
        stop();
        tick.set(1);
        vi.advanceTimersByTime(5000);
        expect(load).not.toHaveBeenCalled();
    });
});
