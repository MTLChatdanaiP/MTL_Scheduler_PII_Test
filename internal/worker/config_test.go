package worker

// Settings must be read when used. These tests set the environment AFTER the process has started,
// which is exactly what loading .env inside main() does.

import (
	"testing"
	"time"
)

func TestMaxConcurrency_ReadsTheEnvironmentWhenUsed(t *testing.T) {
	t.Setenv("MAX_CONCURRENCY", "7")
	if got := maxConcurrency(); got != 7 {
		t.Fatalf("MAX_CONCURRENCY=7 set after startup gave %d: the value was read too early", got)
	}
}

func TestMaxConcurrency_BadValuesFallBackToOne(t *testing.T) {
	// 0 would make the worker refuse to claim anything, so it is refused too
	for _, v := range []string{"", "abc", "0", "-3", "1.5"} {
		t.Setenv("MAX_CONCURRENCY", v)
		if got := maxConcurrency(); got != 1 {
			t.Errorf("MAX_CONCURRENCY=%q gave %d, want 1", v, got)
		}
	}
}

func TestRetentionWindowHours_ReadsTheEnvironmentWhenUsed(t *testing.T) {
	t.Setenv("PII_RETENTION_HOURS", "12")
	if got := retentionWindowHours(); got != 12 {
		t.Fatalf("PII_RETENTION_HOURS=12 gave %d (this was running as 24 before)", got)
	}
}

func TestRetentionWindowHours_BadValuesFallBackToTheDefault(t *testing.T) {
	for _, v := range []string{"", "abc", "0", "-1"} {
		t.Setenv("PII_RETENTION_HOURS", v)
		if got := retentionWindowHours(); got != 24 {
			t.Errorf("PII_RETENTION_HOURS=%q gave %d, want 24", v, got)
		}
	}
}

func TestRetentionSweepEvery_ReadsTheEnvironmentWhenUsed(t *testing.T) {
	t.Setenv("RETENTION_SWEEP_INTERVAL_HOURS", "7")
	if got := retentionSweepEvery(); got != 7*time.Hour {
		t.Fatalf("RETENTION_SWEEP_INTERVAL_HOURS=7 gave %v (this was running as 1h before)", got)
	}
}

func TestRetentionSweepEvery_BadValuesFallBackToOneHour(t *testing.T) {
	for _, v := range []string{"", "abc", "0", "-2"} {
		t.Setenv("RETENTION_SWEEP_INTERVAL_HOURS", v)
		if got := retentionSweepEvery(); got != time.Hour {
			t.Errorf("RETENTION_SWEEP_INTERVAL_HOURS=%q gave %v, want 1h", v, got)
		}
	}
}
