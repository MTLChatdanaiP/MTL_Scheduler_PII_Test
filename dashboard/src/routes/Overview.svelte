<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { lastRefreshedAt, autoRefreshEnabled, timeRange, activeFilterCount } from "../lib/stores";
    import { getAlerts, type AlertsResponse } from "../lib/api";

    let alerts: AlertsResponse | null = null;
    let error: string | null = null;
    let refreshTimer: ReturnType<typeof setInterval>;
    let ready = false;   // guards against double-fetching on first load

    let statusFilter = "";
    let severityFilter = "";

    function getTimeRangeStart(): Date | null {
        const now = new Date();
        switch ($timeRange) {
            case "15m": return new Date(now.getTime() - 15 * 60 * 1000);
            case "1h": return new Date(now.getTime() - 60 * 60 * 1000);
            case "6h": return new Date(now.getTime() - 6 * 60 * 60 * 1000);
            case "24h": return new Date(now.getTime() - 24 * 60 * 60 * 1000);
            case "7d": return new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
            case "custom": return null;
            default: return null;
        }
    }

    async function loadAlerts() {
        try {
            const params = new URLSearchParams();
            const rangeStart = getTimeRangeStart();

            if (rangeStart) params.set("opened_from", rangeStart.toISOString());
            if (statusFilter) params.set("status", statusFilter);
            if (severityFilter) params.set("severity", severityFilter);
            params.set("limit", "10");

            alerts = await getAlerts(params.toString() ? `?${params.toString()}` : "?limit=10");

            lastRefreshedAt.set(
                alerts.freshness.last_updated_at
                    ? new Date(alerts.freshness.last_updated_at)
                    : new Date()
            );
            error = null;
        } catch (e) {
            error = (e as Error).message;
        }
    }

    onMount(() => {
        ready = true;
        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) loadAlerts();
        }, 10000);
    });

    onDestroy(() => clearInterval(refreshTimer));

    // fires once on mount (when `ready` flips true), then again every time
    // $timeRange changes from the GlobalBar's dropdown
    $: if (ready && $timeRange) {
        loadAlerts();
    }

    // page-local filters report their count up to the global bar
    $: activeFilterCount.set([statusFilter, severityFilter].filter(Boolean).length);

    $: totalAlerts = alerts?.page.total ?? 0;
    $: openAlerts = alerts?.alerts.filter(a => a.status === "OPEN").length ?? 0;
    $: criticalAlerts = alerts?.alerts.filter(a => a.severity === "CRITICAL").length ?? 0;
</script>

<h3>System Overview</h3>

{#if error}
    <p>Error: {error}</p>
{:else if alerts}
    <div class="cards">
        <div class="card"><span>Total Alerts</span><strong>{totalAlerts}</strong></div>
        <div class="card"><span>Open Alerts</span><strong>{openAlerts}</strong></div>
        <div class="card"><span>Critical Alerts</span><strong>{criticalAlerts}</strong></div>
    </div>

    <h3>Recent Alerts (showing {alerts.alerts.length} of {alerts.page.total})</h3>

    <div class="alert-table">
        <div class="alert-row header">
            <span>Type</span><span>Severity</span><span>Status</span><span>Subject</span><span>Opened</span>
        </div>
        {#each alerts.alerts as alert}
            <div class="alert-row">
                <span>{alert.alert_type}</span>
                <span class:critical={alert.severity === "CRITICAL"} class:warning={alert.severity === "WARNING"}>
                    {alert.severity}
                </span>
                <span class:open={alert.status === "OPEN"} class:resolved={alert.status === "RESOLVED"}>
                    {alert.status}
                </span>
                <span>{alert.subject_id}</span>
                <span>{new Date(alert.opened_at).toLocaleTimeString()}</span>
            </div>
        {/each}
    </div>
{:else}
    <p>Loading...</p>
{/if}

<details>
    <summary>Debug JSON</summary>
    <pre>{JSON.stringify(alerts, null, 2)}</pre>
</details>

<style>
    /* unchanged from before, minus the .controls block — delete that rule */
    .cards { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; }
    .card { padding: 20px; border: 1px solid #ddd; border-radius: 8px; }
    .card span { display: block; margin-bottom: 8px; }
    .card strong { font-size: 28px; }
    .alert-table { margin-top: 20px; border: 1px solid #ddd; border-radius: 8px; overflow: hidden; }
    .alert-row { display: grid; grid-template-columns: 2fr 1fr 1fr 1.5fr 1fr; gap: 16px; padding: 12px 16px; border-top: 1px solid #ddd; }
    .alert-row.header { font-weight: bold; background: #f5f5f5; border-top: none; }
    .alert-row span { display: flex; align-items: center; }
    .alert-row span.critical, .alert-row span.warning, .alert-row span.open, .alert-row span.resolved {
        width: fit-content; padding: 4px 8px; border-radius: 999px; font-size: 12px; font-weight: bold;
    }
    .critical { background: #fee2e2; color: #991b1b; }
    .warning { background: #fef3c7; color: #92400e; }
    .open { background: #dbeafe; color: #1e40af; }
    .resolved { background: #dcfce7; color: #166534; }
</style>