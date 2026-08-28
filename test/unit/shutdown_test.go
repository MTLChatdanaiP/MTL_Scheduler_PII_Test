package unit_test

import (
	"MTL_Scheduler_PII_Test/internals/shutdown"
	"context"
	"sync"
	"testing"
	"time"
)

func TestWaitGroup_FinishesBeforeTimeout(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) // generous timeout
	defer cancel()
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		time.Sleep(50 * time.Millisecond) // simulates brief real work
	}()

	err := shutdown.WaitGroup(ctx, &wg)
	if err != nil {
		t.Errorf("expected nil error, got %v", err)
	}
}

func TestWaitGroup_ContextTimesOutFirst(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	err := shutdown.WaitGroup(ctx, &wg)
	if err == nil {
		t.Error("expected a timeout error, got nil")
	}
}
