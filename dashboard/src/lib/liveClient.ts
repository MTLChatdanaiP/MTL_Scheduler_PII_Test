import { apiStream, ApiError, type SnapshotHandoff } from "./api";
import { createSSEParser } from "./sseParser";
import { buildLivePath, createStaleGuard, type LiveEnvelopeFields } from "./liveEnvelope";

export type LiveState = "CONNECTING" | "LIVE" | "RECONNECTING" | "RESYNCING" | "FORBIDDEN" | "CLOSED";

// id/type/subject/at are what every server version sends. The RFC-010 §10 envelope fields are optional extras (see LiveEnvelopeFields).
export interface LiveEvent extends LiveEnvelopeFields {
    id: number;
    type: string;
    subject: string;
    at: string;
}

export interface LiveOptions {
    path: string; // "/live/activity" or "/live/alerts"
    // RFC-010 §15: what to subscribe to, e.g. ["workers"] or ["queue:orders", "platform.summary"]. Omitted = everything the key may see.
    scopes?: string[];
    onEvent: (event: LiveEvent) => void;
    onState: (state: LiveState) => void; // fires only when the state CHANGES
    // Fires on every chunk received from the server, INCLUDING keepalive pings.
    // "Last confirmed" in the UI means the last time this fired: a quiet but
    // healthy connection is still confirmed every ~20s by the ping.
    onConfirm?: (at: number) => void;
    // Called when the server says the gap is too large to replay. Must
    // return a fresh snapshot; its watermark becomes the new cursor.
    onResync: () => Promise<SnapshotHandoff>;
}

const STALL_TIMEOUT_MS = 45_000; // server pings every 20s; silence this long = dead connection
const RESUME_OVERLAP = 5; // reconnect slightly BEHIND the cursor -- duplicates are dropped by id, gaps are not
const MAX_BACKOFF_MS = 30_000;
const SEEN_CAP = 1000;

function sleep(ms: number, signal: AbortSignal): Promise<void> {
    return new Promise((resolve) => {
        const t = setTimeout(resolve, ms);
        signal.addEventListener("abort", () => { clearTimeout(t); resolve(); }, { once: true });
    });
}

// Requires a SnapshotHandoff, not a bare number: a live connection can only
// be opened from a snapshot fetched first (RFC-010 §16).
export function connectLive(handoff: SnapshotHandoff, opts: LiveOptions): () => void {
    const lifetime = new AbortController();
    let cursor = handoff.watermark;
    let attempt = 0;
    let everConnected = false;
    let resyncPending = false;
    let lastEmitted: LiveState | null = null;
    const seen = new Set<number>();
    const stale = createStaleGuard(); // RFC-010 §10: reject an update older than what we already applied for the same resource

    // Only report real changes, so callers never see the same state twice in a row.
    function emit(state: LiveState) {
        if (state === lastEmitted) return;
        lastEmitted = state;
        opts.onState(state);
    }

    function isNew(id: number): boolean {
        if (id <= 0) return true; // id-less events (debug publishes) can't be deduped
        if (seen.has(id)) return false;
        seen.add(id);
        if (seen.size > SEEN_CAP) seen.delete(seen.values().next().value as number);
        return true;
    }

    async function run() {
        while (!lifetime.signal.aborted) {
            emit(resyncPending ? "RESYNCING" : everConnected ? "RECONNECTING" : "CONNECTING");

            const conn = new AbortController();
            const abortConn = () => conn.abort();
            lifetime.signal.addEventListener("abort", abortConn, { once: true });

            let watchdog: ReturnType<typeof setTimeout> | undefined;
            const arm = () => { clearTimeout(watchdog); watchdog = setTimeout(abortConn, STALL_TIMEOUT_MS); };

            let resynced = false;

            try {
                arm();
                const after = Math.max(0, cursor - RESUME_OVERLAP);
                const res = await apiStream(buildLivePath(opts.path, after, opts.scopes), conn.signal);
                const reader = res.body!.getReader();
                const decoder = new TextDecoder();
                const parser = createSSEParser();

                everConnected = true;
                resyncPending = false;
                attempt = 0;
                emit("LIVE");

                while (true) {
                    const { done, value } = await reader.read();
                    if (done) break;
                    arm();
                    opts.onConfirm?.(Date.now());

                    for (const msg of parser.feed(decoder.decode(value, { stream: true }))) {
                        if (msg.event === "resync") {
                            resyncPending = true;
                            emit("RESYNCING");
                            const fresh = await opts.onResync();
                            cursor = fresh.watermark;
                            seen.clear();
                            stale.reset();
                            resynced = true;
                            break;
                        }
                        const e = JSON.parse(msg.data) as LiveEvent;
                        if (!isNew(e.id)) continue;
                        if (e.id > cursor) cursor = e.id;
                        if (!stale.accept(e)) continue;
                        opts.onEvent(e);
                    }
                    if (resynced) break;
                }
            } catch (err) {
                if (lifetime.signal.aborted) return;
                if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
                    emit("FORBIDDEN");
                    return; // a credential problem is not fixed by retrying
                }
                // network drop, 5xx, stall abort, malformed frame: fall through to backoff
            } finally {
                clearTimeout(watchdog);
                lifetime.signal.removeEventListener("abort", abortConn);
                conn.abort();
            }

            if (resynced) continue; // reconnect immediately from the fresh snapshot

            const delay = Math.min(MAX_BACKOFF_MS, 1000 * 2 ** attempt) * (0.5 + Math.random() / 2);
            attempt++;
            await sleep(delay, lifetime.signal);
        }
    }

    void run();

    return () => {
        lifetime.abort();
        emit("CLOSED");
    };
}