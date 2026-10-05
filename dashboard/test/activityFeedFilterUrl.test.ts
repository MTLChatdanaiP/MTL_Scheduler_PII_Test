// @vitest-environment jsdom
//
// Exercises the exact hidden-categories encoding, subject and view logic
// ActivityFeed.svelte uses, against the real router.ts -- same approach as
// queueFilterUrl.test.ts. As with that file, navigate()/updateParams() set
// window.location.hash synchronously but the currentPath STORE only updates
// once the browser's hashchange event actually fires (one task later, true
// in a real browser too) -- tests that read currentPath right after a write
// await one tick, same as the real browser would deliver it.
import { describe, it, expect, beforeEach } from "vitest";
import { currentPath, parsePath, updateParams } from "../src/lib/router";
import { get } from "svelte/store";

const CATEGORIES = ["task", "alert", "pii", "other"] as const;
type Category = (typeof CATEGORIES)[number];

beforeEach(() => {
    window.location.hash = "";
    currentPath.set("/live-activity");
});

async function tick() {
    await new Promise((r) => setTimeout(r, 0));
}

function syncFiltersFromUrl() {
    const { params } = parsePath(get(currentPath));
    const hidden = new Set((params.get("hidden") ?? "").split(",").filter(Boolean));
    const show: Record<Category, boolean> = {
        task: !hidden.has("task"),
        alert: !hidden.has("alert"),
        pii: !hidden.has("pii"),
        other: !hidden.has("other"),
    };
    const subject = params.get("subject") ?? "";
    const view = params.get("view") === "graph" ? "graph" : "list";
    return { show, subject, view };
}

function commitUrl(show: Record<Category, boolean>, subject: string, view: "list" | "graph") {
    updateParams({
        hidden: CATEGORIES.filter((c) => !show[c]).join(","),
        subject,
        view: view === "graph" ? "graph" : "",
    });
}

describe("ActivityFeed filter <-> URL round trip (real router.ts)", () => {
    it("a bare URL means every category visible, no subject, list view", () => {
        expect(syncFiltersFromUrl()).toEqual({
            show: { task: true, alert: true, pii: true, other: true },
            subject: "",
            view: "list",
        });
    });

    it("hiding one category writes only that one to 'hidden'", async () => {
        commitUrl({ task: true, alert: true, pii: false, other: true }, "", "list");
        await tick();
        const { params } = parsePath(get(currentPath));
        expect(params.get("hidden")).toBe("pii");
    });

    it("hiding several categories round-trips exactly, in CATEGORIES order", async () => {
        commitUrl({ task: false, alert: true, pii: false, other: false }, "", "list");
        await tick();
        expect(syncFiltersFromUrl().show).toEqual({ task: false, alert: true, pii: false, other: false });
    });

    it("view=graph round-trips; the default 'list' is omitted from the URL entirely", async () => {
        commitUrl({ task: true, alert: true, pii: true, other: true }, "", "graph");
        await tick();
        expect(get(currentPath)).not.toContain("view=list");
        expect(syncFiltersFromUrl().view).toBe("graph");

        commitUrl({ task: true, alert: true, pii: true, other: true }, "", "list");
        await tick();
        expect(get(currentPath)).not.toContain("view=");
    });

    it("subject text round-trips, URL-encoded", async () => {
        commitUrl({ task: true, alert: true, pii: true, other: true }, "01M3K8TGE", "list");
        await tick();
        expect(syncFiltersFromUrl().subject).toBe("01M3K8TGE");
    });

    it("a category added to CATEGORIES in the future defaults to visible on an OLD shared link", () => {
        // Simulates a link shared back when only 3 categories existed, with
        // "other" hidden -- CATEGORIES has since gained a 4th. Nothing in the
        // URL mentions it, so it must default to shown, not silently hidden.
        currentPath.set("/live-activity?hidden=other");
        const hidden = new Set((parsePath(get(currentPath)).params.get("hidden") ?? "").split(",").filter(Boolean));
        const EXTENDED = [...CATEGORIES, "newcategory"] as const;
        const show = Object.fromEntries(EXTENDED.map((c) => [c, !hidden.has(c)]));
        expect(show.newcategory).toBe(true);
        expect(show.other).toBe(false);
    });

    it("clearing all filters removes 'hidden' and 'subject' from the URL entirely", async () => {
        commitUrl({ task: false, alert: true, pii: true, other: true }, "abc", "list");
        await tick();
        commitUrl({ task: true, alert: true, pii: true, other: true }, "", "list");
        await tick();
        expect(get(currentPath)).toBe("/live-activity");
    });

    it("does not clobber an unrelated param already in the URL", async () => {
        currentPath.set("/live-activity?time_range=1h");
        commitUrl({ task: true, alert: true, pii: false, other: true }, "", "list");
        await tick();
        const { params } = parsePath(get(currentPath));
        expect(params.get("time_range")).toBe("1h");
        expect(params.get("hidden")).toBe("pii");
    });

    it("opening a shared link with filters already in the query restores them", () => {
        currentPath.set("/live-activity?hidden=alert%2Cother&subject=Consumer-a&view=graph");
        expect(syncFiltersFromUrl()).toEqual({
            show: { task: true, alert: false, pii: true, other: false },
            subject: "Consumer-a",
            view: "graph",
        });
    });
});
