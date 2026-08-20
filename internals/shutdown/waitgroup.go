package shutdown

import (
	"context"
	"sync"
)

// WaitGroup blocks until wg.Wait() returns, or ctx is done — whichever
// happens first. Plain wg.Wait() has no timeout of its own and cannot be
// cancelled, so this is the standard way to give it one: run the real wait
// in a separate goroutine, and race it against ctx.Done().
func WaitGroup(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
