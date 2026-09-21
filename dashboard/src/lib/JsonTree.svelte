<script lang="ts">
    export let value: unknown;
    export let label: string | null = null;

    let expanded = false;

    function isPlainObject(v: unknown): v is Record<string, unknown> {
        return typeof v === "object" && v !== null && !Array.isArray(v);
    }

    // Pure GORM noise on every nested object — never useful to an operator.
    const HIDDEN_KEYS = new Set(["ID", "DeletedAt", "UpdatedAt"]);

    function formatPrimitive(v: unknown): string {
        if (v === null || v === undefined) return "—";
        if (typeof v === "string" && /^\d{4}-\d{2}-\d{2}T/.test(v)) {
            const d = new Date(v);
            return isNaN(d.getTime()) ? v : d.toLocaleString();
        }
        if (typeof v === "boolean") return v ? "true" : "false";
        return String(v);
    }

    // evidence is a STRING containing JSON text, not an actual object —
    // parse it so it renders as expandable fields instead of one raw line.
    // Falls back to the raw string if it doesn't parse.
    function tryParseJSON(s: string): unknown {
        try {
            return JSON.parse(s);
        } catch {
            return s;
        }
    }
</script>

{#if Array.isArray(value)}
    <div class="tree-array">
        <button class="tree-toggle" on:click={() => expanded = !expanded}>
            {expanded ? "▾" : "▸"} {label ?? "items"} ({value.length})
        </button>

        {#if expanded}
            <div class="tree-array-body">
                {#if value.length === 0}
                    <div class="tree-empty">none</div>
                {:else}
                    {#each value as item, i}
                        <div class="tree-array-item">
                            <svelte:self value={item} label={`#${i + 1}`} />
                        </div>
                    {/each}
                {/if}
            </div>
        {/if}
    </div>

{:else if isPlainObject(value)}
    <div class="tree-object">
        {#if label}<div class="tree-object-label">{label}</div>{/if}
        {#each Object.entries(value) as [key, v]}
            {#if !HIDDEN_KEYS.has(key)}
                <div class="tree-row">
                    <span class="tree-key">{key}</span>

                    {#if key === "evidence" && typeof v === "string"}
                        <svelte:self value={tryParseJSON(v)} label={null} />
                    {:else if Array.isArray(v) || isPlainObject(v)}
                        <svelte:self value={v} label={null} />
                    {:else}
                        <span class="tree-value">{formatPrimitive(v)}</span>
                    {/if}
                </div>
            {/if}
        {/each}
    </div>

{:else}
    <span class="tree-value">{formatPrimitive(value)}</span>
{/if}

<style>
    .tree-object {
        display: flex;
        flex-direction: column;
        gap: 2px;
    }
    .tree-object-label {
        font-weight: bold;
        font-size: 12px;
        color: #555;
        margin-top: 4px;
    }
    .tree-row {
        display: flex;
        gap: 8px;
        font-size: 13px;
        padding: 2px 0;
    }
    .tree-key {
        color: #555;
        min-width: 140px;
        flex-shrink: 0;
    }
    .tree-value {
        color: #111;
    }

    .tree-array {
        margin: 4px 0;
    }
    .tree-toggle {
        background: none;
        border: none;
        cursor: pointer;
        font-size: 13px;
        font-weight: bold;
        color: #1e40af;
        padding: 2px 0;
    }
    .tree-array-body {
        margin-left: 16px;
        padding-left: 12px;
        border-left: 2px solid #eee;
    }
    .tree-array-item {
        padding: 6px 0;
        border-bottom: 1px solid #f0f0f0;
    }
    .tree-array-item:last-child {
        border-bottom: none;
    }
    .tree-empty {
        font-size: 12px;
        color: #999;
        font-style: italic;
    }
</style>