<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { getMonitoringHealth } from "./api";
    import { startRefresh } from "./refresh";
    import { overviewRefreshTick } from "./liveRefreshStores";
    import { pipelineLag, type LagItem } from "./monitoringView";

    // RFC-010 §22: ingestion / projection / live-stream lag, beside the Overview aggregates. Hidden when the backend does not answer.
    let items: LagItem[] = [];
    let stopRefresh: (() => void) | undefined;

    async function load() {
        try {
            items = pipelineLag(await getMonitoringHealth());
        } catch {
            items = [];
        }
    }

    onMount(() => {
        load();
        stopRefresh = startRefresh(load, { ticks: [overviewRefreshTick], gaugeEveryMs: 30000 });
    });
    onDestroy(() => stopRefresh?.());
</script>

{#if items.length}
    <section class="lag-strip" aria-label="Pipeline lag">
        <h3>Pipeline lag</h3>
        <ul>
            {#each items as i}
                <li class={i.level} title={i.level === "slow" ? "Older than 60s. On a quiet system this only means no recent events -- it is not by itself a fault." : ""}><span class="k">{i.label}</span> <span class="v">{i.text}</span></li>
            {/each}
        </ul>
    </section>
{/if}

<style>
    .lag-strip { display: flex; flex-wrap: wrap; align-items: center; gap: 8px 16px; margin: 8px 0 12px; }
    .lag-strip h3 { margin: 0; font-size: 0.9rem; }
    ul { display: flex; flex-wrap: wrap; gap: 8px; list-style: none; margin: 0; padding: 0; }
    li { padding: 2px 10px; border: 1px solid #999; border-radius: 12px; font-size: 0.85rem; }
    li.ok { border-color: #2e7d32; }
    li.slow { border-color: #b45309; color: #92400e; }
    li.unknown { color: #666; }
    .k { font-weight: 600; }
</style>
