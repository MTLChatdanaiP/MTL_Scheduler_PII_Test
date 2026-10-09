// Batch 7 (RFC-010 §22): the browser no longer decides health. The backend sends `health` for workers and queues; these numbers are
// ONLY the fallback for an older backend that sends none, and they live here and nowhere else (a lint test enforces it).
import { serverHealthToStatus, type WorkerStatus } from "./componentView";

export const DEGRADED_HEARTBEAT_SECONDS = 60;
export const OFFLINE_HEARTBEAT_SECONDS = 300;
export const DEGRADED_PENDING_THRESHOLD = 20; // matches models.QueueDegradedPendingThreshold on the server

interface WorkerLike {
    LastHeartbeat: string;
    health?: string;
}

export function workerStatusOf(w: WorkerLike, nowMs: number = Date.now()): WorkerStatus {
    const fromServer = serverHealthToStatus(w.health);
    if (fromServer) return fromServer;
    const age = (nowMs - new Date(w.LastHeartbeat).getTime()) / 1000;
    if (age > OFFLINE_HEARTBEAT_SECONDS) return "offline";
    if (age > DEGRADED_HEARTBEAT_SECONDS) return "degraded";
    return "online";
}

interface QueueLike {
    PendingCount: number;
    health?: string;
}

export function queueDegraded(q: QueueLike): boolean {
    if (q.health === "DEGRADED") return true;
    if (q.health === "HEALTHY") return false;
    return q.PendingCount > DEGRADED_PENDING_THRESHOLD;
}
