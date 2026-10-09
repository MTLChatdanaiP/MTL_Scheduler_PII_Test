<script lang="ts">
    import { activateOnKey } from "../lib/a11y";
    import { startRefresh } from "../lib/refresh";
    import { usePageLive } from "../lib/pageLive";
    import { componentRefreshTick } from "../lib/liveRefreshStores";
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getWorkers, getQueues, getMonitoringHealth, getAlerts, getComponents, type WorkerListItem, type QueueHealth, type MonitoringHealthResponse, type ComponentsResponse } from "../lib/api";
    import { healthClass, dependencyClass, revisionLabel, mixedRevisions, derivedToRows, mergeComponentRows } from "../lib/componentView";
    import { deriveState, errorMessage } from "../lib/dataState";
    import { DEGRADED_HEARTBEAT_SECONDS, OFFLINE_HEARTBEAT_SECONDS, queueDegraded } from "../lib/thresholds";
    import { serverHealthToStatus } from "../lib/componentView";
    import { verdictFromAge, verdictFromCount, worstVerdict, staticVerdict, isZeroTimestamp, type ComponentStatus, type Verdict } from "../lib/componentHealth";
    import DataStateBanner from "../lib/DataStateBanner.svelte";
    import JsonTree from "../lib/JsonTree.svelte";

    // RFC-010 §24: the fields this page reads off GET /monitoring/health's
    // live_pipeline block. Declared locally rather than in api.ts so this
    // page doesn't depend on that type having been extended yet.
    interface LivePipelineStats {
        active_connections: number;
        last_heartbeat_at?: string;
        last_published_at?: string;
    }

    interface ComponentRow {
        name: string;
        status: ComponentStatus;
        reason: string;
        evidence: unknown;
    }

    let report: ComponentsResponse | null = null;
    $: mixed = mixedRevisions(report?.components);

    const FRESH_LAG_SECONDS = 60; // a subsystem newer than this is "healthy"

    let components: ComponentRow[] = [];
    let error: unknown = null;
    let stopRefresh: (() => void) | undefined;
    let hasLoadedOnce = false;
    let isFetching = false;
    let expanded: string | null = null;

    // Every worker's own liveness, through the shared age-based box. A Worker
    // row that has never actually heartbeated comes back with Go's zero-value
    // timestamp, not null -- without this guard that reads as an age of
    // roughly 2000 years and reports OFFLINE with a nonsensical reason
    // ("17757318h ago") instead of the true state, UNKNOWN.
    function workerVerdict(w: WorkerListItem): Verdict {
        if (isZeroTimestamp(w.LastHeartbeat)) {
            return { status: "unknown", reason: `${w.WorkerId}: no heartbeat ever recorded` };
        }
        // Batch 7: the server's verdict (and its reason) wins; the age thresholds are only the fallback for an older backend.
        const fromServer = serverHealthToStatus(w.health);
        if (fromServer) {
            const status = fromServer === "online" ? "healthy" : fromServer;
            return { status, reason: w.health_reason || `${w.WorkerId}: ${w.health}` };
        }
        const age = (Date.now() - new Date(w.LastHeartbeat).getTime()) / 1000;
        return verdictFromAge(age, DEGRADED_HEARTBEAT_SECONDS, OFFLINE_HEARTBEAT_SECONDS, `${w.WorkerId} heartbeat`);
    }

    // Some subsystems are heartbeat-style: a background sweep touches them on
    // a fixed timer regardless of whether there is anything to do, so
    // staleness genuinely means "stopped running" (Alert Evaluation's sweep
    // ticks every 20s unconditionally). Others are activity-style: their
    // freshness is just "when did real work last happen" (Event Ingestion,
    // Projection and the PII Scanner only advance when a task runs) -- if the
    // system is simply quiet, that is not a fault, so staleness there must
    // never escalate past DEGRADED.
    //
    // A subsystem with no successful observation on record at all is UNKNOWN,
    // not OFFLINE: "we never saw it run" is a weaker claim than "it stopped
    // running". monitoring_health_handler.go reports this whenever its
    // newest-row query comes back empty (also the normal state right after a
    // fresh reset, not necessarily a failure).
    function subsystemVerdict(mon: MonitoringHealthResponse, name: string, kind: "heartbeat" | "activity"): Verdict {
        const s = mon.subsystems.find((x) => x.subsystem === name);
        if (!s || !s.available) return { status: "unknown", reason: `${name}: no successful observation on record` };
        const offlineAfter = kind === "heartbeat" ? FRESH_LAG_SECONDS * 5 : null;
        return verdictFromAge(s.lag_seconds ?? null, FRESH_LAG_SECONDS, offlineAfter, `${name} observation`);
    }

    // The live gateway reports on itself via live_pipeline (RFC-010 §24). Its
    // heartbeat ticker only runs per active connection -- with zero
    // connections nobody is there to ping, so a stale/missing heartbeat then
    // is completely expected, not evidence of a problem. Checking
    // active_connections first, before looking at the heartbeat age at all,
    // is what makes that distinction.
    function gatewayVerdict(live: LivePipelineStats | undefined): Verdict {
        if (!live) return { status: "unknown", reason: "monitoring/health did not report live_pipeline" };

        if (live.active_connections === 0) {
            return { status: "healthy", reason: "no active live connections (idle, not evidence of a problem)" };
        }

        if (isZeroTimestamp(live.last_heartbeat_at)) {
            return { status: "unknown", reason: `${live.active_connections} connection(s), but no heartbeat recorded yet` };
        }

        const age = (Date.now() - new Date(live.last_heartbeat_at as string).getTime()) / 1000;
        return verdictFromAge(age, 90, 300, "gateway heartbeat");
    }

    async function load() {
        isFetching = true;
        try {
            const [workersRes, queuesRes, monRes, missedRes] = await Promise.all([
                getWorkers(),
                getQueues(),
                getMonitoringHealth(),
                getAlerts(`?alert_type=SCHEDULE_MISSED&status=OPEN&limit=5&offset=0`),
            ]);

            const workers = workersRes.workers;
            const workerVerdicts = workers.map(workerVerdict);
            const offlineWorkers = workerVerdicts.filter((v) => v.status === "offline").length;
            const degradedWorkers = workerVerdicts.filter((v) => v.status === "degraded").length;

            const degradedQueues = queuesRes.queues.filter((q: QueueHealth) => queueDegraded(q));

            const eventIngestion = subsystemVerdict(monRes, "Event Ingestion", "activity");
            const projection = subsystemVerdict(monRes, "Projection", "activity");
            const livePipeline = (monRes as MonitoringHealthResponse & { live_pipeline?: LivePipelineStats }).live_pipeline;

            const rows: [string, Verdict, unknown][] = [
                ["API", staticVerdict("healthy", "self-evident -- this page loaded via a real API response"),
                    { note: "self-evident" }],
                ["Query API", staticVerdict("healthy", "self-evident -- same reasoning as API"),
                    { note: "self-evident" }],
                ["Scheduler", verdictFromCount(missedRes.page.total ?? 0, 1, 5, "open schedule-missed alerts"),
                    { open_schedule_missed_alerts: missedRes.page.total ?? 0 }],
                ["Redis Delivery",
                    verdictFromCount(degradedQueues.length, 1, queuesRes.queues.length || null, "degraded queues"),
                    { total_queues: queuesRes.queues.length, degraded_queues: degradedQueues.map((q) => q.QueueName) }],
                // Aggregation policy (how one fleet of workers rolls up into one row) is
                // this page's own judgment call, not something the shared box decides --
                // it just combines the per-worker verdicts the box already produced.
                ["Workers", workers.length === 0 ? staticVerdict("unknown", "no workers reporting") : worstVerdict(...workerVerdicts),
                    { total: workers.length, offline: offlineWorkers, degraded: degradedWorkers }],
                ["Monitoring Ingestor / Projector", worstVerdict(eventIngestion, projection), // both activity-driven, so this row can read DEGRADED but never OFFLINE
                    { event_ingestion: monRes.subsystems.find((s) => s.subsystem === "Event Ingestion"), projection: monRes.subsystems.find((s) => s.subsystem === "Projection") }],
                ["PII Scanner", subsystemVerdict(monRes, "PII Scanner", "activity"), // scans run synchronously on task creation, not on a timer
                    monRes.subsystems.find((s) => s.subsystem === "PII Scanner")],
                ["Alert Evaluator", subsystemVerdict(monRes, "Alert Evaluation", "heartbeat"), // the alerts sweep ticks every 20s unconditionally
                    monRes.subsystems.find((s) => s.subsystem === "Alert Evaluation")],
                ["Real-Time Gateway", gatewayVerdict(livePipeline),
                    livePipeline ?? { note: "monitoring/health did not report live_pipeline" }],
            ];

            components = rows.map(([name, verdict, evidence]) => ({ name, status: verdict.status, reason: verdict.reason, evidence }));

            // RFC-005 §7: the server-side projection (build revisions, per-instance reasons, dependencies). An older backend has no
            // /components, so its absence is not an error and the rows above still stand on their own.
            try {
                report = await getComponents();
            } catch {
                report = null;
            }

            // RFC-010 §8: when the server judges the non-process components itself, show ITS verdicts (with their lag and threshold) in place of
            // the ones this page used to work out in the browser. Scheduler and Workers stay as aggregated above.
            if (report?.derived?.length) {
                components = mergeComponentRows(components, derivedToRows(report.derived));
            }

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
        stopRefresh = startRefresh(load, { ticks: [componentRefreshTick], gaugeEveryMs: 10000 });
    });

    // RFC-009 §17: component health changes arrive as component.* events (RFC-010 §8/§9); the 10s timer in lib/refresh.ts covers the
    // heartbeat ages, which change without any event.
    usePageLive({ scope: "components", prefixes: ["component."], tick: componentRefreshTick });

    onDestroy(() => stopRefresh?.());

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: false });

    const STATUS_ORDER: ComponentStatus[] = ["healthy", "degraded", "unhealthy", "offline", "unknown", "not-built"];
    $: counts = STATUS_ORDER.map((status) => ({ status, count: components.filter((c) => c.status === status).length }));

    function toggleExpanded(name: string) {
        expanded = expanded === name ? null : name;
    }
</script>

<h3>Project Components</h3>

{#if state === "READY" || state === "REFRESHING"}
    <div class="summary-cards">
        {#each counts as { status, count } (status)}
            <div class="summary-card {status}"><strong>{count}</strong><span>{status === "not-built" ? "Not Built" : status}</span></div>
        {/each}
    </div>

    <div class="component-list">
        {#each components as c (c.name)}
            <div class="component-row" on:click={() => toggleExpanded(c.name)} role="button" tabindex="0" on:keydown={(e) => activateOnKey(e, () => toggleExpanded(c.name))}>
                <span class="badge" class:healthy={c.status === "healthy"} class:degraded={c.status === "degraded"} class:unhealthy={c.status === "unhealthy"} class:offline={c.status === "offline"} class:unknown={c.status === "unknown"} class:not-built={c.status === "not-built"}>
                    {c.status}
                </span>
                <span class="component-name">{c.name}</span>
                <span class="component-reason">{c.reason}</span>
            </div>

            {#if expanded === c.name}
                <div class="evidence-panel">
                    <p class="reason-line"><strong>Reason:</strong> {c.reason}</p>
                    <JsonTree value={c.evidence} />
                </div>
            {/if}
        {/each}
    </div>

    {#if report}
        <h3 class="section-title">Instances <small>(from the server's component health projection)</small></h3>
        {#if mixed.length > 1}
            <p class="mixed-warning">⚠ Instances are running {mixed.length} different builds: {mixed.join(", ")}. A deploy may not have reached everything.</p>
        {/if}
        {#if report.components.length === 0}
            <p class="empty-note">No component instances have registered yet.</p>
        {:else}
            <div class="instance-table">
                <div class="instance-row header">
                    <span>Type</span><span>Instance</span><span>Build</span><span>Started</span><span>Last heartbeat</span><span>Health</span>
                </div>
                {#each report.components as i (i.component_instance_id)}
                    <div class="instance-row">
                        <span>{i.component_type}</span>
                        <span class="mono" title={i.component_instance_id}>{i.display_name}</span>
                        <span class="mono">{revisionLabel(i.build_revision)}</span>
                        <span>{new Date(i.started_at).toLocaleString()}</span>
                        <span>{i.last_heartbeat ? new Date(i.last_heartbeat).toLocaleTimeString() : "never"}</span>
                        <span class="health {healthClass(i.health)}" title={i.reason}>{i.health}</span>
                    </div>
                {/each}
            </div>
        {/if}

        <h3 class="section-title">Dependencies <small>(as observed by the API, build {revisionLabel(report.observed_by.build_revision)})</small></h3>
        <div class="instance-table">
            {#each report.dependencies as d (d.name)}
                <div class="dep-row">
                    <span class="dep {dependencyClass(d.status)}">{d.status}</span>
                    <span class="mono">{d.name}</span>
                    <span class="reason-line">{d.detail}</span>
                </div>
            {/each}
        </div>
    {/if}
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    .section-title { margin-top: 28px; }
    .section-title small { font-weight: normal; color: #888; font-size: 12px; }
    .mixed-warning { margin: 8px 0; padding: 8px 12px; background: #fef3c7; color: #92400e; border-radius: 6px; font-size: 12px; }
    .empty-note { color: #888; font-size: 13px; }
    .instance-table { border: 1px solid #ddd; border-radius: 8px; overflow-x: auto; }
    .instance-row { display: grid; grid-template-columns: 0.8fr 1.4fr 1fr 1.4fr 1fr 0.9fr; gap: 12px; padding: 8px 16px; border-top: 1px solid #eee; font-size: 13px; align-items: center; }
    .instance-row.header { font-weight: bold; background: #f5f5f5; border-top: none; }
    .dep-row { display: grid; grid-template-columns: 110px 1fr 2fr; gap: 12px; padding: 8px 16px; border-top: 1px solid #eee; font-size: 13px; align-items: center; }
    .dep-row:first-child { border-top: none; }
    .mono { font-family: monospace; }
    .health, .dep { display: inline-block; padding: 1px 8px; border-radius: 999px; font-size: 11px; font-weight: bold; width: fit-content; background: #eee; }
    .health.healthy, .dep.ok { background: #dcfce7; color: #166534; }
    .health.degraded { background: #fef3c7; color: #92400e; }
    .health.offline, .dep.unavailable { background: #fee2e2; color: #991b1b; }
    .health.unknown, .dep.unknown { background: #ede9fe; color: #5b21b6; }

    .summary-cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(100px, 1fr)); gap: 16px; margin-bottom: 20px; }
    .summary-card { padding: 16px; border: 1px solid #ddd; border-radius: 8px; text-align: center; }
    .summary-card strong { display: block; font-size: 28px; }
    .summary-card span { font-size: 12px; color: #666; }
    .summary-card.healthy strong { color: #166534; }
    .summary-card.degraded strong { color: #92400e; }
    .summary-card.offline strong { color: #991b1b; }
    .summary-card.not-built strong { color: #888; }
    .summary-card.unhealthy strong { color: #b91c1c; }
    .summary-card.unknown strong { color: #5b21b6; }

    .component-list { border: 1px solid #ddd; border-radius: 8px; overflow: hidden; }
    .component-row { display: flex; gap: 16px; align-items: center; padding: 12px 16px; border-top: 1px solid #ddd; cursor: pointer; }
    .component-row:hover { background: #fafafa; }
    .component-name { font-size: 14px; }
    .component-reason { font-size: 12px; color: #888; margin-left: auto; }

    .badge { display: inline-block; padding: 3px 10px; border-radius: 999px; font-size: 11px; font-weight: bold; width: 80px; text-align: center; }
    .badge.healthy { background: #dcfce7; color: #166534; }
    .badge.degraded { background: #fef3c7; color: #92400e; }
    .badge.offline { background: #fee2e2; color: #991b1b; }
    .badge.not-built { background: #f3f4f6; color: #6b7280; }
    .badge.unhealthy { background: #fecaca; color: #b91c1c; }
    .badge.unknown { background: #ede9fe; color: #5b21b6; }

    .evidence-panel { padding: 12px 24px; background: #fafafa; border-top: 1px solid #eee; font-size: 13px; }
    .reason-line { margin: 0 0 8px; color: #444; }
</style>