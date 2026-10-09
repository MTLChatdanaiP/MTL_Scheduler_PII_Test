<script lang="ts">
    // RFC-009 §5.2: evidence (a stored fact) and interpretation (something derived from it) must look different. This pill marks the
    // second kind. The text it carries says what the verdict was derived from.
    export let basis: string = "derived from stored facts";
</script>

<span class="interpretation" title={`Interpretation: ${basis}`} aria-label={`Interpretation: ${basis}`}>derived</span>

<style>
    .interpretation { display: inline-block; margin-left: 4px; padding: 0 6px; border: 1px dashed #6b7280; border-radius: 8px; font-size: 10px; font-weight: normal; color: #4b5563; vertical-align: middle; }
</style>
