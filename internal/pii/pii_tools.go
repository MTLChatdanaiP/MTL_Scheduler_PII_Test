package pii

import (
	"MTL_Scheduler_PII_Test/internal/models"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// RFC-006 §33: secrets live outside the policy document, in the environment.
//
// These were previously package-level variables initialised from os.Getenv, and
// Go runs package initialisation BEFORE main() -- so a key that only exists in
// a .env file loaded by godotenv.Load() inside main() was never seen, leaving an
// empty HMAC key and an encryption key derived from sha256(""). They are now read
// lazily, on first use, which is always after main() has loaded the environment.
var (
	keysOnce       sync.Once
	fingerprintKey []byte
	encryptKey     []byte
)

// minKeyBytes is the shortest key ValidateKeys accepts.
const minKeyBytes = 16

var errCiphertextTooShort = errors.New("pii: ciphertext is shorter than the encryption nonce")

var errNoEncryptKey = errors.New("PII_ENCR_KEY is not set")

// piiKeys takes nothing and returns the fingerprint key and the encryption key,
// by reading them from the environment exactly once, on first call.
func piiKeys() (fingerprint []byte, encrypt []byte) {
	keysOnce.Do(func() {
		fingerprintKey = []byte(os.Getenv("PII_FINGERPRINT_KEY"))
		encryptKey = []byte(os.Getenv("PII_ENCR_KEY"))
		if len(fingerprintKey) == 0 || len(encryptKey) == 0 {
			slog.Warn("PII keys are not configured: fingerprints are unkeyed and vaulting is disabled until PII_FINGERPRINT_KEY and PII_ENCR_KEY are set")
		}
	})
	return fingerprintKey, encryptKey
}

// ValidateKeys takes nothing and returns nil only if both keys are present and
// at least minKeyBytes long, otherwise an error naming exactly what is wrong.
// Call it once from main() after godotenv.Load so a misconfigured deployment
// fails at startup instead of silently running with a null key.
func ValidateKeys() error {
	fp, enc := piiKeys()
	return ValidateKeyValues(string(fp), string(enc))
}

// ValidateKeyValues returns an error naming each PII key that is shorter than minKeyBytes. It reports the NAME and the LENGTH,
// never the value. ValidateKeys calls it with the cached keys; startup checks call it with the live environment.
func ValidateKeyValues(fingerprint, encrypt string) error {
	var problems []string
	if len(fingerprint) < minKeyBytes {
		problems = append(problems, fmt.Sprintf("PII_FINGERPRINT_KEY must be at least %d bytes (got %d)", minKeyBytes, len(fingerprint)))
	}
	if len(encrypt) < minKeyBytes {
		problems = append(problems, fmt.Sprintf("PII_ENCR_KEY must be at least %d bytes (got %d)", minKeyBytes, len(encrypt)))
	}
	if len(problems) > 0 {
		return fmt.Errorf("PII keys are not usable: %s", strings.Join(problems, "; "))
	}
	return nil
}

// RFC-006 §11 Actions — REDACT: "Replace sensitive content with a marker." The action itself now comes from
// the loaded policy's rules rather than being hardcoded in Go; this function only performs the substitution
// once REDACT has already been decided elsewhere. OBSERVE/MASK/BLOCK are not yet implemented as distinct code paths.

// PRD §38 PII-Safe Logging: example pattern "[NATIONAL_ID_REDACTED]" — this project uses "[Type-Index]" placeholders
// , e.g. "[SSN-2]", to keep occurrences distinguishable for future rehydration. The Type value now originates from the
//
//	policy's detector definitions (PIIType strings), not a fixed Go enum.
func ReplacerMatch(payload string, match string, PII_Type PIIType, index string) string {
	replacement := "[" + string(PII_Type) + "-" + index + "]"
	return strings.Replace(payload, match, replacement, 1)
}

// ValidMaskStrategies are the 7 strategies RFC-006 §12 defines. Mask and the
// policy validator both read this one list, so they can never disagree about what
// a legal strategy is.
var ValidMaskStrategies = []string{
	"FULL", "KEEP_PREFIX", "KEEP_SUFFIX", "KEEP_PREFIX_SUFFIX",
	"PRESERVE_FORMAT", "EMAIL", "FIXED",
}

const (
	defaultMaskCharacter = "*"
	defaultFixedToken    = "[MASKED]"
)

// normalizeMaskConfig takes a raw MaskConfig from a policy and returns a safe
// copy, by upper-casing the strategy and domain mode, defaulting a missing mask
// character ("*", or "[MASKED]" for FIXED), keeping only the first character of
// a longer one, and clamping negative counts to zero. After this every field is
// something the masking helpers can use without panicking.
func normalizeMaskConfig(cfg models.MaskConfig) models.MaskConfig {
	cfg.Strategy = strings.ToUpper(strings.TrimSpace(cfg.Strategy))
	cfg.DomainMode = strings.ToUpper(strings.TrimSpace(cfg.DomainMode))

	if cfg.VisibleCharacters < 0 {
		cfg.VisibleCharacters = 0
	}
	if cfg.LocalVisiblePrefix < 0 {
		cfg.LocalVisiblePrefix = 0
	}

	switch {
	case cfg.MaskCharacter == "" && cfg.Strategy == "FIXED":
		cfg.MaskCharacter = defaultFixedToken
	case cfg.MaskCharacter == "":
		cfg.MaskCharacter = defaultMaskCharacter
	case cfg.Strategy != "FIXED":
		// only a single character is ever used to fill, FIXED is the one
		// strategy where the whole configured string is the replacement
		cfg.MaskCharacter = string([]rune(cfg.MaskCharacter)[:1])
	}

	return cfg
}

// maskAll takes a value and a mask character and returns a string of the same
// length made entirely of that character, by replacing every character.
func maskAll(value string, maskChar string) string {
	return strings.Repeat(maskChar, utf8.RuneCountInString(value))
}

func hasAlphanumeric(s string) bool {
	for _, r := range s {
		if isAlphanumeric(r) {
			return true
		}
	}
	return false
}

// Mask takes the matched text and a MaskConfig and returns the replacement text.
//
// RFC-006 §12 invariant: Mask is defined for EVERY input and never hands back the
// value it was asked to hide. Before this, FULL returned its input unchanged (so
// a MASK/FULL credit card passed through the whole pipeline unmasked), a short
// value under KEEP_SUFFIX or KEEP_PREFIX_SUFFIX came back whole, an unknown
// strategy returned an empty string, and an omitted maskCharacter panicked three
// of the strategies.
func Mask(value string, config models.MaskConfig) string {
	cfg := normalizeMaskConfig(config)

	var out string

	MaskCharacter := cfg.MaskCharacter

	if MaskCharacter == "" {
		MaskCharacter = "*"
	}

	switch cfg.Strategy {
	case "FULL":
		out = strings.Repeat(MaskCharacter, utf8.RuneCountInString(value))
	case "KEEP_PREFIX":
		out = prefixReplacer(value, cfg.VisibleCharacters, MaskCharacter, false)
	case "KEEP_SUFFIX":
		out = suffixReplacer(value, cfg.VisibleCharacters, MaskCharacter, false)
	case "KEEP_PREFIX_SUFFIX":
		out = prefixsuffixReplacer(value, cfg.VisibleCharacters, MaskCharacter, false)
	case "PRESERVE_FORMAT":
		out = suffixReplacer(value, 0, MaskCharacter, true)
	case "EMAIL":
		visible := cfg.LocalVisiblePrefix
		if visible == 0 {
			visible = cfg.VisibleCharacters // existing policies set visibleCharacters
		}
		out = PrefixBeforeAt(value, visible, MaskCharacter, false, cfg.DomainMode)
	case "FIXED":
		out = MaskCharacter
	default:
		// an unknown or empty strategy: fail closed, never open and never empty
		out = maskAll(value, MaskCharacter)
	}

	// Final guard. Whatever the strategy produced, if it is identical to the input
	// and the input holds something worth hiding, mask everything. This single rule
	// closes every "value shorter than the visible window" case.
	if cfg.Strategy != "FIXED" && out == value && hasAlphanumeric(value) {
		return maskAll(value, cfg.MaskCharacter)
	}

	return out
}

func Fingerprint(value string) string {
	fp, _ := piiKeys()
	h := hmac.New(sha256.New, fp)
	h.Write([]byte(value))
	return hex.EncodeToString(h.Sum(nil))
}

func Encrypt(value string) (string, error) {
	_, enc := piiKeys()
	if len(enc) == 0 {
		// fail closed: refuse to "encrypt" with a key anyone can derive
		return "", errNoEncryptKey
	}
	key := sha256.Sum256(enc)

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
	_, enc := piiKeys()
	if len(enc) == 0 {
		return "", errNoEncryptKey
	}
	key := sha256.Sum256(enc)

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
	// a vault row shorter than the nonce is corrupt: report it as an error. Slicing it used to panic ("slice bounds out of
	// range"), which fails the whole vault read for one bad row.
	if len(data) < nonceSize {
		return "", errCiphertextTooShort
	}
	nonce, encrypted := data[:nonceSize], data[nonceSize:]

	plaintext, err := gcm.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return "", err
	}

	return string(plaintext), nil
}

func isValidText(text string) bool {
	return utf8.ValidString(text)
}

func normalizeText(text string) string {
	return norm.NFC.String(text)
}

// NormalizeForScan returns the Unicode NFC form of s. Detect reports positions in the NFC form of the text it is given, so
// anything that applies those positions must apply them to THIS form, not to the original: "e" + a combining accent is 3
// bytes but NFC turns it into the 2-byte "é", which shifts every later position by one and left part of a value behind.
// Text that is already NFC is returned unchanged (no copy).
func NormalizeForScan(s string) string {
	if norm.NFC.IsNormalString(s) {
		return s
	}
	return norm.NFC.String(s)
}

func isValidLuhn(number string) bool {

	number = removeNonAlphanumeric(number)

	for _, r := range number {
		if !unicode.IsDigit(r) {
			return false
		}
	}

	if len(number) == 0 {
		return false
	}

	sum := 0
	shouldDouble := false

	for i := len(number) - 1; i >= 0; i-- {
		digit := int(number[i] - '0')

		if shouldDouble {
			digit *= 2

			if digit > 9 {
				digit -= 9
			}
		}

		sum += digit
		shouldDouble = !shouldDouble
	}

	return sum%10 == 0
}
