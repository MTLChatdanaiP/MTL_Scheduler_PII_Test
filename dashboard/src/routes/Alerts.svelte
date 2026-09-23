<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { lastRefreshedAt, autoRefreshEnabled, timeRange, activeFilterCount } from "../lib/stores";
    import {
        getAlerts,
        acknowledgeAlert,
        getWorkerDetail,
        getQueueDetail,
        getScheduleDetail,
        getRunDetail,
        ApiError,
        type Alert,
        type AlertsResponse,
    } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";
    import { currentPath, parsePath, updateParams } from "../lib/router";
    import JsonTree from "../lib/JsonTree.svelte";
    import AlertHealthCards from "../lib/AlertHealthCards.svelte";

    let alerts: AlertsResponse | null = null;
    let error: unknown = null;
    let refreshTimer: ReturnType<typeof setInterval>;
    let ready = false;
    let hasLoadedOnce = false;
    let isFetching = false;

    // --- BASIC FILTERS ---
    let severityFilter = "";
    let statusFilter = "";
    let subjectTypeFilter = "";
    let subjectIdFilter = "";

    // --- ADVANCED FILTERS ---
    let showAdvanced = false;
    let ruleIdFilter = "";
    let ruleVersionFilter = "";
    let openedAfter = "";
    let openedBefore = "";

    // --- expanded row state ---
    let expandedAlertId: string | null = null;
    let subjectDetail: unknown = null;
    let subjectDetailError: string | null = null;
    let loadingSubject = false;

    let unsubscribePath: (() => void) | undefined;

    function syncFiltersFromUrl() {
        const { params } = parsePath($currentPath);
        severityFilter = params.get("severity") ?? "";
        statusFilter = params.get("status") ?? "";
        subjectTypeFilter = params.get("subject_type") ?? "";
        subjectIdFilter = params.get("subject_id") ?? "";
        ruleIdFilter = params.get("rule_id") ?? "";
        ruleVersionFilter = params.get("rule_version") ?? "";
    }

    function getTimeRangeStart(): Date | null {
        const now = new Date();
        switch ($timeRange) {
            case "15m": return new Date(now.getTime() - 15 * 60 * 1000);
            case "1h": return new Date(now.getTime() - 60 * 60 * 1000);
            case "6h": return new Date(now.getTime() - 6 * 60 * 60 * 1000);
            case "24h": return new Date(now.getTime() - 24 * 60 * 60 * 1000);
            case "7d": return new Date(now.getTime() - 7 * 24 * 60 * 60 * 1000);
            default: return null;
        }
    }

    async function loadAlerts() {
        isFetching = true;
        try {
            const params = new URLSearchParams();

            if (openedAfter) {
                params.set("opened_from", new Date(openedAfter).toISOString());
            } else {
                const rangeStart = getTimeRangeStart();
                if (rangeStart) params.set("opened_from", rangeStart.toISOString());
            }
            if (openedBefore) params.set("opened_to", new Date(openedBefore).toISOString());

            if (statusFilter) params.set("status", statusFilter);
            if (severityFilter) params.set("severity", severityFilter);
            if (subjectTypeFilter) params.set("subject_type", subjectTypeFilter);
            if (subjectIdFilter) params.set("subject_id", subjectIdFilter);
            if (ruleIdFilter) params.set("rule_id", ruleIdFilter);
            if (ruleVersionFilter) params.set("rule_version", ruleVersionFilter);

            params.set("limit", "50");

            alerts = await getAlerts(`?${params.toString()}`);

            lastRefreshedAt.set(
                alerts.freshness.last_updated_at
                    ? new Date(alerts.freshness.last_updated_at)
                    : new Date()
            );
            error = null;
        } catch (e) {
            error = e;
        } finally {
            isFetching = false;
            hasLoadedOnce = true;
        }
    }

    onMount(() => {
        // Initial read: URL -> filters, once, before the first fetch.
        syncFiltersFromUrl();

        // Ongoing sync: any FUTURE url change (paste, back/forward) re-reads
        // filters AND immediately refetches using those just-read values.
        // Calling loadAlerts() HERE, right after syncFiltersFromUrl(), is
        // what removes the race — it no longer depends on some unrelated
        // reactive block (like the timeRange watcher) happening to fire at
        // the right moment relative to this subscriber.
        unsubscribePath = currentPath.subscribe(() => {
            if (ready) {
                syncFiltersFromUrl();
                loadAlerts();
            }
        });

        ready = true;
        loadAlerts();

        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) loadAlerts();
        }, 10000);
    });

    onDestroy(() => {
        unsubscribePath?.();
        clearInterval(refreshTimer);
    });

    // timeRange changing (the GlobalBar dropdown, not a URL paste) still
    // needs to trigger a refetch on its own.
    $: if (ready && $timeRange) {
        loadAlerts();
    }

    // WRITE: push filters into the URL whenever they change. Merges via
    // updateParams, so this never touches GlobalBar's own time_range param.
    $: if (ready) {
        updateParams({
            severity: severityFilter,
            status: statusFilter,
            subject_type: subjectTypeFilter,
            subject_id: subjectIdFilter,
            rule_id: ruleIdFilter,
            rule_version: ruleVersionFilter,
        });
    }

    $: activeFilterCount.set(
        [severityFilter, statusFilter, subjectTypeFilter, subjectIdFilter, ruleIdFilter, ruleVersionFilter, openedAfter, openedBefore]
            .filter(Boolean).length
    );

    $: state = deriveState({
        hasLoadedOnce,
        isFetching,
        error,
        isEmpty: (alerts?.alerts.length ?? 0) === 0,
    });

    // --- toggle buttons: reuse statusFilter, mutually exclusive with the dropdown ---
    function toggleNotAcknowledged() {
        statusFilter = statusFilter === "OPEN" ? "" : "OPEN";
    }
    function toggleResolved() {
        statusFilter = statusFilter === "RESOLVED" ? "" : "RESOLVED";
    }

    // --- expand / subject detail ---
    async function loadSubjectDetail(alert: Alert) {
        loadingSubject = true;
        subjectDetail = null;
        subjectDetailError = null;

        try {
            switch (alert.subject_type) {
                case "WORKER":
                    subjectDetail = (await getWorkerDetail(alert.subject_id)).worker;
                    break;
                case "QUEUE":
                    subjectDetail = (await getQueueDetail(alert.subject_id)).current;
                    break;
                case "SCHEDULE":
                    subjectDetail = (await getScheduleDetail(alert.subject_id)).schedule;
                    break;
                case "RUN":
                    subjectDetail = (await getRunDetail(alert.subject_id)).run;
                    break;
                default:
                    subjectDetailError = `No detail view available for ${alert.subject_type} yet`;
            }
        } catch {
            subjectDetailError = `Could not load ${alert.subject_type.toLowerCase()} — it may no longer exist`;
        } finally {
            loadingSubject = false;
        }
    }

    function toggleExpanded(alert: Alert) {
        expandedAlertId = expandedAlertId === alert.alert_id ? null : alert.alert_id;
        if (expandedAlertId) loadSubjectDetail(alert);
    }

    async function handleAcknowledge(alertId: string) {
        try {
            await acknowledgeAlert(alertId);
            await loadAlerts();
        } catch (e) {
            error = e;
        }
    }
</script>

<AlertHealthCards />

<h3>Alerts</h3>

<div class="filter-bar">
    <label>
        Severity:
        <select bind:value={severityFilter} on:change={loadAlerts}>
            <option value="">All</option>
            <option value="INFO">INFO</option>
            <option value="WARNING">WARNING</option>
            <option value="CRITICAL">CRITICAL</option>
        </select>
    </label>

    <label>
        Subject type:
        <select bind:value={subjectTypeFilter} on:change={loadAlerts}>
            <option value="">All</option>
            <option value="RUN">RUN</option>
            <option value="QUEUE">QUEUE</option>
            <option value="WORKER">WORKER</option>
            <option value="SCHEDULE">SCHEDULE</option>
            <option value="PII_FINDING">PII_FINDING</option>
            <option value="PLATFORM">PLATFORM</option>
        </select>
    </label>

    <label>
        Subject ID:
        <input type="text" bind:value={subjectIdFilter} on:change={loadAlerts} placeholder="e.g. Consumer-a" />
    </label>

    <button class:active={statusFilter === "OPEN"} on:click={() => { toggleNotAcknowledged(); loadAlerts(); }}>
        Not Acknowledged
    </button>
    <button class:active={statusFilter === "RESOLVED"} on:click={() => { toggleResolved(); loadAlerts(); }}>
        Resolved
    </button>

    <button on:click={() => showAdvanced = !showAdvanced}>
        {showAdvanced ? "Hide" : "Show"} advanced filters
    </button>
</div>

{#if showAdvanced}
    <div class="filter-bar advanced">
        <label>Opened after:<input type="datetime-local" bind:value={openedAfter} on:change={loadAlerts} /></label>
        <label>Opened before:<input type="datetime-local" bind:value={openedBefore} on:change={loadAlerts} /></label>
        <label>Status:
            <select bind:value={statusFilter} on:change={loadAlerts}>
                <option value="">All</option>
                <option value="OPEN">OPEN</option>
                <option value="ACKNOWLEDGED">ACKNOWLEDGED</option>
                <option value="RESOLVED">RESOLVED</option>
            </select>
        </label>
        <label>Rule ID:<input type="text" bind:value={ruleIdFilter} on:change={loadAlerts} /></label>
        <label>Rule version:<input type="text" bind:value={ruleVersionFilter} on:change={loadAlerts} /></label>
    </div>
{/if}

{#if state === "READY" || state === "REFRESHING"}
    <h3>Alert List (showing {alerts?.alerts.length ?? 0} of {alerts?.page.total ?? 0})</h3>

    <div class="alert-table">
        <div class="alert-row header">
            <span>Severity</span><span>Type</span><span>Status</span><span>Entity Type</span><span>Entity ID</span><span>First Seen</span><span>Last Seen</span>
        </div>

        {#each alerts?.alerts ?? [] as alert (alert.alert_id)}
            <div class="alert-row clickable" on:click={() => toggleExpanded(alert)} role="button" tabindex="0" on:keydown={(e) => e.key === "Enter" && toggleExpanded(alert)}>
                <span class:critical={alert.severity === "CRITICAL"} class:warning={alert.severity === "WARNING"}>{alert.severity}</span>
                <span>{alert.alert_type}</span>
                <span class:open={alert.status === "OPEN"} class:resolved={alert.status === "RESOLVED"}>{alert.status}</span>
                <span>{alert.subject_type}</span>
                <span>{alert.subject_id}</span>
                <span>{new Date(alert.opened_at).toLocaleString()}</span>
                <span>{new Date(alert.resolved_at ?? alert.acknowledged_at ?? alert.opened_at).toLocaleString()}</span>
            </div>

            {#if expandedAlertId === alert.alert_id}
                <div class="alert-detail">
                    <p><strong>Summary:</strong> {alert.summary}</p>
                    <p><strong>Rule:</strong> {alert.rule_id} (v{alert.rule_version})</p>
                    {#if alert.acknowledged_by}
                        <p><strong>Acknowledged by:</strong> {alert.acknowledged_by} at {new Date(alert.acknowledged_at!).toLocaleString()}</p>
                    {/if}

                    {#if alert.status === "OPEN"}
                        <button on:click|stopPropagation={() => handleAcknowledge(alert.alert_id)}>Acknowledge</button>
                    {/if}

                    <p><strong>Evidence:</strong></p>
                    <JsonTree value={alert.evidence} />

                    <p><strong>{alert.subject_type} details ({alert.subject_id}):</strong></p>
                    {#if loadingSubject}
                        <p>Loading...</p>
                    {:else if subjectDetailError}
                        <p class="subject-error">{subjectDetailError}</p>
                    {:else if subjectDetail}
                        <JsonTree value={subjectDetail} />
                    {/if}
                </div>
            {/if}
        {/each}
    </div>
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    .filter-bar { display: flex; gap: 16px; align-items: center; flex-wrap: wrap; padding: 12px 0; }
    .filter-bar.advanced { border-top: 1px dashed #ccc; margin-top: 8px; padding-top: 12px; }
    .filter-bar label { display: flex; flex-direction: column; font-size: 12px; gap: 4px; }
    .filter-bar input, .filter-bar select { padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; }
    .filter-bar button { padding: 6px 12px; border: 1px solid #ccc; border-radius: 4px; background: white; cursor: pointer; align-self: flex-end; }
    .filter-bar button.active { background: #dbeafe; border-color: #1e40af; }

    .alert-table { margin-top: 20px; border: 1px solid #ddd; border-radius: 8px; overflow: hidden; }
    .alert-row { display: grid; grid-template-columns: 1fr 1.5fr 1fr 1fr 1.5fr 1.5fr 1.5fr; gap: 16px; padding: 12px 16px; border-top: 1px solid #ddd; }
    .alert-row.header { font-weight: bold; background: #f5f5f5; border-top: none; }
    .alert-row.clickable { cursor: pointer; }
    .alert-row.clickable:hover { background: #fafafa; }

    .alert-detail { padding: 16px 24px; background: #fafafa; border-top: 1px solid #eee; font-size: 13px; }

    .critical { background: #fee2e2; color: #991b1b; }
    .warning { background: #fef3c7; color: #92400e; }
    .open { background: #dbeafe; color: #1e40af; }
    .resolved { background: #dcfce7; color: #166534; }
</style>