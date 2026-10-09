// Pure helpers for the server-side health projection (RFC-005 §7): worker health, build revisions, dependencies, throughput.

import type { ComponentHealthItem } from "./api";
import type { ComponentStatus } from "./componentHealth";

export type WorkerStatus = "online" | "degraded" | "offline" | "unknown";

// The backend now derives worker health itself, from the SAME thresholds the monitoring sweep uses, so a worker cannot be
// "online" here and "offline" in an alert. Returns undefined for an older backend that sends no health, so the caller can fall
// back to working it out from the heartbeat age.
export function serverHealthToStatus(health: string | null | undefined): WorkerStatus | undefined {
    switch ((health ?? "").toUpperCase()) {
        case "HEALTHY": return "online";
        case "DEGRADED": return "degraded";
        case "OFFLINE": return "offline";
        case "UNKNOWN": return "unknown";
        default: return undefined;
    }
}

export function healthClass(health: string | null | undefined): "healthy" | "degraded" | "offline" | "unknown" {
    switch ((health ?? "").toUpperCase()) {
        case "HEALTHY": return "healthy";
        case "DEGRADED": return "degraded";
        case "OFFLINE": return "offline";
        default: return "unknown";
    }
}

export function dependencyClass(status: string | null | undefined): "ok" | "unavailable" | "unknown" {
    switch ((status ?? "").toUpperCase()) {
        case "OK": return "ok";
        case "UNAVAILABLE": return "unavailable";
        default: return "unknown";
    }
}

export function revisionLabel(revision: string | null | undefined): string {
    const r = (revision ?? "").trim();
    return r === "" ? "unknown" : r;
}

// The distinct, KNOWN build revisions among instances, sorted. More than one means a mixed fleet (a deploy that did not reach
// everything), which is exactly the kind of thing a version column exists to expose. "unknown" is not a version.
export function mixedRevisions(items: Pick<ComponentHealthItem, "build_revision">[] | null | undefined): string[] {
    const known = new Set<string>();
    for (const i of items ?? []) {
        const r = (i.build_revision ?? "").trim();
        if (r !== "" && r !== "unknown") known.add(r);
    }
    return [...known].sort();
}

export function formatThroughput(perMinute: number | null | undefined): string {
    if (perMinute === null || perMinute === undefined || Number.isNaN(perMinute)) return "—";
    if (perMinute === 0) return "0/min";
    return perMinute < 10 ? `${perMinute.toFixed(1)}/min` : `${Math.round(perMinute)}/min`;
}

// ---------------------------------------------------------------- server-judged components (RFC-010 §8/§9)

export interface ComponentRowLike {
    name: string;
    status: ComponentStatus;
    reason: string;
    evidence: unknown;
}

// The server's vocabulary (HEALTHY | DEGRADED | UNHEALTHY | OFFLINE | UNKNOWN) as the page's status names. Anything else is "unknown":
// a verdict this page does not recognise must never be shown as healthy.
export function statusFromServer(health: string | null | undefined): ComponentStatus {
    switch ((health ?? "").toUpperCase()) {
        case "HEALTHY": return "healthy";
        case "DEGRADED": return "degraded";
        case "UNHEALTHY": return "unhealthy";
        case "OFFLINE": return "offline";
        default: return "unknown";
    }
}

export function derivedToRows(items: ComponentHealthItem[] | null | undefined): ComponentRowLike[] {
    return (items ?? []).map((c) => ({
        name: c.display_name,
        status: statusFromServer(c.status ?? c.health),
        reason: c.status_reason ?? c.reason,
        evidence: {
            ...c.evidence,
            component_kind: c.component_kind,
            ...(c.observed_lag_ms !== undefined ? { observed_lag_ms: c.observed_lag_ms } : {}),
            ...(c.threshold_ms !== undefined ? { threshold_ms: c.threshold_ms } : {}),
        },
    }));
}

// The order the Components page lists them in. Scheduler and Workers stay client-aggregated (they roll up many instances); the rest
// come from the server when it sends them.
export const COMPONENT_ORDER = [
    "API", "Query API", "Scheduler", "Redis Delivery", "Workers",
    "Monitoring Ingestor", "Monitoring Projector", "PII Scanner", "Alert Evaluator", "Real-Time Gateway",
];

// Prefers the server's row for a name; falls back to the client's. A row neither side knows is appended, never dropped.
// A client row that rolls several server rows into one is dropped once the server supplies all of them.
const SUPERSEDED_BY: Record<string, string[]> = {
    "Monitoring Ingestor / Projector": ["Monitoring Ingestor", "Monitoring Projector"],
};

export function mergeComponentRows(client: ComponentRowLike[], server: ComponentRowLike[]): ComponentRowLike[] {
    const serverNames = new Set(server.map((r) => r.name));
    const byName = new Map<string, ComponentRowLike>();
    for (const r of client) {
        const replacedBy = SUPERSEDED_BY[r.name];
        if (replacedBy && replacedBy.every((n) => serverNames.has(n))) continue;
        byName.set(r.name, r);
    }
    for (const r of server) byName.set(r.name, r);

    const ordered = COMPONENT_ORDER.filter((n) => byName.has(n)).map((n) => byName.get(n)!);
    const extra = [...byName.values()].filter((r) => !COMPONENT_ORDER.includes(r.name));
    return [...ordered, ...extra];
}

// Restarting a worker creates a new instance row each time, so the same logical component appears once per run it ever had. The
// overview wants the CURRENT one: the newest started_at per (type, display name).
export function latestPerName(items: ComponentHealthItem[] | null | undefined): ComponentHealthItem[] {
    const latest = new Map<string, ComponentHealthItem>();
    for (const c of items ?? []) {
        const key = `${c.component_type}|${c.display_name}`;
        const cur = latest.get(key);
        if (!cur || Date.parse(c.started_at) > Date.parse(cur.started_at)) latest.set(key, c);
    }
    return [...latest.values()];
}

export interface HealthSummary {
    healthy: number;
    degraded: number;
    unhealthy: number;
    offline: number;
    unknown: number;
    stopped: number; // switched off on purpose (RFC-010 §9): counted apart from offline, which means unexplained
    worst: { name: string; status: ComponentStatus; reason: string }[];
}

const SEVERITY: Record<string, number> = { offline: 4, unhealthy: 3, degraded: 2, unknown: 1, healthy: 0 };

// Counts the current component set for the overview card. `worst` lists up to three that need attention, most severe first.
export function summarizeHealth(instances: ComponentHealthItem[] | null | undefined, derived: ComponentHealthItem[] | null | undefined): HealthSummary {
    const out: HealthSummary = { healthy: 0, degraded: 0, unhealthy: 0, offline: 0, unknown: 0, stopped: 0, worst: [] };
    const attention: HealthSummary["worst"] = [];

    for (const c of [...latestPerName(instances), ...(derived ?? [])]) {
        if (c.evidence?.stopped === true) {
            out.stopped += 1;
            continue;
        }
        const status = statusFromServer(c.status ?? c.health);
        if (status === "healthy" || status === "degraded" || status === "unhealthy" || status === "offline" || status === "unknown") {
            out[status] += 1;
        }
        if (status !== "healthy") attention.push({ name: c.display_name, status, reason: c.status_reason ?? c.reason });
    }

    attention.sort((a, b) => SEVERITY[b.status] - SEVERITY[a.status]);
    out.worst = attention.slice(0, 3);
    return out;
}
