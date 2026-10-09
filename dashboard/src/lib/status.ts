// A run's status, and how to draw it.
//
// RunList drew its badge from the run PROJECTION's current_status. The backend's projection used to blank that field on any
// event it did not recognise, so a failed run's badge TEXT fell back to Task.Status (correct) while its STYLING, which
// keyed off current_status, was never applied: a failed run was never red. Task.Status is the fact the backend itself
// filters and counts on, so it is what the badge shows; the projection's copy is only compared against it.

export const RUN_STATUSES = ["Pending", "Queued", "Running", "Completed", "Failed", "Blocked"] as const;

export type StatusClass = "pending" | "queued" | "running" | "completed" | "failed" | "blocked" | "unknown";

export interface StatusSource {
    Status?: string | null;
    current_status?: string | null;
}

function clean(s: string | null | undefined): string {
    return (s ?? "").trim();
}

// The status to show: the task's own (the authoritative fact), falling back to the projection's only if the task has none.
export function statusOf(run: StatusSource): string {
    return clean(run.Status) || clean(run.current_status);
}

export function statusClass(status: string): StatusClass {
    switch (clean(status).toLowerCase()) {
        case "pending": return "pending";
        case "queued": return "queued";
        case "running": return "running";
        case "completed": return "completed";
        case "failed": return "failed";
        case "blocked": return "blocked";
        default: return "unknown";
    }
}

export function statusLabel(status: string): string {
    return clean(status) || "Unknown";
}

// True only when BOTH are present and DIFFER: the projection is out of step with the task (typically rows written before the
// projection was fixed and rebuilt). A blank projection is not a disagreement, it is just not populated.
export function statusDisagrees(run: StatusSource): boolean {
    const task = clean(run.Status);
    const projected = clean(run.current_status);
    return task !== "" && projected !== "" && task !== projected;
}
