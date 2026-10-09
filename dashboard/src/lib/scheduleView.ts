// Pure helpers for showing a schedule's occurrence ledger (RFC-002 §14, RFC-005 §7).

import type { ScheduleOccurrence, ScheduleRunItem } from "./api";

// "How late was run creation / how late did execution actually begin". null means "not known yet", which is not the same as
// "on time", so it renders as a dash.
export function formatLateness(seconds: number | null | undefined): string {
    if (seconds === null || seconds === undefined || Number.isNaN(seconds)) return "—";
    const s = Math.max(0, Math.round(seconds));
    if (s < 60) return `${s}s`;
    if (s < 3600) {
        const m = Math.floor(s / 60);
        const r = s % 60;
        return r === 0 ? `${m}m` : `${m}m ${r}s`;
    }
    const h = Math.floor(s / 3600);
    const m = Math.floor((s % 3600) / 60);
    return m === 0 ? `${h}h` : `${h}h ${m}m`;
}

export type OutcomeClass = "created" | "skipped" | "due" | "unknown";

export function outcomeClass(outcome: string | null | undefined): OutcomeClass {
    switch ((outcome ?? "").toUpperCase()) {
        case "CREATED": return "created";
        case "SKIPPED": return "skipped";
        case "DUE": return "due";
        default: return "unknown";
    }
}

// SKIPPED means the scheduler passed the occurrence over and never created a run for it, which is a different fact from a run that
// failed. DUE means it was announced but no run was recorded: the scheduler may have stopped between those two steps.
export function outcomeLabel(outcome: string | null | undefined): string {
    switch (outcomeClass(outcome)) {
        case "created": return "Run created";
        case "skipped": return "Skipped (no run)";
        case "due": return "Due (no run recorded)";
        default: return "Unknown";
    }
}

export function countOutcomes(occurrences: ScheduleOccurrence[] | null | undefined): { created: number; skipped: number; due: number } {
    const out = { created: 0, skipped: 0, due: 0 };
    for (const o of occurrences ?? []) {
        const c = outcomeClass(o.outcome);
        if (c === "created") out.created++;
        else if (c === "skipped") out.skipped++;
        else if (c === "due") out.due++;
    }
    return out;
}

// The backend's run status is "Completed" (the run cards already use it). This list counted "Succeeded", which no run ever has,
// so its "N ok" figure always read 0.
export function resultDistribution(runs: Pick<ScheduleRunItem, "status">[] | null | undefined): string {
    const list = runs ?? [];
    if (list.length === 0) return "…";
    const ok = list.filter((r) => r.status === "Completed" || r.status === "Succeeded").length;
    const failed = list.filter((r) => r.status === "Failed").length;
    const other = list.length - ok - failed;
    return `${ok} ok / ${failed} failed${other > 0 ? ` / ${other} other` : ""}`;
}

export function skippedLabel(skipped: number | null | undefined): string {
    const n = skipped ?? 0;
    return n === 0 ? "none" : `${n} skipped`;
}
