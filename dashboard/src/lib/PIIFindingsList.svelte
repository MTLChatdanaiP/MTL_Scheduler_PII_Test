<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getPIIFindings, type PIIFindingItem, type PIIListResponse } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";
    import { currentPath, parsePath, updateParams } from "../lib/router";

    let findings: PIIFindingItem[] = [];
    let page: PIIListResponse["page"] | null = null;
    let error: unknown = null;
    let refreshTimer: ReturnType<typeof setInterval>;
    let hasLoadedOnce = false;
    let isFetching = false;

    let piiTypeFilter = "";
    let sourceFilter = "";
    let policyActionFilter = "";
    let runIdFilter = "";
    let detectorIdFilter = "";

    let ready = false;
    let unsubscribePath: (() => void) | undefined;
    
    function syncFiltersFromUrl() {
        const { params } = parsePath($currentPath);
        piiTypeFilter = params.get("pii_type") ?? "";
        sourceFilter = params.get("source") ?? "";
        policyActionFilter = params.get("policy_action") ?? "";
        runIdFilter = params.get("run_id") ?? "";
        detectorIdFilter = params.get("detector_id") ?? "";
    }
    
    function commitFilters() {
        updateParams({
            pii_type: piiTypeFilter,
            source: sourceFilter,
            policy_action: policyActionFilter,
            run_id: runIdFilter,
            detector_id: detectorIdFilter,
        });
    }

    async function loadFindings() {
        isFetching = true;
        commitFilters();
        try {
            const params = new URLSearchParams();
            if (piiTypeFilter) params.set("pii_type", piiTypeFilter);
            if (sourceFilter) params.set("source", sourceFilter);
            if (policyActionFilter) params.set("policy_action", policyActionFilter);
            if (runIdFilter) params.set("run_id", runIdFilter);
            if (detectorIdFilter) params.set("detector_id", detectorIdFilter);
            params.set("limit", "50");
            params.set("offset", "0");

            const res = await getPIIFindings(`?${params.toString()}`);
            findings = res.piis;
            page = res.page;
            error = null;
        } catch (e) {
            error = e;
        } finally {
            isFetching = false;
            hasLoadedOnce = true;
        }
    }

    onMount(() => {
        syncFiltersFromUrl();
    
        unsubscribePath = currentPath.subscribe(() => {
            if (ready) {
                syncFiltersFromUrl();
                loadFindings(); 
            }
        });
    
        ready = true;
        loadFindings();
    
        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) loadFindings();
        }, 10000);
    });

   onDestroy(() => {
        unsubscribePath?.();
        clearInterval(refreshTimer);
    });

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: findings.length === 0 });
</script>

<div class="filter-bar">
    <label>PII type:<input type="text" bind:value={piiTypeFilter} on:change={loadFindings} placeholder="e.g. SSN" /></label>
    <label>Source:<input type="text" bind:value={sourceFilter} on:change={loadFindings} placeholder="e.g. JOB_PAYLOAD" /></label>
    <label>
        Policy action:
        <select bind:value={policyActionFilter} on:change={loadFindings}>
            <option value="">All</option>
            <option value="REDACT">REDACT</option>
            <option value="MASK">MASK</option>
            <option value="OBSERVE">OBSERVE</option>
        </select>
    </label>
    <label>Run ID:<input type="text" bind:value={runIdFilter} on:change={loadFindings} placeholder="exact job id" /></label>
    <label>Detector:<input type="text" bind:value={detectorIdFilter} on:change={loadFindings} placeholder="e.g. builtin-ssn" /></label>
    <button on:click={loadFindings}>Refresh</button>
</div>

{#if state === "READY" || state === "REFRESHING"}
    <p class="count-line">{findings.length} of {page?.total ?? findings.length} findings</p>

    <div class="pii-table">
        <div class="pii-row header">
            <span>Type</span>
            <span>Source</span>
            <span>Field Path</span>
            <span>Confidence</span>
            <span>Policy Action</span>
            <span>Run</span>
            <span>Attempt</span>
            <span>Detector / Version</span>
            <span>Policy Name / Version</span>
            <span>Rule ID</span>
            <span>Mask Strategy</span>
            <span>Time</span>
        </div>

         {#each findings as f, i (i)}
            <div class="pii-row">
                <span>{f.type}</span>
                <span>{f.source}</span>
                <span>{f.field_path || "—"}</span>
                <span>{(f.confidence * 100).toFixed(0)}%</span>
                <span class="badge" class:redact={f.policy_action === "REDACT"} class:mask={f.policy_action === "MASK"}>{f.policy_action}</span>
                <span class="run-id">{f.run_id.slice(0, 8)}…</span>
                <span class="gap" title="not available -- always empty for pre-execution scans, no Attempt exists yet at detection time">—</span>
                <span>{f.detector_id} <span class="gap" title="detectors are not versioned in this system yet">(v?)</span></span>
                <span>{f.policy_name} v{f.policy_version}</span>
                <span>{f.rule_id}</span>
                <span>{f.mask_strategy || "n/a"}</span>
                <span>{new Date(f.detected_at).toLocaleString()}</span>
            </div>
        {/each}
    </div>

    <p class="gap-summary">
        "Attempt" is always empty -- these findings come from pre-execution
        scanning, before any Attempt exists. "Detector version" is empty
        because detectors are not versioned anywhere in the policy schema yet.
        Both are real, known gaps, not hidden by the frontend.
    </p>
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    .filter-bar { display: flex; gap: 16px; align-items: flex-end; flex-wrap: wrap; padding: 12px 0; }
    .filter-bar label { display: flex; flex-direction: column; font-size: 12px; gap: 4px; }
    .filter-bar input, .filter-bar select { padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; }
    .filter-bar button { padding: 6px 12px; border: 1px solid #ccc; border-radius: 4px; background: white; cursor: pointer; }

    .count-line { font-size: 12px; color: #888; margin: 8px 0 0; }

    .pii-table { margin-top: 8px; border: 1px solid #ddd; border-radius: 8px; overflow-x: auto; }
    .pii-row {
        display: grid;
        grid-template-columns: 0.8fr 1fr 1fr 0.8fr 1fr 0.8fr 0.8fr 1.2fr 1.2fr 0.8fr 1fr 1fr;
        gap: 12px;
        padding: 10px 16px;
        border-top: 1px solid #ddd;
        font-size: 13px;
        align-items: center;
    }
    .pii-row.header { font-weight: bold; background: #f5f5f5; border-top: none; }
    .gap { color: #ccc; cursor: help; }

    .badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: bold; background: #eee; width: fit-content; }
    .badge.redact { background: #fee2e2; color: #991b1b; }
    .badge.mask { background: #fef3c7; color: #92400e; }

    .gap-summary { margin-top: 16px; padding: 12px; background: #fafafa; border-radius: 4px; font-size: 12px; color: #888; }

    .run-id { font-family: monospace; font-size: 12px; }
</style>