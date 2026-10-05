<script lang="ts">
    import { recordBannerShown } from "./telemetry";

    export let state: "INITIAL_LOADING" | "EMPTY" | "ERROR" | "FORBIDDEN";
    export let message: string | null = null;

    // RFC-009 §23: "stale-data banner frequency" / "permission-denied
    // navigation". Tracks the actual TRANSITION into a state, not every
    // re-render while it is showing -- INITIAL_LOADING is excluded since it
    // is expected, ordinary behaviour, not a failure signal.
    let lastRecorded: string | undefined;
    $: if (state && state !== "INITIAL_LOADING" && state !== lastRecorded) {
        recordBannerShown(state);
        lastRecorded = state;
    }
</script>

{#if state === "INITIAL_LOADING"}
    <p class="state-banner loading">⏳ Loading...</p>
{:else if state === "EMPTY"}
    <p class="state-banner empty">— No data to show —</p>
{:else if state === "FORBIDDEN"}
    <p class="state-banner forbidden">🔒 You don't have permission to view this.</p>
{:else if state === "ERROR"}
    <p class="state-banner error">⚠ {message ?? "Something went wrong."}</p>
{/if}

<style>
    .state-banner {
        padding: 16px;
        text-align: center;
        font-size: 13px;
        border-radius: 4px;
    }
    .loading { color: #666; }
    .empty { color: #999; }
    .forbidden { color: #92400e; background: #fef3c7; }
    .error { color: #991b1b; background: #fee2e2; }
</style>