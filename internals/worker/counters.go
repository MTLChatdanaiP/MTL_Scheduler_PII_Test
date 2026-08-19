package worker

import (
	"log"

	"sync"
	"sync/atomic"
)

// RFC-004 §9 Capacity: "active_attempts" — tracked per-worker (keyed by WorkerId) so individual worker load is distinguishable, not just the platform-wide total
var workerCounters sync.Map

// RFC-004 §5 Worker Registration: counter lifecycle begins alongside worker identity creation (see CreateWorker)
func RegisterWorkerCounter(workerId string) {
	var counter atomic.Int64
	workerCounters.Store(workerId, &counter)
}

// RFC-004 §9 Capacity: "No worker should claim unbounded work without deliberate configuration." — the <0 clamp below is a defensive guard against increment/decrement drift, not a designed RFC behavior
func AddWorkerCounter(workerId string, increment int64) {
	value, ok := workerCounters.Load(workerId)
	if !ok {
		return
	}
	counter := value.(*atomic.Int64)
	counter.Add(increment)

	if counter.Load() < 0 { //in case
		counter.Store(0)
		log.Printf("WARNING: worker counter went below 0 for worker %s", workerId)
	}
}

// RFC-004 §10 Graceful Shutdown: intended cleanup step on worker shutdown — not yet wired to a shutdown signal (§10 remains open)
func DeleteWorkerCounter(workerId string) {
	workerCounters.Delete(workerId)
}
