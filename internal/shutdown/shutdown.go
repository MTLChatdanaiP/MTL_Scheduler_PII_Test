package shutdown

import (
	"context"
	"fmt"
	"time"
)

// Phase is one named, independently-timed step of an ordered shutdown sequence.
// RFC-004 §10 Graceful Shutdown: collects what was previously scattered
// ctx.Done() checks across worker.go/scheduler.go/reclaimer.go/main.go into
// a single, readable, ordered list.
type Phase struct {
	Name    string
	Timeout time.Duration
	Run     func(ctx context.Context) error
}

// Run executes each phase in order, giving each one its own bounded timeout.
//
// IMPORTANT: ctx here must be a FRESH context (e.g. context.Background()),
// never the already-cancelled signal context from main()'s <-ctx.Done().
// A context.WithTimeout derived from an already-cancelled parent is
// immediately cancelled too — every phase would time out instantly with
// zero actual time granted, silently defeating the whole point of Timeout.
func Run(ctx context.Context, phases []Phase) {
	for _, phase := range phases {
		phaseCtx, cancel := context.WithTimeout(ctx, phase.Timeout)
		start := time.Now().UTC()

		err := phase.Run(phaseCtx)

		cancel()
		elapsed := time.Since(start).Round(time.Millisecond)

		if err != nil {
			fmt.Printf("[shutdown] %-20s FAILED after %s: %v\n", phase.Name, elapsed, err)
		} else {
			fmt.Printf("[shutdown] %-20s done in %s\n", phase.Name, elapsed)
		}
	}
}
