import { describe, it, expect } from "vitest";
import { deriveState, errorMessage } from "../src/lib/dataState";
import { ApiError } from "../src/lib/api";

describe("deriveState", () => {
    it("is INITIAL_LOADING on first fetch, before anything has loaded", () => {
        const state = deriveState({ hasLoadedOnce: false, isFetching: true, error: null, isEmpty: true });
        expect(state).toBe("INITIAL_LOADING");
    });

    it("is REFRESHING when a background auto-refresh is in flight AFTER a successful load", () => {
        const state = deriveState({ hasLoadedOnce: true, isFetching: true, error: null, isEmpty: false });
        expect(state).toBe("REFRESHING");
    });

    it("is EMPTY when loaded, not fetching, no error, but zero rows", () => {
        const state = deriveState({ hasLoadedOnce: true, isFetching: false, error: null, isEmpty: true });
        expect(state).toBe("EMPTY");
    });

    it("is READY when loaded, not fetching, no error, has rows", () => {
        const state = deriveState({ hasLoadedOnce: true, isFetching: false, error: null, isEmpty: false });
        expect(state).toBe("READY");
    });

    it("is ERROR for a generic thrown error", () => {
        const state = deriveState({ hasLoadedOnce: true, isFetching: false, error: new Error("boom"), isEmpty: false });
        expect(state).toBe("ERROR");
    });

    it("is FORBIDDEN specifically for a 403 ApiError, not generic ERROR", () => {
        const state = deriveState({
            hasLoadedOnce: true,
            isFetching: false,
            error: new ApiError("forbidden", 403),
            isEmpty: false,
        });
        expect(state).toBe("FORBIDDEN");
    });

    it("is ERROR, not FORBIDDEN, for an ApiError with a different status", () => {
        const state = deriveState({
            hasLoadedOnce: true,
            isFetching: false,
            error: new ApiError("server broke", 500),
            isEmpty: false,
        });
        expect(state).toBe("ERROR");
    });

    it("error takes priority over isEmpty -- a failed fetch is ERROR, not EMPTY, even with no data", () => {
        const state = deriveState({ hasLoadedOnce: true, isFetching: false, error: new Error("boom"), isEmpty: true });
        expect(state).toBe("ERROR");
    });

    it("error takes priority over isFetching -- a stale error is not overwritten by a background refresh", () => {
        const state = deriveState({ hasLoadedOnce: true, isFetching: true, error: new Error("boom"), isEmpty: false });
        expect(state).toBe("ERROR");
    });
});

describe("errorMessage", () => {
    it("extracts the message from a real Error", () => {
        expect(errorMessage(new Error("something specific"))).toBe("something specific");
    });

    it("returns a safe fallback for a non-Error value", () => {
        expect(errorMessage("a plain string")).toBe("Something went wrong.");
        expect(errorMessage(null)).toBe("Something went wrong.");
        expect(errorMessage(undefined)).toBe("Something went wrong.");
    });
});
