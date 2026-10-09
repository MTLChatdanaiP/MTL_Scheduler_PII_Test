// Batch 7 (RFC-009 §20): only the newest request may change what is on screen. Starting a new one cancels the previous (so the network
// stops too) and marks it stale, so a slow answer to an OLD search can never overwrite the answer to the current one.
export interface LatestRequest {
    signal: AbortSignal;
    isCurrent(): boolean;
}

export function latestOnly(): { start(): LatestRequest } {
    let generation = 0;
    let controller: AbortController | null = null;
    return {
        start(): LatestRequest {
            controller?.abort();
            const mine = ++generation;
            const c = new AbortController();
            controller = c;
            return { signal: c.signal, isCurrent: () => mine === generation };
        },
    };
}

export function isAbort(e: unknown): boolean {
    return e instanceof DOMException && e.name === "AbortError";
}
