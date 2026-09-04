package pii

import (
	"MTL_Scheduler_PII_Test/internals/models"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// RFC-006 §5 PII Categories: EMAIL_ADDRESS, PHONE_NUMBER, NATIONAL_ID, PASSPORT_NUMBER, CREDIT_CARD_NUMBER, BANK_ACCOUNT, IP_ADDRESS, PERSON_NAME, ADDRESS, DATE_OF_BIRTH — this project implements a subset: Email, Phone, SSN, CreditCard
type PIIType string

type Finding struct {
	Type       PIIType
	Match      string
	DetectorID string
}

var fingerprintKey = []byte(os.Getenv("PII_FINGERPRINT_KEY"))
var encryptKey = []byte(os.Getenv("PII_ENCR_KEY"))

// RFC-006 §7 Scan Pipeline: Content -> Normalizer -> Detector Set -> Candidate Findings -> Policy Evaluation. This function implements the Detector Set stage; there is no Normalizer stage yet
// RFC-006 §5 Detector Definitions: patterns are no longer hardcoded in Go — they are compiled at call-time from the loaded PIIPolicy's detector list, matching the RFC's requirement that detection be policy-driven rather than baked into application code
// RFC-006 §8 Scan Sources: this only scans JOB_PAYLOAD/TASK_NAME-equivalent text passed in as a string, not metadata/results/logs
func Detect(text string, detectors []models.DetectorDefinition) ([]Finding, []string) {

	var findings []Finding
	var failed_det []string

	for _, det := range detectors {
		if !det.Enabled {
			continue
		}

		compiled_pattern, err := regexp.Compile(det.Pattern)
		if err != nil {
			failed_det = append(failed_det, det.ID)
			slog.Error("invalid detector pattern, skipping", "detector_id", det.ID, "error", err)
			continue
		}

		for _, m := range compiled_pattern.FindAllString(text, -1) {
			findings = append(findings, Finding{Type: PIIType(det.PIIType), Match: m, DetectorID: det.ID})
		}
	}

	return findings, failed_det
}

// RFC-006 §11 Actions — REDACT: "Replace sensitive content with a marker." The action itself now comes from
// the loaded policy's rules rather than being hardcoded in Go; this function only performs the substitution
// once REDACT has already been decided elsewhere. OBSERVE/MASK/BLOCK are not yet implemented as distinct code paths.

// PRD §38 PII-Safe Logging: example pattern "[NATIONAL_ID_REDACTED]" — this project uses "[Type-Index]" placeholders
// , e.g. "[SSN-2]", to keep occurrences distinguishable for future rehydration. The Type value now originates from the
//
//	policy's detector definitions (PIIType strings), not a fixed Go enum.
func Replacer(payload string, match string, PII_Type PIIType, index string) string {
	replacement := "[" + string(PII_Type) + "-" + index + "]"
	return strings.Replace(payload, match, replacement, 1)
}

func Mask(value string, config models.MaskConfig) string {
	VisibleCharacters := config.VisibleCharacters
	MaskCharacter := config.MaskCharacter

	masked_return := ""

	switch config.Strategy {
	case "FULL":
		//nothing happens lol
		masked_return = value
	case "KEEP_PREFIX":
		masked_return = prefixReplacer(value, VisibleCharacters, MaskCharacter, false)
	case "KEEP_SUFFIX":
		masked_return = suffixReplacer(value, VisibleCharacters, MaskCharacter, false)
	case "KEEP_PREFIX_SUFFIX":
		masked_return = prefixsuffixReplacer(value, VisibleCharacters, MaskCharacter, false)
	case "PRESERVE_FORMAT":
		masked_return = suffixReplacer(value, 0, MaskCharacter, true)
	case "EMAIL":
		masked_return = PrefixBeforeAt(value, VisibleCharacters, MaskCharacter, false, config.DomainMode) //i mean depends i guess????
	case "FIXED":
		masked_return = MaskCharacter
	}

	return masked_return
}

func Fingerprint(value string) string {
	h := hmac.New(sha256.New, fingerprintKey)
	h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}

func Encrypt(value string) (string, error) {
	key := sha256.Sum256([]byte(encryptKey))

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	ciphertext := gcm.Seal(nonce, nonce, []byte(value), nil)

	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

func Decrypt(ciphertext string) (string, error) {
	key := sha256.Sum256([]byte(encryptKey))

	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", err
	}

	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonceSize := gcm.NonceSize()
	nonce, encrypted := data[:nonceSize], data[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}
