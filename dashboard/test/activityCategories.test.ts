import { describe, it, expect } from "vitest";
import {
    CATEGORIES, categoryOf, emptyCounts, allVisible, showFromHidden, hiddenFromShow,
    filterEvents, countByCategory, bucketEvents,
} from "../src/lib/activity";
import type { LiveEvent } from "../src/lib/liveClient";

const ev = (id: number, type: string, subject = "s"): LiveEvent => ({ id, type, subject, at: "2026-10-06T12:00:00Z" });

// Batches 3 and 4 added real attempt.* and schedule.* events. The feed filed everything except task/alert/pii under
// "other", so they were one undifferentiated bucket with every queue and worker event.
describe("categoryOf: the new event families", () => {
    it("gives attempt events and schedule events their own categories", () => {
        for (const t of ["attempt.claimed", "attempt.started", "attempt.succeeded", "attempt.failed", "attempt.abandoned", "attempt.duplicate_detected"]) {
            expect(categoryOf(t)).toBe("attempt");
        }
        for (const t of ["schedule.created", "schedule.updated", "schedule.enabled", "schedule.occurrence_due", "schedule.occurrence_run_created", "schedule.occurrence_skipped", "schedule.missed"]) {
            expect(categoryOf(t)).toBe("schedule");
        }
    });

    it("groups the platform's own plumbing as system", () => {
        for (const t of ["queue.degraded", "worker.offline", "component.offline", "monitoring.degraded", "redis.unavailable", "delivery.malformed", "retention.pruned"]) {
            expect(categoryOf(t)).toBe("system");
        }
    });

    it("files run.* with task.*, since both describe a run", () => {
        expect(categoryOf("run.lost")).toBe("task");
        expect(categoryOf("task.blocked")).toBe("task");
        expect(categoryOf("task.published")).toBe("task");
    });

    it("keeps the existing categories and a true fallback", () => {
        expect(categoryOf("alert.opened")).toBe("alert");
        expect(categoryOf("pii.policy_drift_detected")).toBe("pii");
        expect(categoryOf("totally.new")).toBe("other");
        expect(categoryOf("")).toBe("other");
    });

    it("never classifies a family by a prefix match on a longer name", () => {
        // "attempts" and "taskforce" are not the families "attempt" and "task"
        expect(categoryOf("attempts.x")).toBe("other");
        expect(categoryOf("taskforce.x")).toBe("other");
    });
});

describe("the category list drives everything", () => {
    it("emptyCounts and allVisible cover every category and nothing else", () => {
        expect(Object.keys(emptyCounts()).sort()).toEqual([...CATEGORIES].sort());
        expect(Object.keys(allVisible()).sort()).toEqual([...CATEGORIES].sort());
        expect(Object.values(emptyCounts()).every((n) => n === 0)).toBe(true);
        expect(Object.values(allVisible()).every((v) => v === true)).toBe(true);
    });

    it("counts each new category separately", () => {
        const c = countByCategory([ev(1, "attempt.claimed"), ev(2, "attempt.failed"), ev(3, "schedule.created"), ev(4, "worker.online"), ev(5, "task.created")]);
        expect(c).toMatchObject({ attempt: 2, schedule: 1, system: 1, task: 1, other: 0 });
    });

    it("filters by the new categories", () => {
        const events = [ev(1, "attempt.claimed"), ev(2, "schedule.created"), ev(3, "task.created")];
        const show = { ...allVisible(), attempt: false };
        expect(filterEvents(events, show, "").map((e) => e.id)).toEqual([2, 3]);
    });

    it("buckets the new categories too", () => {
        const now = Date.parse("2026-10-06T12:00:30Z");
        const [last] = bucketEvents([ev(1, "attempt.started"), ev(2, "schedule.missed")], now, 60000, 1);
        expect(last.counts.attempt).toBe(1);
        expect(last.counts.schedule).toBe(1);
        expect(last.total).toBe(2);
    });
});

describe("the hidden-categories URL encoding", () => {
    it("an absent param means everything is visible", () => {
        expect(showFromHidden(null)).toEqual(allVisible());
        expect(showFromHidden("")).toEqual(allVisible());
        expect(showFromHidden(undefined)).toEqual(allVisible());
    });

    it("round-trips exactly, in CATEGORIES order", () => {
        const show = { ...allVisible(), attempt: false, system: false };
        const encoded = hiddenFromShow(show);
        expect(encoded).toBe("attempt,system");
        expect(showFromHidden(encoded)).toEqual(show);
    });

    it("an old shared link that hid 'other' still hides it, and shows the categories added since", () => {
        const show = showFromHidden("other");
        expect(show.other).toBe(false);
        expect(show.attempt).toBe(true);
        expect(show.schedule).toBe(true);
        expect(show.system).toBe(true);
    });

    it("ignores a name it does not know instead of breaking", () => {
        expect(showFromHidden("nonsense,task").task).toBe(false);
        expect(Object.keys(showFromHidden("nonsense"))).toHaveLength(CATEGORIES.length);
    });

    it("shows everything again once nothing is hidden", () => {
        expect(hiddenFromShow(allVisible())).toBe("");
    });
});
