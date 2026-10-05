import { ApiError } from "./api";

// RFC-009 §18 Frontend Data States (PRD §43.1 names 8 states; this file now
// implements all 8 -- STALE and PARTIAL were the 2 missing).
export type DataState =
    | "INITIAL_LOADING"
    | "REFRESHING"
    | "EMPTY"
    | "READY"
    | "STALE"
    | "PARTIAL"
    | "ERROR"
    | "FORBIDDEN";

// The original 6-value type, kept under its own name so every existing page
// -- which calls deriveState with only these 4 fields, and passes the result
// into DataStateBanner's narrower prop type -- keeps inferring exactly the
// type it always did. Nothing about them needs to change.
export type LegacyDataState = Exclude<DataState, "STALE" | "PARTIAL">;

interface LegacyOpts {
    hasLoadedOnce: boolean;
    isFetching: boolean;
    error: unknown;
    isEmpty: boolean;
}

interface FullOpts extends LegacyOpts {
    // Only present once a page deliberately opts in (see the file-level
    // comment below for why). Their presence is what tells TypeScript, at
    // each call site, which of the two return types below applies.
    hasLoadedSuccessfully?: boolean;
    isPartial?: boolean;
}

// Two overloads sharing one implementation: a call that doesn't mention the
// 2 new fields at all gets the original narrow return type inferred, byte
// for byte as before; a call that does gets the full 8-value type. This is
// what keeps every untouched page compiling exactly as it did before this
// file changed, with zero edits to those pages.
export function deriveState(opts: LegacyOpts): LegacyDataState;
export function deriveState(opts: FullOpts): DataState;
export function deriveState(opts: FullOpts): DataState {
    if (opts.error) {
        if (opts.error instanceof ApiError && opts.error.status === 403) return "FORBIDDEN";
        // A refresh failed, but we already have good data on screen from a
        // prior successful load -- keep showing it, marked stale, instead of
        // replacing the whole view with an error (PRD §43.1).
        if (opts.hasLoadedSuccessfully) return "STALE";
        return "ERROR";
    }
    if (!opts.hasLoadedOnce && opts.isFetching) return "INITIAL_LOADING";
    if (opts.hasLoadedOnce && opts.isFetching) return "REFRESHING";
    if (opts.isEmpty) return "EMPTY";
    if (opts.isPartial) return "PARTIAL";
    return "READY";
}

// EMPTY and ERROR must never render identically (§18)
export function errorMessage(error: unknown): string {
    if (error instanceof Error) return error.message;
    return "Something went wrong.";
}