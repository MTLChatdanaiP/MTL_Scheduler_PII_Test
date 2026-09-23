<script lang="ts">
    import { onMount } from "svelte";
    import { getRuns, type RunListItem } from "./api";

    export let queueName: string | null = null;
    export let scheduleId: string | null = null;
    export let onClose: () => void;

    let runs: RunListItem[] = [];
    let total: number | undefined = 0;
    let loading = true;
    let error: string | null = null;

    

    async function load() {
        loading = true;
        try {
            const params = new URLSearchParams();
            if (queueName) params.set("queue", queueName);
            if (scheduleId) params.set("schedule_id", scheduleId);
            params.set("limit", "25");
            params.set("offset", "0");
 
            const res = await getRuns(`?${params.toString()}`);
            runs = res.runs;
            total = res.page.total;
            error = null;
        } catch (e) {
            error = e instanceof Error ? e.message : "Something went wrong.";
        } finally {
            loading = false;
        }
    }

    onMount(load);

    function shortId(id: string): string {
        return id.length > 12 ? `${id.slice(0, 8)}…` : id;
    }
</script>

<div class="overlay" on:click={onClose} role="presentation">
    <div class="panel" on:click|stopPropagation role="dialog" aria-label="Runs affected by queue" tabindex="-1">
        <div class="panel-header">
            <h3>Affected Runs — {queueName ?? scheduleId}</h3>
            <button class="close-btn" on:click={onClose}>✕</button>
        </div>

        {#if loading}
            <p class="status">Loading...</p>
        {:else if error}
            <p class="status error">{error}</p>
        {:else}
            <p class="status-line">{total ?? runs.length} run(s) match</p>

            <div class="run-list">
                {#each runs as run (run.JobId)}
                    <div class="run-row">
                        <span class="run-id">{shortId(run.JobId)}</span>
                        <span>{run.TaskType}</span>
                        <span class="badge" class:failed={run.current_status === "Failed"}>
                            {run.current_status || run.Status}
                        </span>
                        <span>{new Date(run.CreatedAt).toLocaleString()}</span>
                    </div>
                {/each}

                {#if runs.length === 0}
                    <p class="status">No runs match.</p>
                {/if}
            </div>
        {/if}
    </div>
</div>

<style>
    .overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.4); display: flex; align-items: center; justify-content: center; z-index: 100; }
    .panel { background: white; width: 90%; max-width: 800px; max-height: 80vh; border-radius: 8px; overflow: hidden; display: flex; flex-direction: column; }
    .panel-header { display: flex; justify-content: space-between; align-items: center; padding: 16px 20px; border-bottom: 1px solid #eee; }
    .panel-header h3 { margin: 0; font-size: 16px; }
    .close-btn { background: none; border: none; font-size: 18px; cursor: pointer; color: #888; }

    .status { padding: 20px; text-align: center; color: #666; }
    .status.error { color: #991b1b; }
    .status-line { padding: 8px 20px 0; font-size: 12px; color: #888; }

    .run-list { overflow-y: auto; padding: 8px 20px 20px; }
    .run-row { display: grid; grid-template-columns: 1fr 1fr 1fr 1.4fr; gap: 12px; padding: 8px 0; border-bottom: 1px solid #f0f0f0; font-size: 12px; align-items: center; }
    .run-id { font-family: monospace; }
    .badge { display: inline-block; padding: 2px 8px; border-radius: 999px; font-size: 10px; font-weight: bold; background: #eee; width: fit-content; }
    .badge.failed { background: #fee2e2; color: #991b1b; }
</style>