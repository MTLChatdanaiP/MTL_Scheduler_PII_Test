// rebuildprojections repairs the run projection from the event log.
//
//	go run ./cmd/rebuildprojections -check    report what is wrong, change nothing (exits 1 if anything is wrong)
//	go run ./cmd/rebuildprojections           repair it
//
// The projection is DERIVED data (RFC-005 §4), so it can always be rebuilt by replaying events through the same
// reducer the live writer uses. Before RFC-005 §7 was fixed the writer blanked a run's status on any event it did not
// recognise and wrote a row for every event subject (system, worker ids, queue names), so existing rows are wrong; this
// repairs them. It is idempotent: run it as often as you like. It prints counts only.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/joho/godotenv"

	"MTL_Scheduler_PII_Test/internal/database"
	"MTL_Scheduler_PII_Test/internal/events"
)

func main() {
	check := flag.Bool("check", false, "report only, change nothing")
	flag.Parse()

	godotenv.Load()
	database.ConnectDatabase()

	report, err := events.RebuildRunProjections(context.Background(), !*check)
	if err != nil {
		fmt.Fprintln(os.Stderr, "rebuild failed:", err)
		os.Exit(1)
	}

	// RFC-005 §7 / RFC-002 §14: the schedule occurrence ledger and summaries, from the schedule events and (for runs that
	// pre-date those events) from the tasks table
	sched, err := events.RebuildScheduleMonitoring(context.Background(), !*check)
	if err != nil {
		fmt.Fprintln(os.Stderr, "schedule rebuild failed:", err)
		os.Exit(1)
	}

	mode := "REBUILT"
	if *check {
		mode = "CHECK ONLY, nothing was changed"
	}
	fmt.Println("mode:                                  ", mode)
	fmt.Println("runs examined:                         ", report.Examined)
	fmt.Println("runs with no projection:               ", report.Missing)
	fmt.Println("projections that disagreed with events:", report.Wrong)
	fmt.Println("rows that belong to no run (junk):     ", report.Junk)
	fmt.Println()
	fmt.Println("schedule occurrences known:            ", sched.Occurrences)
	fmt.Println("occurrences not in the ledger:         ", sched.Missing)
	fmt.Println("ledger rows that were wrong:           ", sched.Wrong)
	fmt.Println("schedule summaries that were wrong:    ", sched.Schedules)

	if *check && (report.Missing+report.Wrong > 0 || report.Junk > 0 || sched.Missing+sched.Wrong+sched.Schedules > 0) {
		fmt.Println("\nRun again without -check to repair.")
		os.Exit(1)
	}
}
