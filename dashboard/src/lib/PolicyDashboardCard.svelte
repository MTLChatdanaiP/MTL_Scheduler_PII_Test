<script lang="ts">
    import { onMount } from "svelte";
    import { getPolicyHistory, getPolicyRules, getPolicyDetectors, getActivePolicy, type PolicyDrift } from "./api";
    import { driftMessage } from "./policyView";
    import { deriveState, errorMessage } from "./dataState";
    import DataStateBanner from "./DataStateBanner.svelte";

    // RFC-006 §31 Dashboard Requirements. Covers the 3 items that had no
    // view at all: policy revision history, safe rule summaries, and the
    // detector inventory. The other 4 (active policy version/status,
    // finding counts by rule/action/type, scanner policy-version drift) are
    // already shown by GetActivePolicy, PIIHealthCards, and the Monitoring
    // Health page's scanner_drift field respectively.

    import type { PolicyActivationItem, PolicyRuleSummary, PolicyDetectorSummary } from "./api";

    let history: PolicyActivationItem[] = [];
    let drift: PolicyDrift | null = null;
    $: drift_note = driftMessage(drift);
    let rules: PolicyRuleSummary[] = [];
    let detectors: PolicyDetectorSummary[] = [];
    let error: unknown = null;
    let hasLoadedOnce = false;
    let isFetching = false;

    onMount(async () => {
        isFetching = true;
        try {
            const [h, r, d] = await Promise.all([getPolicyHistory(), getPolicyRules(), getPolicyDetectors()]);
            history = h.activations ?? [];
            rules = r.rules ?? [];
            detectors = d.detectors ?? [];

            // RFC-006 §32: is the file on disk still the policy that is active? Best-effort: an older backend has no `drift`.
            try {
                drift = (await getActivePolicy()).drift ?? null;
            } catch {
                drift = null;
            }
            error = null;
        } catch (e) {
            error = e;
        } finally {
            isFetching = false;
            hasLoadedOnce = true;
        }
    });

    $: state = deriveState({ hasLoadedOnce, isFetching, error, isEmpty: history.length === 0 && rules.length === 0 && detectors.length === 0 });
</script>

<h4>PII Policy</h4>

{#if drift_note}
    <p class="drift-note {drift_note.kind}">⚠ {drift_note.text}</p>
{/if}

{#if state === "READY" || state === "REFRESHING"}
    <div class="grid">
        <div class="card">
            <span>Revision History</span>
            <table>
                <thead><tr><th>Version</th><th>Result</th><th>When</th></tr></thead>
                <tbody>
                    {#each history as a (a.ActivatedAt + a.PolicyVersion)}
                        <tr>
                            <td>{a.PolicyVersion}</td>
                            <td class:bad={a.Result !== "SUCCESS"}>{a.Result}{a.FailureReason ? `: ${a.FailureReason}` : ""}</td>
                            <td>{new Date(a.ActivatedAt).toLocaleString()}</td>
                        </tr>
                    {/each}
                </tbody>
            </table>
        </div>

        <div class="card">
            <span>Detector Inventory ({detectors.length})</span>
            <ul>
                {#each detectors as d (d.id)}
                    <li class:disabled={!d.enabled}>{d.id} — {d.pii_type} ({d.type}){!d.enabled ? " [disabled]" : ""}</li>
                {/each}
            </ul>
        </div>

        <div class="card">
            <span>Active Rules ({rules.length})</span>
            <ul>
                {#each rules as r (r.id)}
                    <li>{r.id} — {(r.pii_types ?? []).join(", ") || "any"} → {r.action_type}{r.mask_strategy ? ` (${r.mask_strategy})` : ""}</li>
                {/each}
            </ul>
        </div>
    </div>
{:else}
    <DataStateBanner {state} message={errorMessage(error)} />
{/if}

<style>
    .drift-note { margin: 0 0 12px; padding: 8px 12px; border-radius: 6px; font-size: 12px; background: #fef3c7; color: #92400e; }
    .drift-note.unreadable { background: #f1f5f9; color: #475569; }
    h4 { font-size: 13px; margin: 20px 0 8px; color: #555; }
    .grid { display: grid; grid-template-columns: repeat(3, 1fr); gap: 16px; }
    .card { border: 1px solid #ddd; border-radius: 8px; padding: 10px; }
    .card span { font-size: 11px; color: #666; display: block; margin-bottom: 6px; }
    table { width: 100%; font-size: 11px; border-collapse: collapse; }
    th, td { text-align: left; padding: 2px 4px; }
    .bad { color: #991b1b; }
    ul { list-style: none; margin: 0; padding: 0; font-size: 11px; }
    li { padding: 2px 0; }
    li.disabled { color: #999; text-decoration: line-through; }
</style>