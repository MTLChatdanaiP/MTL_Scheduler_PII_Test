<script lang="ts">
    import { onMount } from "svelte";
    import { getChainTimeline, type TimelineEntry } from "./api";
    import JsonTree from "./JsonTree.svelte";

    export let chainId: string;
    export let onClose: () => void;

    let entries: TimelineEntry[] = [];
    let truncated = false;
    let loading = true;
    let error: string | null = null;

    // §6 asks for grouping/filtering noisy event classes WITHOUT deleting
    // them from the underlying evidence — this hides them client-side only,
    // the fetched data is untouched.
    let hideNoisyEvents = true;

    function isNoisy(entry: TimelineEntry): boolean {
        return entry.event_type.includes("progress") || entry.event_type.includes("heartbeat");
    }

    $: visibleEntries = hideNoisyEvents ? entries.filter(e => !isNoisy(e)) : entries;
    $: hiddenCount = entries.length - visibleEntries.length;

    onMount(async () => {
        try {
            const res = await getChainTimeline(chainId);
            entries = res.entries;
            truncated = res.truncated;
        } catch (e) {
            error = (e as Error).message;
        } finally {
            loading = false;
        }
    });

    // No backend field for this — TimelineEntry has no "is_late" marker, so
    // this is a real gap, not something faked here. §6 asks for it, it
    // isn't available yet.

    function shortId(id: string): string {
        return id.length > 12 ? `${id.slice(0, 8)}…` : id;
    }

    // TimelineEntry has no dedicated "summary" field — built here from what
    // IS present, same idea as Alert.summary but composed client-side
    // instead of stored.
    function buildSummary(entry: TimelineEntry): string {
        const worker = entry.detail?.worker_id;
        const attemptNum = entry.detail?.attempt_number;
        if (worker && attemptNum) return `${entry.event_type} (attempt ${attemptNum}, ${worker})`;
        if (entry.detail?.summary) return String(entry.detail.summary);
        return entry.event_type;
    }
</script>

<div class="overlay" on:click={onClose} role="presentation">
    <div class="panel" on:click|stopPropagation role="dialog" aria-label="Execution chain timeline" tabindex="-1">
        <div class="panel-header">
            <h3>Timeline — Chain {shortId(chainId)}</h3>
            <button class="close-btn" on:click={onClose}>✕</button>
        </div>

        <div class="panel-controls">
            <label>
                <input type="checkbox" bind:checked={hideNoisyEvents} />
                Hide noisy events (progress/heartbeat)
                {#if hiddenCount > 0}<span class="hidden-count">({hiddenCount} hidden)</span>{/if}
            </label>
            <span class="gap-note">Late-arrival markers not yet available (backend gap)</span>
        </div>

        {#if loading}
            <p class="status">Loading timeline...</p>
        {:else if error}
            <p class="status error">{error}</p>
        {:else}
            {#if truncated}
                <p class="status warning">Showing first 1000 entries — this chain has more.</p>
            {/if}

            <div class="timeline-list">
                {#each visibleEntries as entry, i (i)}
                    <div class="timeline-entry">
                        <div class="entry-time">{new Date(entry.occurred_at).toLocaleString()}</div>
                        <div class="entry-source badge" class:event={entry.source === "EVENT"} class:attempt={entry.source === "ATTEMPT"} class:pii={entry.source === "PII"} class:alert={entry.source === "ALERT"} class:annotation={entry.source === "ANNOTATION"}>
                            {entry.source}
                        </div>
                        <div class="entry-run">{shortId(entry.run_id)}</div>
                         <div class="entry-summary">
                            {buildSummary(entry)}
                            {#if entry.detail?.worker_id}
                                <span class="entry-worker">· {entry.detail.worker_id}</span>
                            {/if}
                            {#if entry.detail?.attempt_number}
                                <span class="entry-attempt">· attempt {entry.detail.attempt_number}</span>
                            {/if}
                        </div>
                        {#if entry.detail}
                            <div class="entry-detail">
                                <JsonTree value={entry.detail} />
                            </div>
                        {/if}
                    </div>
                {/each}

                {#if visibleEntries.length === 0}
                    <p class="status">No events to show.</p>
                {/if}
            </div>
        {/if}
    </div>
</div>

<style>
    .overlay {
        position: fixed;
        inset: 0;
        background: rgba(0, 0, 0, 0.4);
        display: flex;
        align-items: center;
        justify-content: center;
        z-index: 100;
    }
    .panel {
        background: white;
        width: 90%;
        max-width: 900px;
        max-height: 85vh;
        border-radius: 8px;
        overflow: hidden;
        display: flex;
        flex-direction: column;
    }
    .panel-header {
        display: flex;
        justify-content: space-between;
        align-items: center;
        padding: 16px 20px;
        border-bottom: 1px solid #eee;
    }
    .panel-header h3 { margin: 0; font-size: 16px; }
    .close-btn { background: none; border: none; font-size: 18px; cursor: pointer; color: #888; }

    .panel-controls { padding: 12px 20px; border-bottom: 1px solid #eee; font-size: 13px; }
    .hidden-count { color: #999; margin-left: 4px; }

    .status { padding: 20px; text-align: center; color: #666; }
    .status.error { color: #991b1b; }
    .status.warning { color: #92400e; background: #fef3c7; margin: 12px 20px; border-radius: 4px; }

    .timeline-list { overflow-y: auto; padding: 8px 20px 20px; }
    .timeline-entry {
        display: grid;
        grid-template-columns: 160px 90px 110px 1fr;
        gap: 12px;
        padding: 8px 0;
        border-bottom: 1px solid #f0f0f0;
        font-size: 12px;
        align-items: start;
    }
    .entry-time { color: #555; white-space: nowrap; }
    .entry-run { font-family: monospace; color: #555; }
    .entry-summary { font-weight: 500; }
    .entry-detail { grid-column: 1 / -1; margin-top: 4px; }

    .badge {
        display: inline-block;
        padding: 2px 6px;
        border-radius: 999px;
        font-size: 10px;
        font-weight: bold;
        width: fit-content;
    }
    .event { background: #e0e7ff; color: #3730a3; }
    .attempt { background: #dbeafe; color: #1e40af; }
    .pii { background: #fce7f3; color: #9f1239; }
    .alert { background: #fee2e2; color: #991b1b; }
    .annotation { background: #fef3c7; color: #92400e; }

    .hidden-count { color: #999; margin-left: 4px; }
    .gap-note { color: #999; font-size: 11px; margin-left: 16px; }
    .entry-worker, .entry-attempt { font-weight: normal; color: #888; font-size: 11px; }
</style>