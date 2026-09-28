<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { autoRefreshEnabled } from "../lib/stores";
    import { fetchSnapshotForLiveHandoff, ApiError } from "../lib/api";
    import { connectLive, type LiveEvent, type LiveState } from "../lib/liveClient";
    import { CATEGORIES, categoryOf, filterEvents, countByCategory, connectionBadge, type Category } from "../lib/activity";
    import ActivityGraph from "../lib/ActivityGraph.svelte";
    import DataStateBanner from "../lib/DataStateBanner.svelte";

    // Height of the List view in px. Overview uses the default; the full-page
    // Live Activity page passes a taller value.
    export let listHeight = 320;

    const MAX_KEPT = 1000; // events held in memory (feeds the graph and the filters)
    const MAX_LIST = 50; // rows actually rendered
    // Start this many ids behind the snapshot so the feed and graph have history on load.
    // Keep it under the server's replay cap (500, minus the client's 5-id overlap) or the
    // server answers "resync" instead of replaying.
    const PREFILL_IDS = 400;
    const SNAPSHOT_RETRY_MS = 5000;

    let all: LiveEvent[] = []; // newest first
    let liveState: LiveState = "CONNECTING";
    let now = Date.now();
    // Last time ANY bytes arrived from the server (events or keepalive pings).
    let lastConfirmedAt: number | null = null;
    let clock: ReturnType<typeof setInterval> | undefined;

    // Which view is showing. Filters and the live connection are shared by both.
    let view: "list" | "graph" = "list";

    let show: Record<Category, boolean> = { task: true, alert: true, pii: true, other: true };
    let subject = "";

    let stopLive: (() => void) | null = null;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let want = false;
    let starting = false;
    let mounted = false;

    async function start() {
        if (stopLive || starting) return;
        want = true;
        starting = true;
        liveState = "CONNECTING";
        lastConfirmedAt = null;

        try {
            // RFC-010 §16: snapshot first, then connect from its watermark.
            const handoff = await fetchSnapshotForLiveHandoff();
            if (!want) return;

            all = []; // the replay refills it; clearing avoids duplicates on re-enable

            stopLive = connectLive(
                { ...handoff, watermark: Math.max(0, handoff.watermark - PREFILL_IDS) },
                {
                    path: "/live/activity",
                    onEvent: (e) => {
                        // a keyed {#each} crashes on a duplicate key, so never allow one
                        if (all.some((x) => x.id === e.id)) return;
                        all = [e, ...all].slice(0, MAX_KEPT);
                    },
                    onState: (s) => { liveState = s; },
                    onConfirm: (at) => { lastConfirmedAt = at; },
                    onResync: async () => {
                        all = [];
                        return fetchSnapshotForLiveHandoff();
                    },
                },
            );
        } catch (e) {
            if (e instanceof ApiError && (e.status === 401 || e.status === 403)) {
                liveState = "FORBIDDEN";
            } else if (want) {
                liveState = "RECONNECTING";
                retryTimer = setTimeout(start, SNAPSHOT_RETRY_MS);
            }
        } finally {
            starting = false;
        }
    }

    function stop() {
        want = false;
        clearTimeout(retryTimer);
        stopLive?.();
        stopLive = null;
    }

    // The GlobalBar "Auto-refresh" toggle pauses/resumes the live feed too.
    $: if (mounted) {
        if ($autoRefreshEnabled) start();
        else stop();
    }

    onMount(() => {
        mounted = true;
        clock = setInterval(() => { now = Date.now(); }, 1000); // keeps the graph sliding when quiet
    });
    onDestroy(() => {
        clearInterval(clock);
        stop();
    });

    // Explicit arguments so Svelte re-runs these when any input changes.
    $: filtered = filterEvents(all, show, subject);
    $: visible = filtered.slice(0, MAX_LIST);
    $: counts = countByCategory(all);
    $: anyFilter = subject.trim() !== "" || CATEGORIES.some((c) => !show[c]);

    function clearFilters() {
        show = { task: true, alert: true, pii: true, other: true };
        subject = "";
    }

    $: paused = !$autoRefreshEnabled;
    // RFC-010 §18: the badge says whether the operator is currently seeing updates.
    $: badge = connectionBadge({ paused, state: liveState, lastConfirmedAt, now });
    $: unsure = badge.state === "STALE" || badge.state === "DEGRADED";

    function shortId(id: string): string {
        return id.length > 12 ? `${id.slice(0, 8)}…` : id;
    }
</script>

<div class="header">
    <h4>Live Activity</h4>
    <span class="badge {badge.state.toLowerCase()}">{badge.label}</span>

    <div class="views" role="group" aria-label="View">
        <button class:active={view === "list"} aria-pressed={view === "list"} on:click={() => (view = "list")}>
            List
        </button>
        <button class:active={view === "graph"} aria-pressed={view === "graph"} on:click={() => (view = "graph")}>
            Graph
        </button>
    </div>
</div>

{#if unsure}
    <p class="notice">
        Showing the last confirmed events. New activity may be missing until the connection recovers.
    </p>
{/if}

{#if liveState === "FORBIDDEN" && !paused}
    <DataStateBanner state="FORBIDDEN" />
{:else}
    <div class="filters">
        {#each CATEGORIES as c}
            <button
                class="chip {c}"
                class:off={!show[c]}
                aria-pressed={show[c]}
                on:click={() => (show[c] = !show[c])}
            >
                {c} <span class="n">{counts[c]}</span>
            </button>
        {/each}

        <input type="text" placeholder="Filter by run / subject id" bind:value={subject} />

        {#if anyFilter}
            <button class="clear" on:click={clearFilters}>Clear filters</button>
        {/if}
    </div>

    {#if view === "graph"}
        <ActivityGraph events={filtered} {now} />
    {:else}
        <div class="feed" style="max-height: {listHeight}px">
            {#each visible as e (e.id)}
                <div class="item">
                    <span class="time">{new Date(e.at).toLocaleTimeString()}</span>
                    <span class="type {categoryOf(e.type)}">{e.type}</span>
                    <span class="subject" title={e.subject}>{shortId(e.subject)}</span>
                </div>
            {/each}

            {#if all.length === 0}
                <p class="empty">
                    {paused ? "Live feed paused." : liveState === "LIVE" ? "Connected. Waiting for activity…" : "Connecting…"}
                </p>
            {:else if filtered.length === 0}
                <p class="empty">No events match the current filters.</p>
            {/if}
        </div>
    {/if}
{/if}

<style>
    .header { display: flex; align-items: center; gap: 12px; margin: 20px 0 8px; }
    h4 { font-size: 13px; color: #555; margin: 0; }

    .badge { font-size: 11px; font-weight: bold; padding: 2px 8px; border-radius: 999px; background: #eee; color: #555; }
    .badge.live { background: #dcfce7; color: #166534; }
    .badge.reconnecting { background: #fef3c7; color: #92400e; }
    .badge.forbidden { background: #fee2e2; color: #991b1b; }
    .badge.paused { background: #e5e7eb; color: #6b7280; }
    .badge.degraded { background: #fef3c7; color: #92400e; }
    .badge.stale { background: #fee2e2; color: #991b1b; }
    .badge.resyncing { background: #e0e7ff; color: #3730a3; }

    .notice { font-size: 12px; color: #92400e; background: #fef3c7; border: 1px solid #fcd34d; border-radius: 6px; padding: 6px 10px; margin: 0 0 10px; }

    .views { margin-left: auto; display: inline-flex; border: 1px solid #ccc; border-radius: 6px; overflow: hidden; }
    .views button { font-size: 12px; padding: 4px 12px; border: none; background: white; cursor: pointer; color: #555; }
    .views button + button { border-left: 1px solid #ccc; }
    .views button.active { background: #dbeafe; color: #1e40af; font-weight: bold; }

    .filters { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; margin-bottom: 10px; }
    .chip { font-size: 11px; font-weight: bold; padding: 3px 10px; border-radius: 999px; border: 1px solid transparent; cursor: pointer; background: #eee; color: #555; }
    .chip .n { font-weight: normal; opacity: 0.75; margin-left: 2px; }
    .chip.task { background: #dbeafe; color: #1e40af; }
    .chip.alert { background: #fee2e2; color: #991b1b; }
    .chip.pii { background: #dcfce7; color: #166534; }
    .chip.off { opacity: 0.45; text-decoration: line-through; }
    .filters input { padding: 4px 8px; border: 1px solid #ccc; border-radius: 4px; font-size: 12px; min-width: 200px; }
    .clear { font-size: 11px; padding: 3px 10px; border: 1px solid #ccc; border-radius: 4px; background: white; cursor: pointer; }

    .feed { border: 1px solid #ddd; border-radius: 8px; overflow-y: auto; }
    .item { display: flex; gap: 12px; align-items: center; padding: 8px 16px; border-top: 1px solid #eee; font-size: 12px; }
    .item:first-child { border-top: none; }
    .time { color: #888; min-width: 76px; }
    .subject { font-family: monospace; color: #666; }
    .empty { padding: 16px; text-align: center; color: #999; font-size: 13px; margin: 0; }

    .type { padding: 2px 8px; border-radius: 999px; font-size: 11px; font-weight: bold; background: #eee; }
    .type.task { background: #dbeafe; color: #1e40af; }
    .type.alert { background: #fee2e2; color: #991b1b; }
    .type.pii { background: #dcfce7; color: #166534; }
</style>