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
	"os"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

var fingerprintKey = []byte(os.Getenv("PII_FINGERPRINT_KEY"))
var encryptKey = []byte(os.Getenv("PII_ENCR_KEY"))

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

func isValidText(text string) bool {
	return utf8.ValidString(text)
}

func normalizeText(text string) string {
	return norm.NFC.String(text)
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
