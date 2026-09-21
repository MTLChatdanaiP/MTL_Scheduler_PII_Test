<script lang="ts">
    import { onMount, onDestroy } from "svelte";
    import { currentPath, getParam, updateParams } from "./router";
    import {
        timeRange,
        autoRefreshEnabled,
        lastRefreshedAt,
        globalSearchQuery,
        activeFilterCount,
        type TimeRange
    } from "./stores";

    let showAbsolute = false;
    let tick = 0;
    let tickTimer: ReturnType<typeof setInterval>;

    let unsubscribePath: (() => void) | undefined;

    onMount(() => {
        tickTimer = setInterval(() => {
            tick += 1;
        }, 10000);

        // URL -> store
        unsubscribePath = currentPath.subscribe(() => {
            const urlRange = getParam("time_range");

            if (isTimeRange(urlRange) && urlRange !== $timeRange) {
                timeRange.set(urlRange);
            }
        });

        return () => {
            unsubscribePath?.();
            clearInterval(tickTimer);
        };
    });

    function isTimeRange(value: string): value is TimeRange {
        return ["15m", "1h", "6h", "24h", "7d"].includes(value);
    }

    function formatRelative(date: Date): string {
        const seconds = Math.floor(
            (Date.now() - date.getTime()) / 1000
        );

        if (seconds < 60) return `${seconds}s ago`;

        const minutes = Math.floor(seconds / 60);

        if (minutes < 60) return `${minutes}m ago`;

        return `${Math.floor(minutes / 60)}h ago`;
    }

    function formatAbsolute(date: Date): string {
        return date.toLocaleString(undefined, {
            timeZoneName: "short"
        });
    }

    function handleTimeRangeChange() {
        updateParams({
            time_range: $timeRange
        });
    }

    $: relativeLabel = tick >= 0 && $lastRefreshedAt
        ? formatRelative($lastRefreshedAt)
        : "Never updated";
</script>

<div class="global-bar">
    <label>
        Time range:
        <select
            bind:value={$timeRange}
            on:change={handleTimeRangeChange}
        >
            <option value="15m">15m</option>
            <option value="1h">1h</option>
            <option value="6h">6h</option>
            <option value="24h">24h</option>
            <option value="7d">7d</option>
            <option value="custom" disabled>Custom (coming soon)</option>
        </select>
    </label>

    <label>
        <input type="checkbox" bind:checked={$autoRefreshEnabled} />
        Auto-refresh
    </label>

    <span
        class="last-updated"
        role="status"
        on:mouseenter={() => showAbsolute = true}
        on:mouseleave={() => showAbsolute = false}
    >
        {#if $lastRefreshedAt}
            {showAbsolute ? formatAbsolute($lastRefreshedAt) : relativeLabel}
        {:else}
            Never updated
        {/if}
    </span>

    <input
        type="text"
        placeholder="Global search..."
        bind:value={$globalSearchQuery}
    />

    {#if $activeFilterCount > 0}
        <span class="filter-badge">
            {$activeFilterCount} filter{$activeFilterCount > 1 ? "s" : ""} active
        </span>
    {/if}
</div>

<style>
    .global-bar {
        display: flex;
        gap: 16px;
        align-items: center;
        padding: 12px 20px;
        border-bottom: 1px solid #ddd;
        background: #fafafa;
    }
    .last-updated {
        font-size: 13px;
        color: #555;
        cursor: default;
    }
    .filter-badge {
        background: #dbeafe;
        color: #1e40af;
        padding: 4px 10px;
        border-radius: 999px;
        font-size: 12px;
        font-weight: bold;
    }
    input[type="text"] {
        padding: 6px 10px;
        border: 1px solid #ccc;
        border-radius: 4px;
    }
</style>