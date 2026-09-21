import { writable, get } from "svelte/store";


// A hash router, not a real path router — "/alerts?severity=CRITICAL" lives
// after the "#" in the URL (yoursite.com/#/alerts?severity=CRITICAL). This
// needs zero server-side config, since the server never sees anything after
// the "#" at all — the whole route lives in the browser. That's the entire
// reason this is the simple option: no backend routing changes required.

function getHash(): string {
    return window.location.hash.slice(1) || "/overview";
}

export const currentPath = writable(getHash());

if (typeof window !== "undefined") {
    window.addEventListener("hashchange", () => {
        currentPath.set(getHash());
    });
}

// Changes the URL. This is the ONLY way any page should change what's
// showing — never set a page's own "activePage"-style variable directly,
// or the URL and the visible screen will drift out of sync.
export function navigate(path: string) {
    if (window.location.hash.slice(1) !== path) {
        window.location.hash = path;
    }
}

// "/alerts?severity=CRITICAL&status=OPEN" -> { page: "alerts", params }
export function parsePath(path: string): { page: string; params: URLSearchParams } {
    const [pagePart, queryPart] = path.split("?");
    const page = pagePart.replace(/^\//, "") || "overview";
    return { page, params: new URLSearchParams(queryPart ?? "") };
}

// Builds a path string from a page name + a params object, skipping empty
// values so the URL doesn't fill up with "?severity=&status=". This is what
// a page calls whenever ITS OWN filters change, to push them into the URL.
export function buildPath(page: string, params: Record<string, string>): string {
    const search = new URLSearchParams();
    for (const [key, value] of Object.entries(params)) {
        if (value) search.set(key, value);
    }
    const query = search.toString();
    return `/${page}${query ? `?${query}` : ""}`;
}

export function updateParams(updates: Record<string, string>) {
    const { page, params } = parsePath(get(currentPath));

    for (const [key, value] of Object.entries(updates)) {
        if (value) {
            params.set(key, value);
        } else {
            params.delete(key);
        }
    }

    const query = params.toString();
    const newPath = `/${page}${query ? `?${query}` : ""}`;

    if (newPath === get(currentPath)) return;

    navigate(newPath);
}

export function getParam(key: string): string {
    return parsePath(get(currentPath)).params.get(key) ?? "";
}