import { describe, it, expect } from "vitest";
import { driftMessage, shortChecksum } from "../src/lib/policyView";

describe("shortChecksum", () => {
    it("shows the first eight characters, or a dash", () => {
        expect(shortChecksum("0123456789abcdef")).toBe("01234567");
        expect(shortChecksum("")).toBe("—");
        expect(shortChecksum(undefined)).toBe("—");
    });
});

describe("driftMessage", () => {
    it("says nothing when the file matches", () => {
        expect(driftMessage({ drifted: false, active_checksum: "aaaaaaaa11", file_checksum: "aaaaaaaa11" })).toBeNull();
        expect(driftMessage(null)).toBeNull();
        expect(driftMessage(undefined)).toBeNull();
    });

    it("explains a real drift, with both checksums and what it means", () => {
        const m = driftMessage({ drifted: true, active_checksum: "aaaaaaaa11", file_checksum: "bbbbbbbb22" });
        expect(m?.kind).toBe("drift");
        expect(m?.text).toContain("bbbbbbbb");
        expect(m?.text).toContain("aaaaaaaa");
        expect(m?.text).toContain("still enforcing the active one");
    });

    it("reports an unreadable file separately, and never as drift", () => {
        const m = driftMessage({ drifted: false, active_checksum: "a", file_checksum: "", file_error: "policy file could not be read" });
        expect(m?.kind).toBe("unreadable");
        expect(m?.text).toContain("could not be read");
    });
});
