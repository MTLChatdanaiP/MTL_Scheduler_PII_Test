import { buildPath } from "./router";

// RFC-010 §22: "an operator should be able to drill from a changing aggregate into the affected resource without losing investigation
// context." Each Overview aggregate links to the page that lists what it counts, with the filter that matches the aggregate where the
// page supports one (the alerts page takes ?status=OPEN; the others list everything of their kind).
//
// Built with the router's own buildPath so a link and a typed address bar always agree, and returned as a "#/..." href because the
// router is a hash router.
export type OverviewSection = "runs" | "queues" | "workers" | "schedules" | "alerts" | "pii" | "monitoring" | "components";

const FILTERS: Partial<Record<OverviewSection, Record<string, string>>> = {
    alerts: { status: "OPEN" },
};

export function drillHref(section: OverviewSection): string {
    return "#" + buildPath(section, FILTERS[section] ?? {});
}
