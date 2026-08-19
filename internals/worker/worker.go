package worker

import (
	"context"
	"fmt"
	"time"

	"MTL_Scheduler_PII_Test/internals/cache"
	"MTL_Scheduler_PII_Test/internals/database"
	"MTL_Scheduler_PII_Test/internals/events"
	"MTL_Scheduler_PII_Test/internals/models"

	"sync/atomic"

	"github.com/redis/go-redis/v9"
)

// RFC-003 §4 Redis Primitive: consumer group name — enables competing-consumer distribution across this worker and the reclaimer
const WorkerGroupA = "WorkerG_A"

// RFC-004 §9 Capacity: "active_attempts" — global total across all workers
var activeAttempts int64

// RFC-004 §7 Execution Protocol: validate envelope -> claim/establish attempt -> emit attempt.started -> execute handler -> record success/failure
// RFC-001 §6 Attempt State Model: CLAIMED -> STARTED -> SUCCEEDED|FAILED, simplified here to Running -> Completed on the Task itself rather than a separate Attempt record
func ProcessTask(JobId string, workerId string) {
	var task models.Task

	db := database.DB
	results := database.DB.Where("job_id = ?", JobId).First(&task)

	if results.Error != nil {
		fmt.Println("Task not found:", JobId)
		return
	}

	atomic.AddInt64(&activeAttempts, 1)
	AddWorkerCounter(workerId, 1)

	// RFC-001 §5 Run State Model: RUNNING
	task.Status = "Running"
	db.Save(&task)
	// RFC-000 §5.3: attempt.started-equivalent event
	events.LogEvent(task.JobId, "task.started", "worker")
	//change note status from "Pending" to "Running"
	fmt.Println("Started:", JobId)

	time.Sleep(30 * time.Second)
	// RFC-001 §5 Run State Model: SUCCEEDED (named "Completed" in this project)
	task.Status = "Completed"
	task.FinishedAt = time.Now()
	db.Save(&task)
	// RFC-000 §5.3: attempt.succeeded-equivalent event
	events.LogEvent(task.JobId, "task.completed", "worker")
	//change note status from "Running" to "Completed"

	atomic.AddInt64(&activeAttempts, -1)
	AddWorkerCounter(workerId, -1)

}

// RFC-003 §6 Delivery Lifecycle: AVAILABLE -> CLAIMED — XReadGroup with Block implements event-driven (not polling) delivery, per RFC-003 §1 "runtime delivery substrate"
func ReadStream(Consumer string, StreamText string, Group string) []redis.XStream {
	ctx := cache.Ctx
	rdb := cache.Client

	streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    Group,
		Consumer: Consumer,
		Streams:  []string{StreamText, ">"},
		Count:    1,
		Block:    5 * time.Second,
	}).Result()

	if err != nil {
		if err == redis.Nil {
			return nil
		}
		fmt.Printf("[%s] XReadGroup error: %v\n", Consumer, err)
		time.Sleep(1 * time.Second)
		return nil
	}

	return streams
}

// RFC-003 §7 Critical Distinction: "message claimed != attempt started != attempt succeeded != message acknowledged." This function performs claim handling, delegates execution to ProcessTask, then acknowledges — keeping those steps sequential and observable
// Shared by both the normal consumer (SetupWorker) and the reclaimer (StartReclaimer) — RFC-004 §4 treats a worker as an independent, identifiable consumer regardless of how it acquired a message
func ProcessStream(Consumer string, StreamText string, Group string, Message redis.XMessage) {

	ctx := cache.Ctx
	rdb := cache.Client
	msg := Message

	fmt.Printf("[%s] Received %s: %v (at %s)\n",
		Consumer, msg.ID, msg.Values, time.Now().Format("15:04:05"))

	taskId, ok := msg.Values["task_id"].(string)
	if !ok {
		fmt.Println("Error A parsing string:", ok)
		return
	}

	ProcessTask(taskId, Consumer)

	fmt.Println("Results received: ", taskId)

	// RFC-003 §8 Acknowledgement Semantics: ack happens only after ProcessTask has durably recorded a terminal state, not before
	if err := rdb.XAck(ctx, StreamText, Group, msg.ID).Err(); err != nil {
		fmt.Printf("[%s] XAck error: %v\n", Consumer, err)
	} else {
		fmt.Printf("[%s] Acked %s\n", Consumer, msg.ID)
	}
}

// RFC-004 §5 Worker Registration / §4 Worker Identity: worker_id — no separate registration record, heartbeat, or capacity reporting implemented yet
// RFC-004 §6 Worker Heartbeat: conceptual payload "worker_id, occurred_at, running_attempts, capacity, version" — capacity/version deferred (see models.WorkerHeartbeat)
// Runs as its own goroutine, independent of the message-processing loop, so a slow/blocked XReadGroup or ProcessTask never delays or skips a heartbeat tick
func SetupWorker(ctx context.Context, worker_id string) {
	fmt.Printf("[%s] Setting Up Worker\n", worker_id)

	workerStruct := CreateWorker(worker_id)
	workerInstId := workerStruct.InstanceId //unused cause we got sidetracked on this lol

	// RFC-004 §6: heartbeat starts immediately after worker identity is registered, before any message processing begins
	go StartHeartbeat(worker_id, workerInstId, ctx)

	fmt.Println(workerInstId)

	bg_ctx := cache.Ctx
	rdb := cache.Client
	group := WorkerGroupA
	TaskStream := cache.TaskStream

	// RFC-003 §4 Redis Primitive: consumer group created with start position "$" (new messages only) — avoids replaying the stream's entire historical backlog on every group (re)creation
	err := rdb.XGroupCreateMkStream(bg_ctx, TaskStream, group, "$").Err()
	if err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		fmt.Printf("[%s] XGroupCreate error: %v\n", worker_id, err)
	}

	for {
		select { // RFC-004 §10 Graceful Shutdown: "stop claiming new jobs... do not acknowledge unfinished work merely to empty the queue." Checked only at the top of the loop, so an in-flight ProcessStream/ProcessTask always finishes before this worker stops claiming new messages.
		case <-ctx.Done():
			return
		default:
		}
		Streams := ReadStream(worker_id, TaskStream, group)
		if Streams != nil {
			for _, s := range Streams {
				for _, msg := range s.Messages {
					ProcessStream(worker_id, TaskStream, group, msg)
				}
			}
		}
	}
}
