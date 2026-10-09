<script lang="ts">
    import { currentPath, parsePath, updateParams } from "./router";
    import { parseSort, formatSort } from "./sortView";

    export let options: { key: string; label: string }[];

    $: allowed = options.map((o) => o.key);
    $: current = parseSort(parsePath($currentPath).params.get("sort"), allowed);
    $: field = current?.key ?? "";
    $: desc = current?.desc ?? false;

    function change(nextField: string, nextDesc: boolean) {
        updateParams({ sort: nextField ? formatSort({ key: nextField, desc: nextDesc }) : "" });
    }
</script>

<label>
    Sort by:
    <select value={field} on:change={(e) => change(e.currentTarget.value, desc)}>
        <option value="">Default order</option>
        {#each options as o (o.key)}<option value={o.key}>{o.label}</option>{/each}
    </select>
</label>
<label>
    <input type="checkbox" checked={desc} disabled={!field} on:change={(e) => change(field, e.currentTarget.checked)} />
    Descending
</label>
