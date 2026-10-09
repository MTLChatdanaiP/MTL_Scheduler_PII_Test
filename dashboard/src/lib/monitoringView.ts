// Batch 7 (RFC-010 §22): the three lags an operator wants next to the Overview numbers -- is events getting IN, is the projection
// keeping UP, is the live stream getting OUT -- worked out from the one /monitoring/health response that already carries them.
import type { MonitoringHealthResponse } from "./api";

export interface LagItem {
    label: string;
    text: string; // "2s", "idle", "no data"
    level: "ok" | "slow" | "unknown";
}

export interface LivePipelineLag {
    last_delivery_lag_ms?: number;
    last_publish_lag_ms?: number;
}

const SLOW_SECONDS = 60; // the same "fresh" window the Components page uses for a subsystem

export function formatLag(seconds: number): string {
    if (seconds < 1) return "<1s";
    if (seconds < 120) return `${Math.round(seconds)}s`;
    if (seconds < 7200) return `${Math.round(seconds / 60)}m`;
    return `${Math.round(seconds / 3600)}h`;
}

function subsystemLag(res: MonitoringHealthResponse, name: string): LagItem {
    const s = (res.subsystems ?? []).find((x) => x.subsystem === name);
    if (!s || !s.available || s.lag_seconds === undefined || s.lag_seconds === null) {
        return { label: name, text: "no data", level: "unknown" };
    }
    return { label: name, text: formatLag(s.lag_seconds), level: s.lag_seconds > SLOW_SECONDS ? "slow" : "ok" };
}

export function pipelineLag(res: MonitoringHealthResponse | null | undefined): LagItem[] {
    if (!res) return [];
    const live = (res as MonitoringHealthResponse & { live_pipeline?: LivePipelineLag }).live_pipeline;
    const stream: LagItem =
        live && typeof live.last_delivery_lag_ms === "number"
            ? {
                  label: "Live stream",
                  text: formatLag(live.last_delivery_lag_ms / 1000),
                  level: live.last_delivery_lag_ms / 1000 > SLOW_SECONDS ? "slow" : "ok",
              }
            : { label: "Live stream", text: "no data", level: "unknown" };
    return [subsystemLag(res, "Event Ingestion"), subsystemLag(res, "Projection"), stream];
}
