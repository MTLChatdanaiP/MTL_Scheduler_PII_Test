// @vitest-environment node
// (same reason as ClientLiveFeed.test.ts: Node's fetch needs Node's AbortController.)
import { describe, it, expect, vi, afterEach } from "vitest";
import http from "node:http";
import type { AddressInfo } from "node:net";

const server = vi.hoisted(() => ({ base: "" }));

vi.mock("../src/lib/api", () => {
    class ApiError extends Error {
        status: number;
        constructor(message: string, status: number) {
            super(message);
            this.status = status;
        }
    }
    return {
        ApiError,
        apiStream: async (path: string, signal: AbortSignal) => {
            const res = await fetch(server.base + path, { headers: { "X-API-Key": "k" }, signal });
            if (!res.ok || !res.body) throw new ApiError("failed", res.status);
            return res;
        },
    };
});

import { connectLive } from "../src/lib/liveClient";

let openServers: http.Server[] = [];

function startServer(handler: (n: number, res: http.ServerResponse) => void) {
    return new Promise<{ urls: string[] }>((resolve) => {
        const urls: string[] = [];
        const srv = http.createServer((req, res) => {
            urls.push(req.url ?? "");
            handler(urls.length, res);
        });
        openServers.push(srv);
        srv.listen(0, "127.0.0.1", () => {
            server.base = `http://127.0.0.1:${(srv.address() as AddressInfo).port}`;
            resolve({ urls });
        });
    });
}

const frame = (data: Record<string, unknown>, id?: number) =>
    (id ? `id: ${id}\n` : "") + `event: ${data.type}\ndata: ${JSON.stringify(data)}\n\n`;
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

const handoff = { overview: {} as never, watermark: 10 };
const noResync = async () => {
    throw new Error("unexpected resync");
};

describe("connectLive -- Batch 6", () => {
    it("sends every scope as a repeated query parameter, after the cursor", async () => {
        const { urls } = await startServer((_n, res) => {
            res.writeHead(200, { "Content-Type": "text/event-stream" });
            res.write(": connected\n\n");
        });
        const stop = connectLive(handoff, {
            path: "/live/activity",
            scopes: ["workers", "queue:orders"],
            onEvent: () => {},
            onState: () => {},
            onResync: noResync,
        });
        await until(() => urls.length >= 1);
        stop();

        const q = new URL(urls[0], "http://x").searchParams;
        expect(q.get("after")).toBe("5");
        expect(q.getAll("scope")).toEqual(["workers", "queue:orders"]);
    });

    it("without scopes the request is exactly what it always was", async () => {
        const { urls } = await startServer((_n, res) => {
            res.writeHead(200, { "Content-Type": "text/event-stream" });
            res.write(": connected\n\n");
        });
        const stop = connectLive(handoff, { path: "/live/activity", onEvent: () => {}, onState: () => {}, onResync: noResync });
        await until(() => urls.length >= 1);
        stop();
        expect(urls[0]).toBe("/live/activity?after=5");
    });

    it("drops a late, older update for the same resource but delivers the rest", async () => {
        await startServer((_n, res) => {
            res.writeHead(200, { "Content-Type": "text/event-stream" });
            const mk = (id: number, seq: number, type: string) => ({
                id, type, subject: "orders", at: "t", change_seq: seq, resource_type: "QUEUE", resource_id: "orders",
            });
            res.write(frame(mk(20, 20, "queue.degraded"), 20) + frame(mk(18, 18, "queue.healthy"), 18) + frame(mk(21, 21, "queue.no_consumer"), 21));
        });
        const got: string[] = [];
        const stop = connectLive(handoff, { path: "/live/activity", onEvent: (e) => got.push(e.type), onState: () => {}, onResync: noResync });
        await until(() => got.length >= 2);
        stop();
        expect(got).toEqual(["queue.degraded", "queue.no_consumer"]); // the out-of-order id-18 update never reached the page
    });

    it("delivers every id-0 platform.summary frame (they are not recorded facts, so they are never duplicates)", async () => {
        await startServer((_n, res) => {
            res.writeHead(200, { "Content-Type": "text/event-stream" });
            const summary = { id: 0, type: "platform.summary", subject: "platform", at: "t", change_seq: 0, resource_type: "PLATFORM_SUMMARY", resource_id: "platform" };
            res.write(frame(summary) + frame(summary) + frame(summary));
        });
        const got: string[] = [];
        const stop = connectLive(handoff, { path: "/live/activity", scopes: ["platform.summary"], onEvent: (e) => got.push(e.type), onState: () => {}, onResync: noResync });
        await until(() => got.length >= 3);
        stop();
        expect(got).toEqual(["platform.summary", "platform.summary", "platform.summary"]);
    });

    it("still works against an older backend that sends only id/type/subject/at", async () => {
        await startServer((_n, res) => {
            res.writeHead(200, { "Content-Type": "text/event-stream" });
            res.write(frame({ id: 31, type: "worker.offline", subject: "w1", at: "t" }, 31));
        });
        const got: number[] = [];
        const stop = connectLive(handoff, { path: "/live/activity", onEvent: (e) => got.push(e.id), onState: () => {}, onResync: noResync });
        await until(() => got.length >= 1);
        stop();
        expect(got).toEqual([31]);
    });
});
