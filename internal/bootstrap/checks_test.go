package bootstrap

import (
	"strings"
	"testing"
)

const goodKey = "0123456789abcdef0123456789abcdef"

func setGood(t *testing.T) {
	t.Helper()
	t.Setenv("PII_FINGERPRINT_KEY", goodKey)
	t.Setenv("PII_ENCR_KEY", goodKey)
	for _, n := range requiredEnv {
		t.Setenv(n, "x")
	}
}

func TestStartupChecks_AcceptsGoodSettings(t *testing.T) {
	setGood(t)
	if err := StartupChecks(); err != nil {
		t.Fatalf("good settings were refused: %v", err)
	}
}

func TestStartupChecks_RejectsAShortFingerprintKey(t *testing.T) {
	setGood(t)
	t.Setenv("PII_FINGERPRINT_KEY", "only14bytes!!!") // the real key in this project was 14 bytes
	err := StartupChecks()
	if err == nil || !strings.Contains(err.Error(), "PII_FINGERPRINT_KEY") || strings.Contains(err.Error(), "PII_ENCR_KEY") {
		t.Fatalf("expected an error naming only PII_FINGERPRINT_KEY, got %v", err)
	}
}

func TestStartupChecks_RejectsAShortEncryptionKey(t *testing.T) {
	setGood(t)
	t.Setenv("PII_ENCR_KEY", "short")
	if err := StartupChecks(); err == nil || !strings.Contains(err.Error(), "PII_ENCR_KEY") {
		t.Fatalf("got %v", err)
	}
}

func TestStartupChecks_RejectsMissingKeysAndNamesBoth(t *testing.T) {
	setGood(t)
	t.Setenv("PII_FINGERPRINT_KEY", "")
	t.Setenv("PII_ENCR_KEY", "")
	err := StartupChecks()
	if err == nil || !strings.Contains(err.Error(), "PII_FINGERPRINT_KEY") || !strings.Contains(err.Error(), "PII_ENCR_KEY") {
		t.Fatalf("got %v", err)
	}
}

func TestStartupChecks_RejectsMissingDatabaseSettings(t *testing.T) {
	setGood(t)
	t.Setenv("DB_HOST", "")
	t.Setenv("DB_NAME", "")
	err := StartupChecks()
	if err == nil || !strings.Contains(err.Error(), "DB_HOST") || !strings.Contains(err.Error(), "DB_NAME") || strings.Contains(err.Error(), "DB_USER") {
		t.Fatalf("got %v", err)
	}
}

func TestStartupChecks_TheErrorNeverContainsAKeyValue(t *testing.T) {
	setGood(t)
	secret := "SuperSecretValue"
	t.Setenv("PII_FINGERPRINT_KEY", secret) // 16 bytes: good
	t.Setenv("PII_ENCR_KEY", "ShortSecret12")
	err := StartupChecks()
	if err == nil {
		t.Fatal("expected an error for the short key")
	}
	if strings.Contains(err.Error(), "ShortSecret12") || strings.Contains(err.Error(), secret) {
		t.Fatalf("the error leaks a key: %v", err)
	}
	if !strings.Contains(err.Error(), "got 13") {
		t.Fatalf("it should report the LENGTH: %v", err)
	}
}

// the keys are read when called, not when the package was loaded
func TestStartupChecks_ReadsTheEnvironmentWhenCalled(t *testing.T) {
	setGood(t)
	if err := StartupChecks(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PII_ENCR_KEY", "x")
	if err := StartupChecks(); err == nil {
		t.Fatal("a change to the environment after the first call must be seen")
	}
}
