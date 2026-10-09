// RFC-007 §4 Alert Types. The Alerts page had no type filter, so an operator could not ask "show me only PII_SCAN_FAILED". All 17
// can now be produced (Batches 1-3 gave the last five a data source), so they can all be filtered on.

export const ALERT_TYPES = [
    "MONITORING_GAP",
    "PII_DETECTED",
    "PII_POLICY_VIOLATED",
    "PII_SCAN_FAILED",
    "QUEUE_AGE_HIGH",
    "QUEUE_BACKLOG",
    "QUEUE_NO_CONSUMER",
    "RUN_DUPLICATE_SUSPECTED",
    "RUN_FAILED",
    "RUN_LOST",
    "RUN_RETRY_EXHAUSTED",
    "RUN_STUCK",
    "RUN_TIMEOUT",
    "SCHEDULE_DELAYED",
    "SCHEDULE_MISSED",
    "WORKER_CAPACITY_HIGH",
    "WORKER_OFFLINE",
] as const;

export type AlertType = (typeof ALERT_TYPES)[number];

// "RUN_RETRY_EXHAUSTED" -> "Run retry exhausted"
export function alertTypeLabel(type: string): string {
    const words = type.toLowerCase().split("_").filter(Boolean);
    if (words.length === 0) return "";
    return [words[0].charAt(0).toUpperCase() + words[0].slice(1), ...words.slice(1)].join(" ");
}

export function isKnownAlertType(type: string): boolean {
    return (ALERT_TYPES as readonly string[]).includes(type);
}
