// RFC-010 §10 / §13 / §15: the live envelope on the client, and the small pure helpers around it.

// The extra fields the server now adds to every live event. All optional: an older backend sends only id/type/subject/at, and
// everything below must keep working against it.
export interface LiveEnvelopeFields {
    live_event_id?: string;
    schema_version?: number;
    published_at?: string;
    observed_at?: string;
    resource_type?: string;
    resource_id?: string;
    change_type?: string; // INVALIDATE | HEALTH_CHANGED
    change_seq?: number;
    payload_mode?: string; // always NONE: the client refetches, it never applies a payload
    freshness?: { status: string; projection_lag_ms: number }; // FRESH | LAGGING | REPLAYED
    execution_chain_id?: string;
    attempt_id?: string;
}

// Builds "/live/activity?after=12&scope=workers&scope=queue:orders". `scope` is repeatable (RFC-010 §15); no scopes means the
// route's old behaviour, exactly. Values are encoded, so an id with odd characters cannot break the URL.
export function buildLivePath(path: string, after: number, scopes: string[] = []): string {
    const params = new URLSearchParams();
    params.set("after", String(after));
    for (const s of scopes) params.append("scope", s);
    return `${path}?${params.toString()}`;
}

// RFC-010 §10 "stale-update rejection". change_seq is the server's event id: monotonic across the whole system. If an update for a
// resource arrives with a change_seq no greater than one already seen for THAT resource, it is older than (or the same as) what the
// screen already reflects and is dropped.
//
// Events without the envelope fields (an older backend) and the synthetic platform summary (id 0, not a recorded fact) are always
// accepted: there is nothing to compare.
const GUARD_CAP = 5000;

export function createStaleGuard() {
    const lastSeq = new Map<string, number>();

    return {
        accept(e: { id: number; change_seq?: number; resource_type?: string; resource_id?: string }): boolean {
            if (!e.id || e.id <= 0) return true;
            if (e.change_seq === undefined || !e.resource_type) return true;

            const key = `${e.resource_type}:${e.resource_id ?? ""}`;
            const last = lastSeq.get(key);
            if (last !== undefined && e.change_seq <= last) return false;

            lastSeq.delete(key); // re-insert so the oldest key is always first
            lastSeq.set(key, e.change_seq);
            if (lastSeq.size > GUARD_CAP) lastSeq.delete(lastSeq.keys().next().value as string);
            return true;
        },
        reset() {
            lastSeq.clear();
        },
    };
}
