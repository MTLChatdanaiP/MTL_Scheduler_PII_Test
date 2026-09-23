<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "./stores";
    import { getPIIFindings, getRuns } from "./api";
    import { deriveState, errorMessage } from "./dataState";
    import DataStateBanner from "./DataStateBanner.svelte";

    let byType: Record<string, number> = {};
    let byAction: Record<string, number> = {};
    let scanFailureCount = 0;
    let error: unknown = null;
    let refreshTimer: ReturnType<typeof setInterval>;
    let hasLoadedOnce = false;
    let isFetching = false;
    let runsWithFindingsCount = 0;

    async function loadCards() {
        isFetching = true;
        try {
            const res = await getPIIFindings(`?limit=200&offset=0`);

            byType = {};
            byAction = {};
            for (const p of res.piis) {
                byType[p.type] = (byType[p.type] ?? 0) + 1;
                byAction[p.policy_action] = (byAction[p.policy_action] ?? 0) + 1;
            }
            const distinctRuns = new Set(res.piis.map(p => p.run_id));
            runsWithFindingsCount = distinctRuns.size;

            // scan failures ARE derivable -- this lives on Task, not on the
            // PII finding itself, so it's a separate call.
            const scanRes = await getRuns(`?pii_scan_status=SCAN_ERROR&limit=1&offset=0`);
            scanFailureCount = scanRes.page.total ?? 0;

            error = null;
        } catch (e) {
            error = e;
        } finally {
            isFetching = false;
            hasLoadedOnce = true;
        }
    }

    onMount(() => {
        loadCards();
        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) loadCards();
        }, 10000);
    });

    onDestroy(() => clearInterval(refreshTimer));

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: false });
</script>

<h3>PII</h3>

{#if state === "READY" || state === "REFRESHING"}
    <div class="cards">
         <div class="card">
            <span>Runs With Findings</span>
            <strong>{runsWithFindingsCount}</strong>
            <small>within the current page of findings fetched -- same
                fetched-page limitation as every other client-side count on
                this dashboard, not a full-dataset count</small>
        </div>

        <div class="card">
            <span>Findings by Type</span>
            <div class="breakdown">
                {#each Object.entries(byType) as [type, count]}
                    <span class="pill">{type}: {count}</span>
                {/each}
                {#if Object.keys(byType).length === 0}<span>None</span>{/if}
            </div>
        </div>

        <div class="card">
            <span>Policy Actions</span>
            <div class="breakdown">
                {#each Object.entries(byAction) as [action, count]}
                    <span class="pill">{action}: {count}</span>
                {/each}
                {#if Object.keys(byAction).length === 0}<span>None</span>{/if}
            </div>
        </div>

        <div class="card">
            <span>Scan Failures / Incomplete Scans</span>
            <strong>{scanFailureCount}</strong>
        </div>
    </div>
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    .cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; }
    .card { padding: 16px; border: 1px solid #ddd; border-radius: 8px; }
    .card span { display: block; margin-bottom: 8px; font-size: 13px; color: #555; }
    .card strong { font-size: 28px; display: block; }
    .card small { color: #888; font-size: 11px; }
    .gap-card { background: #fafafa; }
    .breakdown { display: flex; flex-direction: column; gap: 4px; }
    .pill {
        display: inline-block; padding: 4px 8px; border-radius: 999px;
        font-size: 12px; font-weight: bold; background: #eee; width: fit-content;
    }
</style>