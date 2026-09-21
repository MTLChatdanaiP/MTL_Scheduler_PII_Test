import { ApiError } from "./api";

// RFC-009 §18 Frontend Data States.
export type DataState =
    | "INITIAL_LOADING"
    | "REFRESHING"
    | "EMPTY"
    | "READY"
    | "ERROR"
    | "FORBIDDEN";

export function deriveState(opts: {
    hasLoadedOnce: boolean;
    isFetching: boolean;
    error: unknown;
    isEmpty: boolean;
}): DataState {
    if (opts.error) {
        if (opts.error instanceof ApiError && opts.error.status === 403) return "FORBIDDEN";
        return "ERROR";
    }
    if (!opts.hasLoadedOnce && opts.isFetching) return "INITIAL_LOADING";
    if (opts.hasLoadedOnce && opts.isFetching) return "REFRESHING";
    if (opts.isEmpty) return "EMPTY";
    return "READY";
}

// EMPTY and ERROR must never render identically (§18)
export function errorMessage(error: unknown): string {
    if (error instanceof Error) return error.message;
    return "Something went wrong.";
}