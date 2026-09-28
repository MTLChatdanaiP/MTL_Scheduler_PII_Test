import { describe, it, expect } from "vitest";
import { createSSEParser } from "../src/lib/SSEParser";

const wire =
    ": connected\n\nid: 7\nevent: alert.opened\ndata: {\"id\":7}\n\n: ping\n\nid: 8\nevent: task.created\ndata: {\"id\":8}\n\n";

describe("createSSEParser", () => {
    it("parses a whole stream at once and ignores comments/pings", () => {
        const msgs = createSSEParser().feed(wire);
        expect(msgs).toHaveLength(2);
        expect(msgs[0]).toEqual({ id: "7", event: "alert.opened", data: '{"id":7}' });
    });

    it("gives the same result when fed one character at a time", () => {
        const p = createSSEParser();
        const msgs = [];
        for (const ch of wire) msgs.push(...p.feed(ch));
        expect(msgs).toHaveLength(2);
        expect(msgs[1].id).toBe("8");
    });

    it("handles CRLF and multi-line data", () => {
        const msgs = createSSEParser().feed("event: x\r\ndata: a\r\ndata: b\r\n\r\n");
        expect(msgs).toEqual([{ id: undefined, event: "x", data: "a\nb" }]);
    });

    it("does not dispatch an event that has no data lines", () => {
        expect(createSSEParser().feed("event: x\n\n")).toHaveLength(0);
    });

    it("parses this backend's resync frame", () => {
        const msgs = createSSEParser().feed("event: resync\ndata: {}\n\n");
        expect(msgs[0].event).toBe("resync");
    });
});