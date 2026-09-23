<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getWorkers, getQueues, getMonitoringHealth, getAlerts, type WorkerListItem, type QueueHealth, type MonitoringHealthResponse } from "../lib/api";
    import { deriveState, errorMessage } from "../lib/dataState";
    import DataStateBanner from "../lib/DataStateBanner.svelte";
    import JsonTree from "../lib/JsonTree.svelte";

    type ComponentStatus = "healthy" | "degraded" | "offline" | "not-built";

    interface ComponentRow {
        name: string;
        status: ComponentStatus;
        evidence: unknown;
    }

    const DEGRADED_HEARTBEAT_SECONDS = 60;
    const OFFLINE_HEARTBEAT_SECONDS = 300;
    const DEGRADED_PENDING_THRESHOLD = 20;
    const FRESH_LAG_SECONDS = 60; // a subsystem newer than this is "healthy"

    let components: ComponentRow[] = [];
    let error: unknown = null;
    let refreshTimer: ReturnType<typeof setInterval>;
    let hasLoadedOnce = false;
    let isFetching = false;
    let expanded: string | null = null;

    function workerStatus(w: WorkerListItem): "online" | "degraded" | "offline" {
        const age = (Date.now() - new Date(w.LastHeartbeat).getTime()) / 1000;
        if (age > OFFLINE_HEARTBEAT_SECONDS) return "offline";
        if (age > DEGRADED_HEARTBEAT_SECONDS) return "degraded";
        return "online";
    }

    function subsystemStatus(mon: MonitoringHealthResponse, name: string): ComponentStatus {
        const s = mon.subsystems.find(x => x.subsystem === name);
        if (!s || !s.available) return "offline";
        if (s.lag_seconds! > FRESH_LAG_SECONDS) return "degraded";
        return "healthy";
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
            const offlineWorkers = workers.filter(w => workerStatus(w) === "offline").length;
            const degradedWorkers = workers.filter(w => workerStatus(w) === "degraded").length;

            const degradedQueues = queuesRes.queues.filter((q: QueueHealth) => q.PendingCount > DEGRADED_PENDING_THRESHOLD);

            components = [
                {
                    name: "API",
                    status: "healthy",
                    evidence: { note: "self-evident -- this page loaded via a real API response" },
                },
                {
                    name: "Query API",
                    status: "healthy",
                    evidence: { note: "self-evident -- same reasoning as API" },
                },
                 {
                    name: "Scheduler",
                    status: (missedRes.page.total ?? 0) > 0 ? "degraded" : "healthy",
                    evidence: { open_schedule_missed_alerts: missedRes.page.total ?? 0 },
                },
                {
                    name: "Redis Delivery",
                    status: degradedQueues.length > 0 ? "degraded" : "healthy",
                    evidence: { total_queues: queuesRes.queues.length, degraded_queues: degradedQueues.map(q => q.QueueName) },
                },
                {
                    name: "Workers",
                    status: offlineWorkers === workers.length && workers.length > 0 ? "offline" : (offlineWorkers > 0 || degradedWorkers > 0) ? "degraded" : "healthy",
                    evidence: { total: workers.length, offline: offlineWorkers, degraded: degradedWorkers },
                },
                {
                    name: "Monitoring Ingestor / Projector",
                    status: [subsystemStatus(monRes, "Event Ingestion"), subsystemStatus(monRes, "Projection")].includes("offline")
                        ? "offline"
                        : [subsystemStatus(monRes, "Event Ingestion"), subsystemStatus(monRes, "Projection")].includes("degraded")
                        ? "degraded"
                        : "healthy",
                    evidence: {
                        event_ingestion: monRes.subsystems.find(s => s.subsystem === "Event Ingestion"),
                        projection: monRes.subsystems.find(s => s.subsystem === "Projection"),
                    },
                },
                {
                    name: "PII Scanner",
                    status: subsystemStatus(monRes, "PII Scanner"),
                    evidence: monRes.subsystems.find(s => s.subsystem === "PII Scanner"),
                },
                {
                    name: "Alert Evaluator",
                    status: subsystemStatus(monRes, "Alert Evaluation"),
                    evidence: monRes.subsystems.find(s => s.subsystem === "Alert Evaluation"),
                },
                {
                    name: "Real-Time Gateway",
                    status: "not-built",
                    evidence: { note: "does not exist yet -- this is RFC-010, not built in this pass" },
                },
            ];

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

    $: healthyCount = components.filter(c => c.status === "healthy").length;
    $: degradedCount = components.filter(c => c.status === "degraded").length;
    $: offlineCount = components.filter(c => c.status === "offline").length;
    $: notBuiltCount = components.filter(c => c.status === "not-built").length;

    function toggleExpanded(name: string) {
        expanded = expanded === name ? null : name;
    }
</script>

<h3>Project Components</h3>

{#if state === "READY" || state === "REFRESHING"}
    <div class="summary-cards">
        <div class="summary-card healthy"><strong>{healthyCount}</strong><span>Healthy</span></div>
        <div class="summary-card degraded"><strong>{degradedCount}</strong><span>Degraded</span></div>
        <div class="summary-card offline"><strong>{offlineCount}</strong><span>Offline</span></div>
        <div class="summary-card not-built"><strong>{notBuiltCount}</strong><span>Not Built</span></div>
    </div>

    <div class="component-list">
        {#each components as c (c.name)}
            <div class="component-row" on:click={() => toggleExpanded(c.name)} role="button" tabindex="0" on:keydown={(e) => e.key === "Enter" && toggleExpanded(c.name)}>
                <span class="badge" class:healthy={c.status === "healthy"} class:degraded={c.status === "degraded"} class:offline={c.status === "offline"} class:not-built={c.status === "not-built"}>
                    {c.status}
                </span>
                <span class="component-name">{c.name}</span>
            </div>

            {#if expanded === c.name}
                <div class="evidence-panel">
                    <JsonTree value={c.evidence} />
                </div>
            {/if}
        {/each}
    </div>
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    .summary-cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; margin-bottom: 20px; }
    .summary-card { padding: 16px; border: 1px solid #ddd; border-radius: 8px; text-align: center; }
    .summary-card strong { display: block; font-size: 28px; }
    .summary-card span { font-size: 12px; color: #666; }
    .summary-card.healthy strong { color: #166534; }
    .summary-card.degraded strong { color: #92400e; }
    .summary-card.offline strong { color: #991b1b; }
    .summary-card.not-built strong { color: #888; }

    .component-list { border: 1px solid #ddd; border-radius: 8px; overflow: hidden; }
    .component-row { display: flex; gap: 16px; align-items: center; padding: 12px 16px; border-top: 1px solid #ddd; cursor: pointer; }
    .component-row:hover { background: #fafafa; }
    .component-name { font-size: 14px; }

    .badge { display: inline-block; padding: 3px 10px; border-radius: 999px; font-size: 11px; font-weight: bold; width: 80px; text-align: center; }
    .badge.healthy { background: #dcfce7; color: #166534; }
    .badge.degraded { background: #fef3c7; color: #92400e; }
    .badge.offline { background: #fee2e2; color: #991b1b; }
    .badge.not-built { background: #f3f4f6; color: #6b7280; }

    .evidence-panel { padding: 12px 24px; background: #fafafa; border-top: 1px solid #eee; font-size: 13px; }
</style>