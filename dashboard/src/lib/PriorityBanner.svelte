<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { getAlerts } from "../lib/api";

    // Cheapest signal that most correlates with "worth investigating right
    // now": open CRITICAL alerts, falling back to open WARNING. This is
    // deliberately NOT re-deriving worker/queue/monitoring status itself --
    // every real problem in those areas already produces an alert (that's
    // what §9's alert rules exist for), so open alerts ARE the priority
    // signal, not a separate computation.
    let criticalCount = 0;
    let warningCount = 0;
    let topAlert: string | null = null;
    let error: unknown = null;

    async function load() {
        try {
            const [critRes, warnRes] = await Promise.all([
                getAlerts(`?severity=CRITICAL&status=OPEN&limit=1&offset=0`),
                getAlerts(`?severity=WARNING&status=OPEN&limit=1&offset=0`),
            ]);
            criticalCount = critRes.page.total ?? 0;
            warningCount = warnRes.page.total ?? 0;
            topAlert = criticalCount > 0
                ? critRes.alerts[0]?.summary ?? null
                : warningCount > 0
                ? warnRes.alerts[0]?.summary ?? null
                : null;
            error = null;
        } catch (e) {
            error = e;
        }
    }

    let refreshTimer: ReturnType<typeof setInterval>;
    onMount(() => {
        load();
        refreshTimer = setInterval(() => {
            if ($autoRefreshEnabled) load();
        }, 10000);
    });
    onDestroy(() => clearInterval(refreshTimer));

    $: level = criticalCount > 0 ? "critical" : warningCount > 0 ? "warning" : "healthy";
</script>

{#if !error}
    <div class="banner" class:critical={level === "critical"} class:warning={level === "warning"} class:healthy={level === "healthy"}>
        {#if level === "critical"}
            <strong>{criticalCount} critical alert{criticalCount > 1 ? "s" : ""} open.</strong>
            {#if topAlert}<span> Start here: {topAlert}</span>{/if}
        {:else if level === "warning"}
            <strong>{warningCount} warning alert{warningCount > 1 ? "s" : ""} open, nothing critical.</strong>
            {#if topAlert}<span> {topAlert}</span>{/if}
        {:else}
            <strong>No open alerts.</strong> <span>System looks healthy right now.</span>
        {/if}
    </div>
{/if}

<style>
    .banner { padding: 14px 20px; border-radius: 8px; margin-bottom: 20px; font-size: 14px; }
    .banner.critical { background: #fee2e2; color: #991b1b; border: 1px solid #fca5a5; }
    .banner.warning { background: #fef3c7; color: #92400e; border: 1px solid #fcd34d; }
    .banner.healthy { background: #dcfce7; color: #166534; border: 1px solid #86efac; }
</style>