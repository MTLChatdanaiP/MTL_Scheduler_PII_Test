import { describe, it, expect } from "vitest";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, relative } from "node:path";

const SRC = join(__dirname, "..", "src");

function walk(dir: string): string[] {
    return readdirSync(dir).flatMap((n) => {
        const p = join(dir, n);
        return statSync(p).isDirectory() ? walk(p) : [p];
    });
}
const files = walk(SRC).filter((f) => /\.(svelte|ts)$/.test(f));
const rel = (f: string) => relative(SRC, f).replace(/\\/g, "/");

describe("Batch 7 lint: one way to refresh (RFC-009 §17)", () => {
    // Local clocks and the shared helper only.
    const ALLOWED = new Set([
        "lib/refresh.ts",
        "lib/liveRefreshTrigger.ts",
        "lib/GlobalBar.svelte",
        "lib/FrontendHealthCard.svelte",
        "lib/ActivityFeed.svelte",
    ]);
    it("no component calls setInterval outside the allowed list", () => {
        const offenders = files.filter((f) => /\bsetInterval\s*\(/.test(readFileSync(f, "utf8")) && !ALLOWED.has(rel(f)));
        expect(offenders.map(rel)).toEqual([]);
    });
});

describe("Batch 7 lint: thresholds live in one place (RFC-010 §22)", () => {
    it("heartbeat and backlog thresholds are declared only in thresholds.ts", () => {
        const offenders = files.filter((f) => {
            if (rel(f) === "lib/thresholds.ts") return false;
            const t = readFileSync(f, "utf8");
            return /(DEGRADED_HEARTBEAT_SECONDS|OFFLINE_HEARTBEAT_SECONDS|DEGRADED_PENDING_THRESHOLD)\s*=\s*\d/.test(t) || /PendingCount\s*>\s*\d/.test(t);
        });
        expect(offenders.map(rel)).toEqual([]);
    });
});

describe("Batch 7 lint: role=button rows answer Space (RFC-009 §22)", () => {
    it("no row handles Enter alone", () => {
        const offenders = files.filter((f) => /key === "Enter" && toggleExpanded/.test(readFileSync(f, "utf8")));
        expect(offenders.map(rel)).toEqual([]);
    });
});
