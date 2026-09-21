<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled, timeRange, globalSearchQuery, activeFilterCount } from "../lib/stores";
    import { getRuns, getRunDetail, type RunListItem, type RunDetailResponse } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";
    import { currentPath, parsePath, updateParams } from "../lib/router";
    import JsonTree from "../lib/JsonTree.svelte";
    import TimelineView from "../lib/TimelineView.svelte";

    let rows: RunListItem[] = [];
    let error: unknown = null;
    let refreshTimer: ReturnType<typeof setInterval>;
    let ready = false;
    let hasLoadedOnce = false;
    let isFetching = false;
    let loadError: string | null = null;

    let jobTypeFilter = "";
    let executionStateFilter = "";
    let queueFilter = "";

    let showAdvanced = false;
    let workerFilter = "";
    let scheduleIdFilter = "";
    let monitoringAnnotationFilter = "";
    let attemptCountFilter = "";
    let failureCategoryFilter = "";
    let piiTypeFilter = "";
    let piiScanStatusFilter = "";
    let alertTypeFilter = "";
    let alertSeverityFilter = "";
    let durationFromFilter = "";
    let durationToFilter = "";

    let unsubscribePath: (() => void) | undefined;

    let expandedRunId: string | null = null;
    let runDetail: RunDetailResponse | null = null;
    let chainSiblings: RunListItem[] = [];
    let detailLoading = false;
    let detailError: string | null = null;

    let timelineChainId: string | null = null;

    function syncFiltersFromUrl() {
        const { params } = parsePath($currentPath);
        jobTypeFilter = params.get("job_type") ?? "";
        executionStateFilter = params.get("execution_state") ?? "";
        queueFilter = params.get("queue") ?? "";
        workerFilter = params.get("worker_id") ?? "";
        scheduleIdFilter = params.get("schedule_id") ?? "";
        monitoringAnnotationFilter = params.get("monitoring_annotation") ?? "";
        attemptCountFilter = params.get("attempt_count") ?? "";
        failureCategoryFilter = params.get("failure_category") ?? "";
        piiTypeFilter = params.get("pii_type") ?? "";
        piiScanStatusFilter = params.get("pii_scan_status") ?? "";
        alertTypeFilter = params.get("alert_type") ?? "";
        alertSeverityFilter = params.get("alert_severity") ?? "";
        durationFromFilter = params.get("duration_from") ?? "";
        durationToFilter = params.get("duration_to") ?? "";
    }

    function commitFilters() {
        updateParams({
            job_type: jobTypeFilter,
            execution_state: executionStateFilter,
            queue: queueFilter,
            worker_id: workerFilter,
            schedule_id: scheduleIdFilter,
            monitoring_annotation: monitoringAnnotationFilter,
            attempt_count: attemptCountFilter,
            failure_category: failureCategoryFilter,
            pii_type: piiTypeFilter,
            pii_scan_status: piiScanStatusFilter,
            alert_type: alertTypeFilter,
            alert_severity: alertSeverityFilter,
            duration_from: durationFromFilter,
            duration_to: durationToFilter,
        });
    }

    function getTimeRangeStart(): Date | null {
        const now = new Date();
        const ranges: Record<string, number> = {
            "15m": 15 * 60 * 1000, "1h": 60 * 60 * 1000, "6h": 6 * 60 * 60 * 1000,
            "24h": 24 * 60 * 60 * 1000, "7d": 7 * 24 * 60 * 60 * 1000,
        };
        const ms = ranges[$timeRange];
        return ms ? new Date(now.getTime() - ms) : null;
    }

    async function loadRuns() {
        isFetching = true;
        loadError = null;
        commitFilters();

        try {
            const params = new URLSearchParams();
            const rangeStart = getTimeRangeStart();
            if (rangeStart) params.set("created_from", rangeStart.toISOString());

            if (jobTypeFilter) params.set("job_type", jobTypeFilter);
            if (executionStateFilter) params.set("execution_state", executionStateFilter);
            if (queueFilter) params.set("queue", queueFilter);
            if (workerFilter) params.set("worker_id", workerFilter);
            if (scheduleIdFilter) params.set("schedule_id", scheduleIdFilter);
            if (monitoringAnnotationFilter) params.set("monitoring_annotation", monitoringAnnotationFilter);
            if (attemptCountFilter) params.set("attempt_count", attemptCountFilter);
            if (failureCategoryFilter) params.set("failure_category", failureCategoryFilter);
            if (piiTypeFilter) params.set("pii_type", piiTypeFilter);
            if (piiScanStatusFilter) params.set("pii_scan_status", piiScanStatusFilter);
            if (alertTypeFilter) params.set("alert_type", alertTypeFilter);
            if (alertSeverityFilter) params.set("alert_severity", alertSeverityFilter);
            if (durationFromFilter) params.set("duration_from", durationFromFilter);
            if (durationToFilter) params.set("duration_to", durationToFilter);
            if ($globalSearchQuery) params.set("run_id", $globalSearchQuery);

            params.set("limit", "25");

            const res = await getRuns(`?${params.toString()}`);
            rows = res.runs;
            error = null;
        } catch (e) {
            error = e;
            loadError = errorMessage(e);
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
                loadRuns();
            }
        });

        ready = true;
        loadRuns();

        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) loadRuns();
        }, 10000);
    });

    onDestroy(() => {
        unsubscribePath?.();
        clearInterval(refreshTimer);
    });

    $: if (ready && $timeRange) {
        loadRuns();
    }

    $: activeFilterCount.set(
        [
            jobTypeFilter, executionStateFilter, queueFilter, workerFilter, scheduleIdFilter,
            monitoringAnnotationFilter, attemptCountFilter, failureCategoryFilter, piiTypeFilter,
            piiScanStatusFilter, alertTypeFilter, alertSeverityFilter, durationFromFilter, durationToFilter,
        ].filter(Boolean).length
    );

    function clearAllFilters() {
        jobTypeFilter = ""; executionStateFilter = ""; queueFilter = ""; workerFilter = "";
        scheduleIdFilter = ""; monitoringAnnotationFilter = ""; attemptCountFilter = "";
        failureCategoryFilter = ""; piiTypeFilter = ""; piiScanStatusFilter = "";
        alertTypeFilter = ""; alertSeverityFilter = ""; durationFromFilter = ""; durationToFilter = "";
        loadRuns();
    }

    $: state = deriveState({
        hasLoadedOnce,
        isFetching,
        error,
        isEmpty: rows.length === 0,
    });

    function formatDuration(ns: number): string {
        if (!ns) return "—";
        const seconds = ns / 1_000_000_000;
        if (seconds < 60) return `${seconds.toFixed(1)}s`;
        return `${Math.floor(seconds / 60)}m ${Math.round(seconds % 60)}s`;
    }

    function shortId(id: string): string {
        if (!id) return "—";
        return id.length > 12 ? `${id.slice(0, 8)}…` : id;
    }

    async function copyId(id: string, e: MouseEvent) {
        e.stopPropagation();
        await navigator.clipboard.writeText(id);
    }

    function monitoringHealth(row: RunListItem): "OK" | "Attention" {
        const unresolved = (row.annotations ?? []).some(a => a.ResolvedAt === null);
        return unresolved ? "Attention" : "OK";
    }

    function piiStatusLabel(row: RunListItem): string {
        if (row.ScanStatus === "SCAN_ERROR") return "Scan Error";
        const count = row.pii_findings?.length ?? 0;
        if (count > 0) return `${count} found`;
        if (row.ScanStatus === "CLEAN") return "Clean";
        return row.ScanStatus || "—";
    }

    function isFreshEventTime(iso: string | undefined): boolean {
        if (!iso) return false;
        return new Date(iso).getFullYear() > 1970;
    }

    function latestKnownTransition(run: RunListItem): { label: string; time: Date } | null {
        const candidates = [
            { label: "Run completed", iso: run.completed_at },
            { label: "Run started", iso: run.started_at },
            { label: "Run queued", iso: run.queued_at },
        ];
        const real = candidates
            .filter(c => isFreshEventTime(c.iso))
            .map(c => ({ label: c.label, time: new Date(c.iso) }));
        if (real.length === 0) return null;
        return real.reduce((latest, c) => (c.time > latest.time ? c : latest));
    }

    async function toggleExpanded(row: RunListItem) {
        if (expandedRunId === row.JobId) {
            expandedRunId = null;
            return;
        }

        expandedRunId = row.JobId;
        detailLoading = true;
        detailError = null;
        runDetail = null;
        chainSiblings = [];

        try {
            const [detail, chainRes] = await Promise.all([
                getRunDetail(row.JobId),
                getRuns(`?execution_chain_id=${row.ExecutionChainId}&limit=50&offset=0`),
            ]);

            runDetail = detail;
            chainSiblings = chainRes.runs.sort((a, b) => a.RetryIndex - b.RetryIndex);
        } catch (e) {
            detailError = errorMessage(e);
        } finally {
            detailLoading = false;
        }
    }

    function findSibling(row: RunListItem, direction: "prev" | "next"): RunListItem | undefined {
        const idx = chainSiblings.findIndex(s => s.JobId === row.JobId);
        if (idx === -1) return undefined;
        return direction === "prev" ? chainSiblings[idx - 1] : chainSiblings[idx + 1];
    }
</script>

<div class="filter-bar">
    <label>Job type:<input type="text" bind:value={jobTypeFilter} on:change={loadRuns} placeholder="e.g. CUSTOMER_UPDATE" /></label>
    <label>
        Execution state:
        <select bind:value={executionStateFilter} on:change={loadRuns}>
            <option value="">All</option>
            <option value="Pending">Pending</option>
            <option value="Running">Running</option>
            <option value="Completed">Completed</option>
            <option value="Failed">Failed</option>
        </select>
    </label>
    <label>Queue:<input type="text" bind:value={queueFilter} on:change={loadRuns} placeholder="e.g. tasks:stream" /></label>

    <button on:click={() => showAdvanced = !showAdvanced}>
        {showAdvanced ? "Hide" : "Show"} advanced filters
    </button>
    <button on:click={loadRuns}>Refresh</button>

    {#if $activeFilterCount > 0}
        <button class="clear-btn" on:click={clearAllFilters}>Clear {$activeFilterCount} filter{$activeFilterCount > 1 ? "s" : ""}</button>
    {/if}
</div>

{#if showAdvanced}
    <div class="filter-bar advanced">
        <label>Worker:<input type="text" bind:value={workerFilter} on:change={loadRuns} placeholder="e.g. Consumer-a" /></label>
        <label>Schedule ID:<input type="text" bind:value={scheduleIdFilter} on:change={loadRuns} /></label>
        <label>Monitoring annotation:<input type="text" bind:value={monitoringAnnotationFilter} on:change={loadRuns} placeholder="e.g. RUN_LOST" /></label>
        <label>Attempt count:<input type="number" bind:value={attemptCountFilter} on:change={loadRuns} /></label>
        <label>Failure category:<input type="text" bind:value={failureCategoryFilter} on:change={loadRuns} /></label>
        <label>PII type:<input type="text" bind:value={piiTypeFilter} on:change={loadRuns} placeholder="e.g. SSN" /></label>
        <label>
            PII scan status:
            <select bind:value={piiScanStatusFilter} on:change={loadRuns}>
                <option value="">All</option>
                <option value="CLEAN">Clean</option>
                <option value="DETECTED">Detected</option>
                <option value="SCAN_ERROR">Scan Error</option>
            </select>
        </label>
        <label>Alert type:<input type="text" bind:value={alertTypeFilter} on:change={loadRuns} placeholder="e.g. RUN_STUCK" /></label>
        <label>
            Alert severity:
            <select bind:value={alertSeverityFilter} on:change={loadRuns}>
                <option value="">All</option>
                <option value="INFO">INFO</option>
                <option value="WARNING">WARNING</option>
                <option value="CRITICAL">CRITICAL</option>
            </select>
        </label>
        <label>Duration from (s):<input type="number" bind:value={durationFromFilter} on:change={loadRuns} /></label>
        <label>Duration to (s):<input type="number" bind:value={durationToFilter} on:change={loadRuns} /></label>
    </div>
{/if}

{#if loadError}
    <p class="load-error">⚠ {loadError}</p>
{/if}

{#if state === "READY" || state === "REFRESHING"}
    <div class="run-table">
        <div class="run-row header">
            <span>Run ID</span><span>Retry</span><span>Job Type</span><span>Queue</span><span>State</span>
            <span>Monitoring</span><span>Attempts</span><span>Worker</span><span>Created</span>
            <span>Duration</span><span>PII</span><span>Alerts</span>
        </div>

        {#each rows as row (row.JobId)}
            <div class="run-row clickable" on:click={() => toggleExpanded(row)} role="button" tabindex="0" on:keydown={(e) => e.key === "Enter" && toggleExpanded(row)}>
                <span class="run-id-cell">
                    <span class="copyable" title={row.JobId} on:click={(e) => copyId(row.JobId, e)}>{shortId(row.JobId)}</span>
                    {#if row.RetryIndex > 0}
                        <span class="retry-indicator">↳ retry of {shortId(row.ParentRunId)}</span>
                    {/if}
                </span>
                <span>{row.RetryIndex}</span>
                <span>{row.TaskType}</span>
                <span>{row.Queue || "—"}</span>
                <span class="badge" class:failed={row.current_status === "Failed"} class:running={row.current_status === "Running"}>
                    {row.current_status || row.Status}
                </span>
                <span class="badge" class:attention={monitoringHealth(row) === "Attention"}>{monitoringHealth(row)}</span>
                <span>{row.AttemptCount}</span>
                <span>{row.LatestWorker || "—"}</span>
                <span>{new Date(row.CreatedAt).toLocaleString()}</span>
                <span>{formatDuration(row.Duration)}</span>
                <span>{piiStatusLabel(row)}</span>
                <span>{row.active_alert_count}</span>
            </div>

            {#if expandedRunId === row.JobId}
                <div class="run-detail">
                    <div class="detail-header">
                        <div class="id-row"><span class="id-label">Execution Chain:</span><span class="id-value">{row.ExecutionChainId}</span></div>
                        <div class="id-row"><span class="id-label">Parent Run:</span><span class="id-value">{row.ParentRunId || "— (origin run)"}</span></div>
                        <div class="id-row"><span class="id-label">Retry Index:</span><span class="id-value">{row.RetryIndex}</span></div>

                        {#if !detailLoading}
                            {@const prev = findSibling(row, "prev")}
                            {@const next = findSibling(row, "next")}
                            <div class="retry-links">
                                {#if prev}<button on:click|stopPropagation={() => toggleExpanded(prev)}>← Previous retry ({prev.JobId.slice(0, 8)}…)</button>{/if}
                                {#if next}<button on:click|stopPropagation={() => toggleExpanded(next)}>Next retry ({next.JobId.slice(0, 8)}…) →</button>{/if}
                            </div>
                        {/if}
                    </div>

                    {#if detailLoading}
                        <p class="loading">Loading run detail...</p>
                    {:else if detailError}
                        <p class="detail-error">{detailError}</p>
                    {:else if runDetail}
                        {@const transition = latestKnownTransition(runDetail.run)}
                        <div class="first-viewport">
                            <div class="viewport-item"><span class="viewport-label">Execution state</span>
                                <span class="badge" class:failed={runDetail.run.current_status === "Failed"}>{runDetail.run.current_status || runDetail.run.Status}</span>
                            </div>
                            <div class="viewport-item"><span class="viewport-label">Abnormal condition?</span>
                                <span class="badge" class:attention={monitoringHealth(runDetail.run) === "Attention"}>{monitoringHealth(runDetail.run)}</span>
                            </div>
                            <div class="viewport-item"><span class="viewport-label">Latest event</span>
                                <span>{#if transition}{transition.label} — {transition.time.toLocaleString()}{:else}No state transition recorded{/if}</span>
                            </div>
                            <div class="viewport-item"><span class="viewport-label">State age</span>
                                <span>{#if transition}{Math.round((Date.now() - transition.time.getTime()) / 1000)}s ago{:else}Unknown{/if}</span>
                            </div>
                            <div class="viewport-item"><span class="viewport-label">Data freshness</span>
                                <span>{#if runDetail.freshness?.last_updated_at}{runDetail.freshness.lag_seconds.toFixed(1)}s old{:else}Unknown <small>(backend gap)</small>{/if}</span>
                            </div>
                            <div class="viewport-item"><span class="viewport-label">Payload size</span>
                                <span>{runDetail.payload_size_bytes.toLocaleString()} bytes</span>
                            </div>
                        </div>

                        <hr />

                        <div class="detail-section">
                            <h4>Attempts ({runDetail.run.attempts?.length ?? 0})</h4>
                            <JsonTree value={runDetail.run.attempts ?? []} />
                        </div>
                        <div class="detail-section">
                            <h4>PII Findings ({runDetail.run.pii_findings?.length ?? 0})</h4>
                            <JsonTree value={runDetail.run.pii_findings ?? []} />
                        </div>
                        <div class="detail-section">
                            <h4>Annotations ({runDetail.run.annotations?.length ?? 0})</h4>
                            <JsonTree value={runDetail.run.annotations ?? []} />
                        </div>

                        <div class="detail-section">
                            <button on:click|stopPropagation={() => timelineChainId = row.ExecutionChainId}>View full timeline →</button>
                        </div>

                        <details>
                            <summary>Debug JSON (full response)</summary>
                            <pre>{JSON.stringify(runDetail, null, 2)}</pre>
                        </details>
                    {/if}
                </div>
            {/if}
        {/each}
    </div>
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

{#if timelineChainId}
    <TimelineView chainId={timelineChainId} onClose={() => timelineChainId = null} />
{/if}

<style>
    .filter-bar { display: flex; gap: 16px; align-items: flex-end; flex-wrap: wrap; padding: 12px 0; }
    .filter-bar.advanced { border-top: 1px dashed #ccc; margin-top: 8px; padding-top: 12px; }
    .filter-bar label { display: flex; flex-direction: column; font-size: 12px; gap: 4px; }
    .filter-bar input, .filter-bar select { padding: 6px 8px; border: 1px solid #ccc; border-radius: 4px; }
    .filter-bar button { padding: 6px 12px; border: 1px solid #ccc; border-radius: 4px; background: white; cursor: pointer; }
    .clear-btn { background: #fee2e2; border-color: #991b1b; color: #991b1b; }
    .load-error { color: #991b1b; background: #fee2e2; padding: 8px 16px; border-radius: 4px; font-size: 13px; }

    .run-table { margin-top: 12px; border: 1px solid #ddd; border-radius: 8px; overflow-x: auto; }
    .run-row { display: grid; grid-template-columns: 1fr 0.6fr 1.2fr 1fr 1fr 1fr 0.8fr 1fr 1.4fr 1fr 1fr 0.8fr; gap: 12px; padding: 10px 16px; border-top: 1px solid #ddd; font-size: 13px; align-items: center; }
    .run-row.header { font-weight: bold; background: #f5f5f5; border-top: none; }
    .run-row.clickable { cursor: pointer; }
    .run-row.clickable:hover { background: #fafafa; }

    .run-id-cell { display: flex; flex-direction: column; gap: 2px; }
    .retry-indicator { font-size: 11px; color: #888; }
    .copyable { cursor: pointer; text-decoration: underline dotted; font-family: monospace; width: fit-content; }

    .badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: bold; background: #eee; width: fit-content; }
    .badge.failed { background: #fee2e2; color: #991b1b; }
    .badge.running { background: #dbeafe; color: #1e40af; }
    .badge.attention { background: #fef3c7; color: #92400e; }

    .run-detail { padding: 16px 24px; background: #fafafa; border-top: 1px solid #eee; font-size: 13px; }
    .detail-header { margin-bottom: 16px; }
    .id-row { display: flex; gap: 8px; margin-bottom: 4px; }
    .id-label { color: #555; min-width: 130px; }
    .id-value { font-family: monospace; }
    .retry-links { display: flex; gap: 8px; margin-top: 8px; }
    .retry-links button { padding: 4px 10px; border: 1px solid #ccc; border-radius: 4px; background: white; cursor: pointer; font-size: 12px; }

    .first-viewport { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; margin-bottom: 8px; }
    .viewport-item { display: flex; flex-direction: column; gap: 4px; }
    .viewport-label { font-size: 11px; color: #888; }

    .detail-section { margin-top: 16px; }
    .detail-section h4 { font-size: 13px; margin-bottom: 8px; }
    .loading, .detail-error { padding: 12px 0; color: #666; }
</style>