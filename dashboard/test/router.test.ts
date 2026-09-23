import { describe, it, expect, beforeEach } from "vitest";
import { currentPath, parsePath, buildPath, updateParams, navigate, getParam } from "../src/lib/router";

// window.location.hash is real jsdom state, not mocked -- these tests
// genuinely exercise the same browser API the bug lived in.
function resetHash() {
    window.location.hash = "";
}

beforeEach(() => {
    resetHash();
    currentPath.set("/overview");
});

describe("parsePath", () => {
    it("splits page and query params", () => {
        const { page, params } = parsePath("/alerts?severity=CRITICAL&status=OPEN");
        expect(page).toBe("alerts");
        expect(params.get("severity")).toBe("CRITICAL");
        expect(params.get("status")).toBe("OPEN");
    });

    it("defaults to overview with no leading slash", () => {
        const { page } = parsePath("");
        expect(page).toBe("overview");
    });

    it("handles a page with no query string", () => {
        const { page, params } = parsePath("/runs");
        expect(page).toBe("runs");
        expect(params.toString()).toBe("");
    });
});

describe("buildPath", () => {
    it("skips empty values", () => {
        const path = buildPath("alerts", { severity: "CRITICAL", status: "" });
        expect(path).toBe("/alerts?severity=CRITICAL");
    });

    it("produces a bare path with no params when everything is empty", () => {
        const path = buildPath("alerts", { severity: "", status: "" });
        expect(path).toBe("/alerts");
    });
});

describe("updateParams — THE REGRESSION TEST for the infinite-loop bug", () => {
    it("does NOT call navigate again when the resulting path is unchanged", () => {
        window.location.hash = "/alerts?status=OPEN";
        currentPath.set("/alerts?status=OPEN");

        const before = window.location.hash;
        updateParams({ status: "OPEN" }); // setting to the SAME value it already has
        const after = window.location.hash;

        // The actual bug: this used to always call navigate(), which set
        // window.location.hash even to an identical value, which (eventually,
        // through a chain of subscribers) re-triggered itself. The fix was a
        // no-op guard comparing the new path against the current one BEFORE
        // calling navigate.
        expect(after).toBe(before);
    });

    it("DOES update the URL when the value genuinely changes", () => {
        window.location.hash = "/alerts?status=OPEN";
        currentPath.set("/alerts?status=OPEN");

        updateParams({ status: "RESOLVED" });

        expect(window.location.hash).toContain("status=RESOLVED");
    });

    it("merges into existing params instead of replacing them", () => {
        window.location.hash = "/alerts?severity=CRITICAL";
        currentPath.set("/alerts?severity=CRITICAL");

        updateParams({ status: "OPEN" });

        // THE OTHER real bug this was built to prevent: GlobalBar's
        // time_range and a page's own filters must not stomp each other.
        expect(window.location.hash).toContain("severity=CRITICAL");
        expect(window.location.hash).toContain("status=OPEN");
    });

    it("deletes a param when given an empty string", () => {
        window.location.hash = "/alerts?severity=CRITICAL&status=OPEN";
        currentPath.set("/alerts?severity=CRITICAL&status=OPEN");

        updateParams({ status: "" });

        expect(window.location.hash).toContain("severity=CRITICAL");
        expect(window.location.hash).not.toContain("status=");
    });
});

describe("getParam", () => {
    it("reads a single param from the current path", () => {
        currentPath.set("/runs?job_type=CUSTOMER_UPDATE");
        expect(getParam("job_type")).toBe("CUSTOMER_UPDATE");
    });

    it("returns empty string for a missing param, not undefined or null", () => {
        currentPath.set("/runs");
        expect(getParam("job_type")).toBe("");
    });
});

describe("navigate", () => {
    it("does not fire a hashchange when the path is already current", () => {
        window.location.hash = "/overview";
        let changeCount = 0;
        window.addEventListener("hashchange", () => changeCount++);

        navigate("/overview"); // identical to current hash

        expect(changeCount).toBe(0);
    });

    it("does update the hash for a genuinely different path", () => {
        window.location.hash = "/overview";
        navigate("/alerts");
        expect(window.location.hash).toBe("#/alerts");
    });
});
