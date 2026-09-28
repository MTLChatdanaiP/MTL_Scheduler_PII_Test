// @vitest-environment node
// (jsdom's AbortController is not accepted by Node's fetch, so this file runs in plain node.)
import { describe, it, expect, vi, afterEach } from "vitest";
import http from "node:http";
import type { AddressInfo } from "node:net";

const server = vi.hoisted(() => ({ base: "" }));

// Same contract as the real apiStream in api.ts: send X-API-Key, and turn a
// non-2xx response into an ApiError carrying the status.
vi.mock("../src/lib/api", () => {
    class ApiError extends Error {
        status: number;
        constructor(message: string, status: number) {
            super(message);
            this.status = status;
            this.name = "ApiError";
        }
    }
    return {
        ApiError,
        apiStream: async (path: string, signal: AbortSignal) => {
            const res = await fetch(server.base + path, {
                headers: { "X-API-Key": "k", Accept: "text/event-stream" },
                signal,
            });
            if (!res.ok || !res.body) {
                const body = (await res.json().catch(() => ({}))) as { error?: string };
                throw new ApiError(body.error || `request failed: ${res.status}`, res.status);
            }
            return res;
        },
    };
});

import { connectLive, type LiveState } from "../src/lib/liveClient";

interface Recorded {
    after: string | null;
    key: string | undefined;
    accept: string | undefined;
}

let openServers: http.Server[] = [];

function startServer(handler: (n: number, res: http.ServerResponse) => void) {
    return new Promise<{ seen: Recorded[] }>((resolve) => {
        const seen: Recorded[] = [];
        const srv = http.createServer((req, res) => {
            const url = new URL(req.url ?? "", "http://x");
            seen.push({
                after: url.searchParams.get("after"),
                key: req.headers["x-api-key"] as string | undefined,
                accept: req.headers["accept"] as string | undefined,
            });
            handler(seen.length, res);
        });
        openServers.push(srv);
        srv.listen(0, "127.0.0.1", () => {
            server.base = `http://127.0.0.1:${(srv.address() as AddressInfo).port}`;
            resolve({ seen });
        });
    });
}

const sseHead = (res: http.ServerResponse) => res.writeHead(200, { "Content-Type": "text/event-stream" });
const ev = (id: number, type = "alert.opened") =>
    `id: ${id}\nevent: ${type}\ndata: ${JSON.stringify({ id, type, subject: "s", at: "t" })}\n\n`;
const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));

async function until(cond: () => boolean, ms = 4000) {
    const t0 = Date.now();
    while (!cond()) {
        if (Date.now() - t0 > ms) throw new Error("timed out waiting for condition");
        await wait(20);
    }
}

afterEach(() => {
    for (const s of openServers) {
        s.closeAllConnections();
        s.close();
    }
    openServers = [];
});

describe("connectLive", () => {
    it("after a drop it reconnects from (cursor - 5), delivers no duplicates, and sends the API key every time", async () => {
        const { seen } = await startServer((n, res) => {
            sseHead(res);
            if (n === 1) {
                res.write(": connected\n\n" + ev(11) + ev(12));
                setTimeout(() => res.destroy(), 50);
            } else {
                res.write(ev(11) + ev(12) + ev(13));
            }
        });

        const got: number[] = [];
        const states: LiveState[] = [];
        const stop = connectLive(
            { overview: {} as never, watermark: 10 },
            {
                path: "/live/activity",
                onEvent: (e) => got.push(e.id),
                onState: (s) => states.push(s),
                onResync: async () => {
                    throw new Error("unexpected resync");
                },
            },
        );

        await until(() => got.length >= 3);
        stop();

        expect(got).toEqual([11, 12, 13]); // 11 and 12 were replayed but not delivered twice
        expect(seen[0].after).toBe("5"); // watermark 10 - 5
        expect(seen[1].after).toBe("7"); // cursor 12 - 5
        expect(seen.every((r) => r.key === "k")).toBe(true);
        expect(seen.every((r) => r.accept === "text/event-stream")).toBe(true);
        expect(states.slice(0, 4)).toEqual(["CONNECTING", "LIVE", "RECONNECTING", "LIVE"]);
    });

    it("treats 403 as terminal: FORBIDDEN and no retry loop", async () => {
        const { seen } = await startServer((_n, res) => {
            res.writeHead(403, { "Content-Type": "application/json" });
            res.end(JSON.stringify({ error: "forbidden" }));
        });

        const states: LiveState[] = [];
        const stop = connectLive(
            { overview: {} as never, watermark: 0 },
            {
                path: "/live/activity",
                onEvent: () => {},
                onState: (s) => states.push(s),
                onResync: async () => ({ overview: {} as never, watermark: 0 }),
            },
        );

        await wait(1600); // longer than the first backoff, so a retry would have happened by now
        stop();

        expect(seen).toHaveLength(1);
        expect(states).toContain("FORBIDDEN");
    });

    it("on a resync frame it goes RESYNCING, refetches, and reconnects immediately from the fresh watermark", async () => {
        const { seen } = await startServer((n, res) => {
            sseHead(res);
            if (n === 1) res.write("event: resync\ndata: {}\n\n");
            else res.write(ev(101));
        });

        const got: number[] = [];
        const states: LiveState[] = [];
        let resyncs = 0;
        const t0 = Date.now();
        const stop = connectLive(
            { overview: {} as never, watermark: 1 },
            {
                path: "/live/activity",
                onEvent: (e) => got.push(e.id),
                onState: (s) => states.push(s),
                onResync: async () => {
                    resyncs++;
                    return { overview: {} as never, watermark: 100 };
                },
            },
        );

        await until(() => got.length === 1);
        const elapsed = Date.now() - t0;
        stop();

        expect(resyncs).toBe(1);
        expect(seen[1].after).toBe("95"); // fresh watermark 100 - 5
        expect(got).toEqual([101]);
        expect(elapsed).toBeLessThan(450); // no backoff (the first backoff is at least 500ms)
        expect(states.slice(0, 4)).toEqual(["CONNECTING", "LIVE", "RESYNCING", "LIVE"]); // each state reported once
    });

    it("counts keepalive pings as confirmation, even though they are not events", async () => {
        await startServer((_n, res) => {
            sseHead(res);
            res.write(": connected\n\n");
            setTimeout(() => res.write(": ping\n\n"), 80);
        });

        const confirms: number[] = [];
        const events: number[] = [];
        const stop = connectLive(
            { overview: {} as never, watermark: 0 },
            {
                path: "/live/activity",
                onEvent: (e) => events.push(e.id),
                onConfirm: (at) => confirms.push(at),
                onState: () => {},
                onResync: async () => ({ overview: {} as never, watermark: 0 }),
            },
        );

        await until(() => confirms.length >= 2);
        stop();

        expect(events).toEqual([]);
        expect(confirms.length).toBeGreaterThanOrEqual(2);
        expect(confirms.every((t) => Math.abs(t - Date.now()) < 5000)).toBe(true);
    });
});