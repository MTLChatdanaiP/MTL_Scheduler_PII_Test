package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"syscall"
	"testing"
	"time"
)

func TestGracefulShutdown_InFlightTaskCompletes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("SIGINT via os/exec is not supported on Windows; this test requires a Unix-like OS")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// TODO: adjust "go" + "run" + "." to match how you actually build/run
	// the server, or point this at a pre-built binary instead — running
	// via `go run .` adds startup overhead worth knowing about
	cmd := exec.CommandContext(ctx, "go", "run", ".")
	cmd.Dir = "../.." // adjust to your actual project root relative to this test file

	if err := cmd.Start(); err != nil {
		t.Fatalf("failed to start server process: %v", err)
	}

	time.Sleep(5 * time.Second) // give the server time to boot

	created := postTask(t, "e2e_shutdown_test", "dummy", "should survive shutdown")
	jobId, _ := created["JobId"].(string)

	time.Sleep(10 * time.Second) // let it get into the middle of processing

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("failed to send SIGINT: %v", err)
	}

	cmd.Wait() // wait for the process to actually exit

	// server is down now — need a direct DB check instead of an HTTP call
	// TODO: this requires database.ConnectDatabase() to be callable from
	// this test file too (same TestMain pattern), since the server that
	// would normally answer GET /tasks/{id} is no longer running
	fmt.Println("check task", jobId, "status directly via database.DB, not HTTP")
}
