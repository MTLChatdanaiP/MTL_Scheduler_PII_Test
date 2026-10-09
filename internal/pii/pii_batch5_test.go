package pii

// Batch 5 (5A): the JSON rewrite for ANY payload shape, fail-closed behaviour, NFC offsets and Decrypt's length guard.
// Pure: no database. Helper names are prefixed b5 so this file cannot collide with another test file in the package.

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"MTL_Scheduler_PII_Test/internal/models"
)

func init() {
	// the keys are read lazily on first use, and tests in this package do not load .env
	if os.Getenv("PII_FINGERPRINT_KEY") == "" {
		os.Setenv("PII_FINGERPRINT_KEY", "0123456789abcdef0123456789abcdef")
	}
	if os.Getenv("PII_ENCR_KEY") == "" {
		os.Setenv("PII_ENCR_KEY", "fedcba9876543210fedcba9876543210")
	}
}

func b5Policy(emailAction, phoneAction string) models.PIIPolicy {
	var p models.PIIPolicy
	p.Spec.EvaluationMode = "FIRST_MATCH"
	p.Spec.Defaults.Action = "OBSERVE"
	p.Spec.Detectors = []models.DetectorDefinition{
		{ID: "b5-email", PIIType: "Email", Type: "REGEX", Enabled: true, Pattern: `[^\s@"]+@[^\s@"]+\.[^\s@"]+`, MinimumConfidence: 1},
		{ID: "b5-phone", PIIType: "Phone", Type: "REGEX", Enabled: true, Pattern: `\b\d{3}-\d{3}-\d{4}\b`, MinimumConfidence: 1},
	}
	rule := func(id string, prio int, piiType, action string) models.PolicyRule {
		r := models.PolicyRule{ID: id, Priority: prio, Match: models.MatchConditions{PIITypes: []string{piiType}}, Action: models.PolicyAction{Type: action}}
		if action == "MASK" {
			r.Action.Mask = models.MaskConfig{Strategy: "FULL", MaskCharacter: "*"}
		}
		return r
	}
	p.Spec.Rules = []models.PolicyRule{rule("b5-email-rule", 200, "Email", emailAction), rule("b5-phone-rule", 100, "Phone", phoneAction)}
	return p
}

// b5Resolved runs the same steps CreateTask_Direct runs before it rewrites a JSON payload.
func b5Resolved(t *testing.T, payload string, p models.PIIPolicy) []EvaluatedFinding {
	t.Helper()
	evaluated, failed, ok := DetectJSON(payload, p.Spec.Detectors, p, "JOB_PAYLOAD", "T", "q")
	if !ok || len(failed) > 0 {
		t.Fatalf("detection did not run cleanly on %s: ok=%v failed=%v", payload, ok, failed)
	}
	return DropIgnored(ResolveOverlaps(evaluated))
}

func b5Rewrite(t *testing.T, payload string, p models.PIIPolicy) string {
	t.Helper()
	out, err := ApplyFindingsToJSONChecked(payload, b5Resolved(t, payload, p))
	if err != nil {
		t.Fatalf("a payload the detector understood must be rewritable, got: %v", err)
	}
	return out
}

// the shapes that used to keep the raw value: anything whose top level is not an object
func TestChecked_ARootThatIsNotAnObjectIsRewritten(t *testing.T) {
	p := b5Policy("REDACT", "REDACT")
	for payload, want := range map[string]string{
		`[{"p":"555-123-4567"}]`:                     `[{"p":"[Phone-REDACTED]"}]`,
		`["call 555-123-4567 now","fine"]`:           `["call [Phone-REDACTED] now","fine"]`,
		`"mail jane@example.com please"`:             `"mail [Email-REDACTED] please"`,
		`{"a":[["555-123-4567"],[{"b":"x@y.org"}]]}`: `{"a":[["[Phone-REDACTED]"],[{"b":"[Email-REDACTED]"}]]}`,
		`{"b":"fine","a":"jane@example.com"}`:        `{"a":"[Email-REDACTED]","b":"fine"}`, // object: unchanged behaviour, keys sorted
	} {
		if got := b5Rewrite(t, payload, p); got != want {
			t.Errorf("payload %s\n got  %s\n want %s", payload, got, want)
		}
	}
}

func TestChecked_TwoFindingsInOneString(t *testing.T) {
	got := b5Rewrite(t, `{"n":"call 555-123-4567 or 555-987-6543"}`, b5Policy("REDACT", "REDACT"))
	if got != `{"n":"call [Phone-REDACTED] or [Phone-REDACTED]"}` {
		t.Fatalf("both positions must be replaced (back to front), got %s", got)
	}
}

func TestChecked_MaskAndRedactTogether(t *testing.T) {
	got := b5Rewrite(t, `["555-123-4567","jane@example.com"]`, b5Policy("REDACT", "MASK"))
	if got != `["************","[Email-REDACTED]"]` {
		t.Fatalf("got %s", got)
	}
}

func TestChecked_ARepeatedValueInTwoFieldsIsRewrittenInBoth(t *testing.T) {
	got := b5Rewrite(t, `{"a":"x@y.com","b":"x@y.com"}`, b5Policy("REDACT", "REDACT"))
	if got != `{"a":"[Email-REDACTED]","b":"[Email-REDACTED]"}` {
		t.Fatalf("got %s", got)
	}
}

// walkJSON gives {"a.b": ...} and {"a":{"b": ...}} the SAME path text. Matching by path alone would apply one string's
// finding to the other. The rewrite checks that the finding's text really sits at its offsets in that string.
func TestChecked_TwoStringsWithTheSamePathTextAreNotConfused(t *testing.T) {
	got := b5Rewrite(t, `{"a.b":"x@y.com","a":{"b":"p@q.com"}}`, b5Policy("REDACT", "REDACT"))
	if got != `{"a":{"b":"[Email-REDACTED]"},"a.b":"[Email-REDACTED]"}` {
		t.Fatalf("got %s", got)
	}
}

func TestChecked_FailsClosed(t *testing.T) {
	p := b5Policy("REDACT", "REDACT")
	payload := `{"a":"jane@example.com"}`
	good := b5Resolved(t, payload, p)

	t.Run("not JSON", func(t *testing.T) {
		if _, err := ApplyFindingsToJSONChecked("just some text", good); err == nil {
			t.Fatal("text that is not JSON cannot be rewritten as JSON: that must be an error")
		}
	})
	t.Run("data after the value", func(t *testing.T) {
		if _, err := ApplyFindingsToJSONChecked(payload+" tail", good); err == nil {
			t.Fatal("trailing data must be an error")
		}
	})
	t.Run("a finding whose path does not exist", func(t *testing.T) {
		bad := append([]EvaluatedFinding{}, good...)
		bad[0].Finding.FieldPath = "nope"
		if _, err := ApplyFindingsToJSONChecked(payload, bad); err == nil {
			t.Fatal("a finding that matches no string must be an error, not skipped")
		}
	})
	t.Run("a finding whose text is not at its offsets", func(t *testing.T) {
		bad := append([]EvaluatedFinding{}, good...)
		bad[0].Finding.Match = "someone@else.com"
		if _, err := ApplyFindingsToJSONChecked(payload, bad); err == nil {
			t.Fatal("offsets that point at different text must be an error")
		}
	})
	t.Run("one good finding does not excuse a bad one", func(t *testing.T) {
		two := b5Resolved(t, `{"a":"jane@example.com","b":"x@y.com"}`, p)
		two[1].Finding.FieldPath = "nope"
		if _, err := ApplyFindingsToJSONChecked(`{"a":"jane@example.com","b":"x@y.com"}`, two); err == nil {
			t.Fatal("every finding must be applied")
		}
	})
}

func TestChecked_NumbersAreKeptExactly(t *testing.T) {
	// the old rewrite decoded numbers as float64, so this became 1.2345678901234567e+19
	got := b5Rewrite(t, `{"id":12345678901234567890,"price":10.50,"e":"jane@example.com"}`, b5Policy("REDACT", "REDACT"))
	if !strings.Contains(got, `"id":12345678901234567890`) || !strings.Contains(got, `"price":10.50`) {
		t.Fatalf("numbers must survive exactly as written, got %s", got)
	}
}

func TestChecked_ANothingToRewritePayloadIsByteIdentical(t *testing.T) {
	p := b5Policy("REDACT", "REDACT")
	for _, payload := range []string{`{"b":1,  "a":2}`, `[ 1 , "fine" ]`, `{"k":"nothing here"}`} {
		out, err := ApplyFindingsToJSONChecked(payload, b5Resolved(t, payload, p))
		if err != nil || out != payload {
			t.Errorf("a payload with no findings must come back untouched (spacing, key order): %q -> %q (%v)", payload, out, err)
		}
	}
	// OBSERVE records the finding but changes nothing, so the payload must not be reformatted either
	observe := b5Policy("OBSERVE", "OBSERVE")
	payload := `{"b":1,  "e":"jane@example.com"}`
	if out, err := ApplyFindingsToJSONChecked(payload, b5Resolved(t, payload, observe)); err != nil || out != payload {
		t.Errorf("an OBSERVE-only payload must be byte-identical: %q (%v)", out, err)
	}
}

// the old entry point still exists for old callers and tests: it rewrites objects, and still hands back the input on error
func TestLegacyWrapper_KeepsItsOldContract(t *testing.T) {
	p := b5Policy("REDACT", "REDACT")
	payload := `{"a":"jane@example.com"}`
	if got := ApplyFindingsToJSON(payload, b5Resolved(t, payload, p)); got != `{"a":"[Email-REDACTED]"}` {
		t.Errorf("got %s", got)
	}
	if got := ApplyFindingsToJSON("not json", nil); got != "not json" {
		t.Errorf("legacy fail-open contract changed: %q", got)
	}
}

// the rewriter and walkJSON must agree on the path text of every string, for every shape
func TestWalkAndRewriteAgreeOnEveryPath(t *testing.T) {
	p := b5Policy("REDACT", "REDACT")
	shapes := []string{
		`"jane@example.com"`, `["jane@example.com"]`, `[["jane@example.com"]]`, `[[[{"a":["jane@example.com"]}]]]`,
		`{"a":"jane@example.com"}`, `{"a":{"b":{"c":"jane@example.com"}}}`, `{"a":[{"b":"jane@example.com"},{"b":"x@y.com"}]}`,
		`{"a.b.c":"jane@example.com"}`, `{"a[0]":"jane@example.com"}`, `{"":"jane@example.com"}`, `[{"":["x@y.com"]}]`,
		`{"é":"jane@example.com","日本":["x@y.com"]}`,
	}
	for _, payload := range shapes {
		out, err := ApplyFindingsToJSONChecked(payload, b5Resolved(t, payload, p))
		if err != nil {
			t.Errorf("%s: %v", payload, err)
			continue
		}
		if strings.Contains(out, "@") {
			t.Errorf("%s: a raw value is still in the output: %s", payload, out)
		}
		if !json.Valid([]byte(out)) {
			t.Errorf("%s: the output is not valid JSON: %s", payload, out)
		}
	}
}

// ---------------------------------------------------------------------------------------------------------------- NFC

func TestNormalizeForScan(t *testing.T) {
	decomposed := "cafe\u0301"
	if got := NormalizeForScan(decomposed); got != "caf\u00e9" || len(got) != 5 {
		t.Fatalf("e + combining accent must become one é: %q (%d bytes)", got, len(got))
	}
	composed := "caf\u00e9"
	if got := NormalizeForScan(composed); got != composed {
		t.Fatalf("text that is already NFC must come back unchanged: %q", got)
	}
	if got := NormalizeForScan("plain ascii"); got != "plain ascii" {
		t.Fatal("ASCII must come back unchanged")
	}
}

// the reproduced bug: positions are in the NFC text, but were applied to the original, so the replacement landed one byte early
func TestNFC_TheRedactionLandsOnTheValueNotNextToIt(t *testing.T) {
	p := b5Policy("REDACT", "REDACT")

	// plain-text path, as create.go and the artifact scan do it
	text := "cafe\u0301 contact jane.doe@example.com now"
	findings, _ := Detect(text, p.Spec.Detectors)
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %d", len(findings))
	}
	f := findings[0]
	got := ApplyTransformations(NormalizeForScan(text), []Transformation{{Start: f.Start, End: f.End, Replacement: "[Email-1]"}})
	if got != "caf\u00e9 contact [Email-1] now" {
		t.Fatalf("text path: got %q", got)
	}

	// JSON path
	out := b5Rewrite(t, `{"n":"cafe\u0301 contact jane.doe@example.com now"}`, p)
	if out != "{\"n\":\"caf\u00e9 contact [Email-REDACTED] now\"}" {
		t.Fatalf("json path: got %q", out)
	}
}

func TestNFC_TextWithNoFindingKeepsItsExactBytes(t *testing.T) {
	decomposed := "{\"n\":\"cafe\u0301 and nothing sensitive\"}"
	out, err := ApplyFindingsToJSONChecked(decomposed, b5Resolved(t, decomposed, b5Policy("REDACT", "REDACT")))
	if err != nil || out != decomposed {
		t.Fatalf("a string with no finding must not be normalised: %q (%v)", out, err)
	}
}

// ---------------------------------------------------------------------------------------------------------------- Decrypt

func TestDecrypt_ACiphertextShorterThanTheNonceIsAnErrorNotAPanic(t *testing.T) {
	for name, input := range map[string]string{
		"empty":        "",
		"three bytes":  base64.StdEncoding.EncodeToString([]byte("abc")),
		"eleven bytes": base64.StdEncoding.EncodeToString(make([]byte, 11)),
	} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: Decrypt panicked: %v", name, r)
				}
			}()
			if _, err := Decrypt(input); err == nil {
				t.Errorf("%s: a corrupt ciphertext must be an error", name)
			}
		}()
	}
}

func TestDecrypt_RoundTripAndTamper(t *testing.T) {
	enc, err := Encrypt("jane.doe@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := Decrypt(enc); err != nil || got != "jane.doe@example.com" {
		t.Fatalf("round trip: %q %v", got, err)
	}
	raw, _ := base64.StdEncoding.DecodeString(enc)
	raw[len(raw)-1] ^= 0xff
	if _, err := Decrypt(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Fatal("a tampered ciphertext must fail to decrypt")
	}
}

// the cause behind the test above: the two locations must have different path text
func TestWalkJSON_AKeyContainingADotDoesNotShareAPathWithANestedKey(t *testing.T) {
	var parsed interface{}
	json.Unmarshal([]byte(`{"a.b":"one","a":{"b":"two"},"c[0]":"three","c":["four"],"d\\e":"five"}`), &parsed)

	paths := map[string]string{}
	walkJSON("", parsed, func(path, value string) { paths[value] = path })

	want := map[string]string{"one": `a\.b`, "two": "a.b", "three": `c\[0\]`, "four": "c[0]", "five": `d\\e`}
	for value, path := range want {
		if paths[value] != path {
			t.Errorf("%s: path = %q, want %q", value, paths[value], path)
		}
	}
	seen := map[string]bool{}
	for _, p := range paths {
		if seen[p] {
			t.Errorf("path %q is used by two different strings", p)
		}
		seen[p] = true
	}
}

// ordinary keys must keep exactly the path text they always had, because rules match on it (fieldPaths)
func TestWalkJSON_OrdinaryKeysKeepTheirPaths(t *testing.T) {
	var parsed interface{}
	json.Unmarshal([]byte(`{"customer":{"emails":["x"],"name":"y"},"snake_case-key":"z"}`), &parsed)
	var got []string
	walkJSON("", parsed, func(path, value string) { got = append(got, path) })
	joined := strings.Join(got, "|")
	for _, want := range []string{"customer.emails[0]", "customer.name", "snake_case-key"} {
		if !strings.Contains(joined, want) {
			t.Errorf("path %q is missing from %s", want, joined)
		}
	}
}
