<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getAlerts, type Alert } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";

    // Same cards logic as Alerts.svelte's old §8.5 section, extracted into
    // its own self-contained fetcher matching every other XHealthCards.svelte
    // -- NOT sharing state with Alerts.svelte, deliberately. This fetches its
    // own copy independently, same as how RunHealthCards and RunList each
    // fetch independently rather than sharing one loaded array. Slightly
    // more network traffic if both are ever on screen together, but zero
    // coupling risk to the filter/URL-sync logic Alerts.svelte already has
    // working and tested.

    let alerts: Alert[] = [];
    let error: unknown = null;
    let refreshTimer: ReturnType<typeof setInterval>;
    let hasLoadedOnce = false;
    let isFetching = false;

    async function load() {
        isFetching = true;
        try {
            const res = await getAlerts(`?limit=50&offset=0`);
            alerts = res.alerts;
            error = null;
        } catch (e) {
            error = e;
        } finally {
            isFetching = false;
            hasLoadedOnce = true;
        }
    }

    onMount(() => {
        load();
        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) load();
        }, 10000);
    });

    onDestroy(() => clearInterval(refreshTimer));

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: false });

    $: activeAlerts = alerts.filter(a => a.status !== "RESOLVED");
    $: activeBySeverity = activeAlerts.reduce<Record<string, number>>((acc, a) => {
        acc[a.severity] = (acc[a.severity] ?? 0) + 1;
        return acc;
    }, {});
    $: newAlertsCount = alerts.filter(a => Date.now() - new Date(a.opened_at).getTime() < 15 * 60 * 1000).length;
    $: acknowledgedCount = alerts.filter(a => a.status === "ACKNOWLEDGED").length;
    $: highestImpactGroups = Object.entries(
        activeAlerts.reduce<Record<string, number>>((acc, a) => {
            const key = `${a.subject_type}:${a.subject_id}`;
            acc[key] = (acc[key] ?? 0) + 1;
            return acc;
        }, {})
    ).sort((a, b) => b[1] - a[1]).slice(0, 3);
</script>

<h3>Alerts</h3>

{#if state === "READY" || state === "REFRESHING"}
    <div class="cards">
        <div class="card">
            <span>Active Alerts by Severity</span>
            <div class="severity-breakdown">
                {#each Object.entries(activeBySeverity) as [sev, count]}
                    <span class="pill" class:critical={sev === "CRITICAL"} class:warning={sev === "WARNING"}>{sev}: {count}</span>
                {/each}
                {#if Object.keys(activeBySeverity).length === 0}<span class="pill">None active</span>{/if}
            </div>
        </div>
        <div class="card"><span>New Alerts (last 15m)</span><strong>{newAlertsCount}</strong></div>
        <div class="card"><span>Acknowledged Alerts</span><strong>{acknowledgedCount}</strong></div>
        <div class="card">
            <span>Highest-Impact Subjects</span>
            <div class="impact-list">
                {#each highestImpactGroups as [key, count]}
                    <div class="impact-row"><span>{key.replace(":", " ")}</span><span>{count} active</span></div>
                {/each}
                {#if highestImpactGroups.length === 0}<span>None</span>{/if}
            </div>
        </div>
    </div>
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    .cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; }
    .card { padding: 16px; border: 1px solid #ddd; border-radius: 8px; }
    .card span { display: block; margin-bottom: 8px; font-size: 13px; color: #555; }
    .card strong { font-size: 28px; }
    .severity-breakdown { display: flex; flex-direction: column; gap: 4px; }
    .impact-list { display: flex; flex-direction: column; gap: 4px; font-size: 13px; }
    .impact-row { display: flex; justify-content: space-between; }
    .pill { display: inline-block; padding: 4px 8px; border-radius: 999px; font-size: 12px; font-weight: bold; background: #eee; width: fit-content; }
    .critical { background: #fee2e2; color: #991b1b; }
    .warning { background: #fef3c7; color: #92400e; }
</style>