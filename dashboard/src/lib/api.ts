const BASE_URL = "http://localhost:8080";
const API_KEY = "dev-local-key-changeme";

// RFC-009 §23: every request that goes through this file is timed and
// counted here, keyed by endpoint PATTERN (see telemetry.ts), never by the
// literal called URL.
import { recordApiCall } from "./telemetry";

// ---------------------------------------------------------------- Alert

export interface Alert {
    alert_id: string;
    alert_type: string;
    severity: string;
    status: string;
    opened_at: string;
    acknowledged_at: string | null;   // may be absent/null if never acked
    acknowledged_by: string | null;
    resolved_at: string | null;
    subject_type: string;
    subject_id: string;
    rule_id: string;
    rule_version: number;
    evidence: string;                  // JSON-encoded string, parse before display
    summary: string;
}
 
export interface AlertsResponse {
    alerts: Alert[];
    page: Page;
    freshness: Freshness;
    live: Live;
}

export function getAlerts(params: string = "", signal?: AbortSignal): Promise<AlertsResponse> {
    return apiFetch(`/alerts${params}`, { signal });
}

export function acknowledgeAlert(alertId: string): Promise<{ alert_id: string; status: string; acknowledged_by: string }> {
    return apiFetch(`/alerts/${alertId}/acknowledge`, { method: "POST" })
}

// ---------------------------------------------------------------- Runs

export interface RunListItem {
    // Batch 7 (RFC-001 §5): the RFC's name for this run's state (SCHEDULED, QUEUED, RUNNING, SUCCEEDED, FAILED, TIMED_OUT, DEAD, UNKNOWN).
    // Optional: an older backend does not send it.
    rfc_state?: string;
    // gorm.Model fields, also untagged, also PascalCase
    ID: number;
    CreatedAt: string;
    UpdatedAt: string;
    DeletedAt: string | null;

    // models.Task, embedded with no json tag — flattened, PascalCase.
    JobId: string;
    TaskName: string;
    TaskType: string;
    Status: string;
    FinishedAt: string;
    RunAt: string;
    ExpectedAt: string;
    ExecutionChainId: string;
    ParentRunId: string;
    RetryIndex: number;
    ScheduleId: string;
    ScanStatus: string;
    Queue: string;

    // this one DOES have a tag, per your earlier Debug JSON — confirm it's
    // not just coincidentally lowercase
    source_run_id: string;

    current_status: string;
    queued_at: string;
    started_at: string;
    completed_at: string;
    recovery_started: boolean;
    pii_finding_count: number;
    was_reclaimed: boolean;
    last_event_at: string;

    // models.Task fields added in Batches 2 and 3 (flattened and PascalCase like the rest of models.Task). Optional: an older
    // backend does not send them.
    TraceID?: string;
    ScheduleOccurrenceId?: string;
    PublishedAt?: string | null;

    // RFC-005 §7 Run Projection fields added in Batch 4. Optional for the same reason.
    latest_attempt_id?: string;
    latest_attempt_status?: string;
    latest_worker_id?: string;
    attempt_count?: number;
    active_annotation_count?: number;
    open_alert_count?: number;
    contradicted?: boolean;
    contradiction_note?: string;

    attempts: unknown[];

    AttemptCount: number;
    LatestWorker: string;
    FailureCategory: string;
    Duration: number;

    pii_findings: PIIFinding[];
    active_alert_count: number;
    schedule?: ScheduleDefinition;
    annotations?: MonitoringAnnotationItem[];
}

export interface MonitoringAnnotationItem {
    AnnotationID: string;
    Type: string;
    SubjectType: string;
    SubjectID: string;
    DerivedAt: string;
    Evidence: string;
    ResolvedAt: string | null;
}

export interface RunsListResponse {
    runs: RunListItem[];
    page: Page;
    freshness: Freshness;
    live: Live;
}

export interface RunDetailResponse {
    run: RunListItem;
    freshness: Freshness;
    live: Live;
    payload_size_bytes: number;
}

export function getRuns(params: string = "", signal?: AbortSignal): Promise<RunsListResponse> {
    return apiFetch(`/runs${params}`, { signal });
}

export function getRunDetail(runId: string): Promise<RunDetailResponse> {
    return apiFetch(`/runs/${runId}`);
}

// ---------------------------------------------------------------- Timelines

export interface TimelineEntry {
    occurred_at: string;
    event_type: string;
    run_id: string;
    source: "EVENT" | "ATTEMPT" | "PII" | "ALERT" | "ANNOTATION";
    detail?: Record<string, unknown>;
}

export interface TimelineResponse {
    chain_id: string;
    entries: TimelineEntry[];
    count: number;
    truncated: boolean;
}

export function getChainTimeline(chainId: string): Promise<TimelineResponse> {
    return apiFetch(`/execution-chains/${chainId}/timeline`);
}

// ---------------------------------------------------------------- Queues

export interface QueueHealth {
    ID: number;
    CreatedAt: string;
    UpdatedAt: string;
    DeletedAt: string | null;
    QueueName: string;
    StreamLength: number;
    PendingCount: number;
    OldestPendingAgeSeconds: number;
    ConsumerCount: number;
    SampledAt: string;
    // runs that finished (completed or failed) per minute over the last five minutes. Optional for an older backend.
    ThroughputPerMinute?: number;
    // Batch 7: the server's own verdict (same threshold as the QUEUE_BACKLOG alert). Optional for an older backend.
    health?: string; // HEALTHY | DEGRADED
    health_reason?: string;
}
 
export interface QueuesListResponse {
    queues: QueueHealth[];
    freshness: Freshness;
    live: Live;
}
 
export interface QueueDetailResponse {
    current: QueueHealth;
    history: QueueHealth[];
    freshness: Freshness;
    live: Live;
}
 
export function getQueues(): Promise<QueuesListResponse> {
    return apiFetch(`/queues`);
}
 
export function getQueueDetail(queueName: string): Promise<QueueDetailResponse> {
    return apiFetch(`/queues/${encodeURIComponent(queueName)}`);
}

// ---------------------------------------------------------------- Workers

export interface WorkerListItem {
    WorkerId: string;
    InstanceId: string;
    Hostname: string;
    StartedAt: string;
    ConfiguredCapacity: number;
    LastHeartbeat: string;
    RunningAttempts: number;
    Capacity: number;
    // RFC-005 §7 Worker Projection, derived by the backend. Optional: an older backend does not send them.
    health?: string; // HEALTHY | DEGRADED | OFFLINE | UNKNOWN
    health_reason?: string;
    build_revision?: string;
}

export interface WorkerDetailItem extends WorkerListItem {
    active_attempts: unknown[];
    recent_completions: unknown[];
    recent_failures: unknown[];
    alerts: Alert[];
    active_alert_count: number;
}
 
export interface WorkersListResponse {
    workers: WorkerListItem[];
    freshness: Freshness;
    live: Live;
}
 
export interface WorkerDetailResponse {
    worker: WorkerDetailItem;
    freshness: Freshness;
    live: Live;
}
 
export function getWorkers(): Promise<WorkersListResponse> {
    return apiFetch(`/workers`);
}
 
export function getWorkerDetail(workerId: string): Promise<WorkerDetailResponse> {
    return apiFetch(`/workers/${encodeURIComponent(workerId)}`);
}

// ---------------------------------------------------------------- Schedules

export interface ScheduleDefinition {
    ScheduleId: string;
    NextRunAt: string;
    Enabled: boolean;
}
 
// tagged/snake_case, per the Go struct given directly
export interface ScheduleRunItem {
    run_id: string;
    expected_at: string;
    created_at: string;
    started_at: string | null;
    status: string;
    creation_drift_seconds: number;
    start_drift_seconds: number | null;
}
 
export interface MonitoringAnnotationItem {
    AnnotationID: string;
    Type: string;
    SubjectType: string;
    SubjectID: string;
    DerivedAt: string;
    Evidence: string;
    ResolvedAt: string | null;
}
 
export interface SchedulesListResponse {
    schedules: ScheduleDefinition[];
    page: Page;
    freshness: Freshness;
    live: Live;
}
 
// RFC-002 §14 / RFC-005 §7: one expected occurrence of a schedule. A SKIPPED one has no run and used to exist nowhere.
export interface ScheduleOccurrence {
    schedule_id: string;
    occurrence_id: string;
    expected_at: string;
    outcome: string; // DUE | CREATED | SKIPPED
    run_id: string;
    creation_lateness_seconds: number | null;
    start_lateness_seconds: number | null;
    recorded_at: string;
    last_event_at: string;
}

export interface ScheduleProjection {
    schedule_id: string;
    last_expected_at: string;
    last_occurrence_id: string;
    last_run_id: string;
    last_creation_lateness_seconds: number | null;
    last_start_lateness_seconds: number | null;
    occurrences_created: number;
    occurrences_skipped: number;
    last_skipped_at: string | null;
    last_event_at: string;
}

export interface ScheduleDetailResponse {
    schedule: ScheduleDefinition;
    recent_runs: ScheduleRunItem[];
    annotations: MonitoringAnnotationItem[];
    next_expected_at: string | null;
    // Optional: an older backend does not send them.
    projection?: ScheduleProjection | null;
    occurrences?: ScheduleOccurrence[];
    freshness: Freshness;
    live: Live;
}
 
export function getSchedules(params: string = ""): Promise<SchedulesListResponse> {
    return apiFetch(`/schedules${params}`);
}
 
export function getScheduleDetail(scheduleId: string): Promise<ScheduleDetailResponse> {
    return apiFetch(`/schedules/${encodeURIComponent(scheduleId)}`);
}

// ---------------------------------------------------------------- PII findings

export interface PIIFinding {
    type: string;
    detector_id: string;
    confidence: number;
    source: string;
    index: number;
    policy_action: string;
}

export interface PIIFindingItem {
    type: string;
    detector_id: string;
    confidence: number;
    source: string;
    index: number;
    policy_action: string;
 
    run_id: string;
    field_path?: string;
    rule_id: string;
    mask_strategy?: string;
    policy_name: string;
    policy_version: number;
    policy_checksum: string;
    detected_at: string;
}

export interface PIIListResponse {
    piis: PIIFindingItem[];   // confirmed "piis", not "findings"
    page: Page;
    freshness: Freshness;
    live: Live;
}
 
export function getPIIFindings(params: string = "", signal?: AbortSignal): Promise<PIIListResponse> {
    return apiFetch(`/pii/findings${params}`, { signal });
}

// ---------------------------------------------------------------- PII Policy Dashboard (RFC-006 §31)

export interface PolicyActivationItem {
    PolicyName: string;
    PolicyVersion: number;
    Result: string;
    FailureReason: string;
    ActivatedAt: string;
    Trigger: string;
}

export interface PolicyHistoryResponse {
    activations: PolicyActivationItem[];
}

export function getPolicyHistory(): Promise<PolicyHistoryResponse> {
    return apiFetch(`/pii/policy/history`);
}

export interface PolicyRuleSummary {
    id: string;
    priority: number;
    sources: string[];
    pii_types: string[];
    action_type: string;
    mask_strategy?: string;
}

export interface PolicyRulesResponse {
    rules: PolicyRuleSummary[];
}

export function getPolicyRules(): Promise<PolicyRulesResponse> {
    return apiFetch(`/pii/policy/rules`);
}

export interface PolicyDetectorSummary {
    id: string;
    pii_type: string;
    type: string;
    enabled: boolean;
    minimum_confidence: number;
}

export interface PolicyDetectorsResponse {
    detectors: PolicyDetectorSummary[];
}

export function getPolicyDetectors(): Promise<PolicyDetectorsResponse> {
    return apiFetch(`/pii/policy/detectors`);
}
 

// ---------------------------------------------------------------- Monitoring health

// ============================================================================
// ADD TO api.ts
// ============================================================================

export interface SubsystemFreshness {
    subsystem: string;
    last_observed_at?: string;
    lag_seconds?: number;
    available: boolean;
    unavailable_reason?: string;
}

export interface MonitoringHealthResponse {
    sweep_status: string;
    sweep_failed_checks: number;
    sweep_sampled_at: string;

    subsystems: SubsystemFreshness[];

    active_policy_name: string;
    active_policy_version: number;
    active_policy_checksum: string;

    last_reload_result: string;
    last_reload_at: string;
    last_reload_failure_reason?: string;

    scanner_drift: string;
    known_gaps: string[];

    freshness: Freshness;
    live: Live;
}

export function getMonitoringHealth(): Promise<MonitoringHealthResponse> {
    return apiFetch(`/monitoring/health`);
}

// ---------------------------------------------------------------- Components (RFC-005 §7 Component Health Projection)

export interface ComponentHealthItem {
    component_type: string;
    component_instance_id: string;
    display_name: string;
    build_revision: string;
    started_at: string;
    last_heartbeat: string | null; // null: it has never reported one
    health: string; // HEALTHY | DEGRADED | OFFLINE | UNKNOWN
    reason: string;
    evidence: Record<string, unknown>;

    // RFC-010 §8/§9 names for the same facts. Optional: an older backend sends none of them.
    component_kind?: string; // API, SCHEDULER, WORKER, MONITORING_INGESTOR, ...
    status?: string; // same value as health, plus UNHEALTHY
    status_reason?: string; // same value as reason
    observed_lag_ms?: number; // set when the verdict is about a lag or an age
    threshold_ms?: number; // the threshold that applied
}

export interface DependencyStatus {
    name: string;
    status: string; // OK | UNAVAILABLE
    detail: string;
}

export interface ComponentsResponse {
    components: ComponentHealthItem[];
    // RFC-010 §8: components that are not a registered process (API, Redis delivery, the monitoring pipeline, the PII scanner, the
    // alert evaluator, the live gateway), judged by the server. Absent on an older backend.
    derived?: ComponentHealthItem[];
    dependencies: DependencyStatus[];
    observed_by: { build_revision: string };
    generated_at: string;
}

export function getComponents(): Promise<ComponentsResponse> {
    return apiFetch(`/components`);
}

// ---------------------------------------------------------------- Active policy + drift (RFC-006 §32)

export interface PolicyDrift {
    drifted: boolean;
    active_checksum: string;
    file_checksum: string;
    file_error?: string; // set when the file could not be read or parsed; never contains the file's contents
}

export interface ActivePolicyResponse {
    name: string;
    version: number;
    checksum: string;
    detector_count: number;
    rule_count: number;
    drift?: PolicyDrift; // optional: an older backend does not send it
}

export function getActivePolicy(): Promise<ActivePolicyResponse> {
    return apiFetch(`/pii/policy/active`);
}

// ---------------------------------------------------------------- Overview

export interface OverviewResponse {
    active_runs: number;
    queued_runs: number;
    open_alerts: number;
    degraded_queues: number;
    offline_workers: number;
    // Batch 7: execution chains that are waiting on or running a retry. Optional for an older backend.
    retrying_chains?: number;
    freshness: Freshness;
    live: Live;
}

export function getOverview(): Promise<OverviewResponse> {
    return apiFetch(`/overview`);
}

export interface SnapshotHandoff {
    overview: OverviewResponse;
    watermark: number;
}

export async function fetchSnapshotForLiveHandoff(): Promise<SnapshotHandoff> {
    const overview = await getOverview();
    return { overview, watermark: overview.live.watermark };
}

// ---------------------------------------------------------------- Watermark/Page

export interface Page {
    limit: number;
    count: number;
    has_more: boolean;
    next_cursor?: string; // set in cursor mode (Alerts, Runs); pass it back as ?cursor=
    offset?: number;
    total?: number;
}

export interface Freshness {
    last_updated_at: string | null;
    lag_seconds: number;
}
 
export interface Live {
    watermark: number;
}

async function apiPost<T>(path: string): Promise<T> {
    const res = await fetch(BASE_URL + path, {
        method: "POST",
        headers: { "X-API-Key": API_KEY },
    });

    if (!res.ok) {
        const body = await res.json().catch(() => ({}));
        throw new Error(body.error || `request failed: ${res.status}`);
    }

    return res.json();
}

async function apiFetch<T>(path: string, options: RequestInit = {}): Promise<T> {
    const start = performance.now();
    let ok = true;
    try {
        const res = await fetch(BASE_URL + path, {
            ...options,
            headers: { "X-API-Key": API_KEY, ...options.headers },
        });

        if (!res.ok) {
            ok = false;
            const body = await res.json().catch(() => ({}));
            throw new ApiError(body.error || `request failed: ${res.status}`, res.status);
        }

        return await res.json();
    } catch (e) {
        ok = false;
        throw e;
    } finally {
        recordApiCall(path, ok, performance.now() - start);
    }
}

export class ApiError extends Error {
    status: number;
    constructor(message: string, status: number) {
        super(message);
        this.status = status;
    }
}

export async function apiStream(path: string, signal: AbortSignal): Promise<Response> {
    // Only the connection-open leg is timed here -- the stream itself is
    // long-lived, and its health (disconnects) is tracked separately via
    // recordLiveState, called from the component that owns the connection.
    const start = performance.now();
    let ok = true;
    try {
        const res = await fetch(BASE_URL + path, {
            headers: { "X-API-Key": API_KEY, Accept: "text/event-stream" },
            signal,
        });

        if (!res.ok || !res.body) {
            ok = false;
            const body = await res.json().catch(() => ({}));
            throw new ApiError(body.error || `request failed: ${res.status}`, res.status);
        }

        return res;
    } catch (e) {
        ok = false;
        throw e;
    } finally {
        recordApiCall(path, ok, performance.now() - start);
    }
}