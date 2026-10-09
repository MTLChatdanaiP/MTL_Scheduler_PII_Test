// Pure helpers for the PII policy card (RFC-006 §32 pii.policy_drift_detected).

import type { PolicyDrift } from "./api";

export function shortChecksum(checksum: string | null | undefined): string {
    const c = (checksum ?? "").trim();
    return c === "" ? "—" : c.slice(0, 8);
}

// What to tell the operator, or null when there is nothing to say. An unreadable file is reported, but it is not "drift": nothing
// can be said about content that could not be read.
export function driftMessage(drift: PolicyDrift | null | undefined): { kind: "drift" | "unreadable"; text: string } | null {
    if (!drift) return null;
    if (drift.drifted) {
        return {
            kind: "drift",
            text: `The policy file no longer matches the active policy (file ${shortChecksum(drift.file_checksum)}, active ${shortChecksum(drift.active_checksum)}). The engine is still enforcing the active one. Reload the policy to apply the file, or restore the file.`,
        };
    }
    if (drift.file_error) {
        return { kind: "unreadable", text: `Cannot compare the policy file with the active policy: ${drift.file_error}.` };
    }
    return null;
}
