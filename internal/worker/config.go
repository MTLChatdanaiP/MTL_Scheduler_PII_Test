package worker

import "time"

// Settings read when they are USED, not when the program starts.
//
// Go initialises package-level variables before main() runs, so a variable such as
//
//	var MaxConcurrency = getEnvIntOrDefault("MAX_CONCURRENCY", 1)
//
// is evaluated before godotenv has loaded .env. A value that only exists in .env was
// therefore ignored: MAX_CONCURRENCY=4 still ran one task at a time and reported capacity 1,
// and PII_RETENTION_HOURS=12 still ran with 24. (The same bug was fixed for the PII keys.)

// maxConcurrency takes nothing and returns how many tasks one worker may run at once, by
// reading MAX_CONCURRENCY now. Missing, invalid or below 1 gives 1: a value of 0 would make
// the worker refuse to claim anything.
func maxConcurrency() int {
	n := getEnvIntOrDefault("MAX_CONCURRENCY", 1)
	if n < 1 {
		return 1
	}
	return n
}

// retentionWindowHours returns how long monitoring samples are kept, from PII_RETENTION_HOURS
// (default 24, and anything below 1 gives 24).
func retentionWindowHours() int {
	n := getEnvIntOrDefault("PII_RETENTION_HOURS", 24)
	if n < 1 {
		return 24
	}
	return n
}

// retentionSweepEvery returns how often the retention sweep runs, from
// RETENTION_SWEEP_INTERVAL_HOURS (default 1, and anything below 1 gives 1).
func retentionSweepEvery() time.Duration {
	n := getEnvIntOrDefault("RETENTION_SWEEP_INTERVAL_HOURS", 1)
	if n < 1 {
		n = 1
	}
	return time.Duration(n) * time.Hour
}
