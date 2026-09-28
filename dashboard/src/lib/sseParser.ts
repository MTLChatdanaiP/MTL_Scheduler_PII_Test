export interface SSEMessage {
    id?: string;
    event: string;
    data: string;
}

// Incremental parser for the text/event-stream wire format (WHATWG SSE spec).
// feed() accepts decoded text in chunks of ANY size -- a chunk may end
// mid-line or mid-event -- and returns every message completed so far.
// Line endings: "\n" and "\r\n" are supported. A bare "\r" (legal per spec)
// is never emitted by this backend and is not handled.
export function createSSEParser() {
    let buffer = "";
    let dataLines: string[] = [];
    let eventName = "";
    let lastId: string | undefined;

    function feed(chunk: string): SSEMessage[] {
        buffer += chunk;
        const lines = buffer.split(/\r?\n/);
        buffer = lines.pop() ?? ""; // trailing partial line waits for more input

        const out: SSEMessage[] = [];

        for (const line of lines) {
            if (line === "") {
                // blank line = dispatch. No data lines -> nothing to dispatch.
                if (dataLines.length > 0) {
                    out.push({ id: lastId, event: eventName || "message", data: dataLines.join("\n") });
                }
                dataLines = [];
                eventName = "";
                continue;
            }

            if (line.startsWith(":")) continue; // comment / keepalive ping

            const idx = line.indexOf(":");
            const field = idx === -1 ? line : line.slice(0, idx);
            let value = idx === -1 ? "" : line.slice(idx + 1);
            if (value.startsWith(" ")) value = value.slice(1);

            if (field === "data") dataLines.push(value);
            else if (field === "event") eventName = value;
            else if (field === "id" && !value.includes("\0")) lastId = value;
        }

        return out;
    }

    return { feed };
}