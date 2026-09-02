package unit_test

import (
	"testing"

	"MTL_Scheduler_PII_Test/internals/models"
	"MTL_Scheduler_PII_Test/internals/pii"
)

func TestMask(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		config models.MaskConfig
		want   string
	}{
		// ---------- FULL ----------
		{
			name:   "FULL masks nothing, returns value unchanged",
			value:  "1234567890123",
			config: models.MaskConfig{Strategy: "FULL", MaskCharacter: "*"},
			want:   "1234567890123",
		},
		{
			name:   "FULL on empty string returns empty string",
			value:  "",
			config: models.MaskConfig{Strategy: "FULL", MaskCharacter: "*"},
			want:   "",
		},

		// ---------- FIXED ----------
		{
			name:   "FIXED replaces entire value regardless of length",
			value:  "1234567890123",
			config: models.MaskConfig{Strategy: "FIXED", MaskCharacter: "[MASKED]"},
			want:   "[MASKED]",
		},
		{
			name:   "FIXED on short value still returns the fixed string",
			value:  "ab",
			config: models.MaskConfig{Strategy: "FIXED", MaskCharacter: "[MASKED]"},
			want:   "[MASKED]",
		},

		// ---------- KEEP_SUFFIX ----------
		{
			name:   "KEEP_SUFFIX matches RFC-006 example exactly",
			value:  "1234567890123",
			config: models.MaskConfig{Strategy: "KEEP_SUFFIX", VisibleCharacters: 4, MaskCharacter: "*"},
			want:   "*********0123",
		},
		{
			name:   "KEEP_SUFFIX with VisibleCharacters 0 masks everything",
			value:  "1234567890123",
			config: models.MaskConfig{Strategy: "KEEP_SUFFIX", VisibleCharacters: 0, MaskCharacter: "*"},
			want:   "*************",
		},
		{
			name:   "KEEP_SUFFIX with VisibleCharacters larger than value length masks nothing",
			value:  "123",
			config: models.MaskConfig{Strategy: "KEEP_SUFFIX", VisibleCharacters: 10, MaskCharacter: "*"},
			want:   "123",
		},
		{
			name:   "KEEP_SUFFIX with VisibleCharacters exactly equal to length masks nothing",
			value:  "1234",
			config: models.MaskConfig{Strategy: "KEEP_SUFFIX", VisibleCharacters: 4, MaskCharacter: "*"},
			want:   "1234",
		},

		// ---------- KEEP_PREFIX ----------
		{
			name:   "KEEP_PREFIX keeps the start, masks the rest",
			value:  "1234567890123",
			config: models.MaskConfig{Strategy: "KEEP_PREFIX", VisibleCharacters: 4, MaskCharacter: "*"},
			want:   "1234*********",
		},
		{
			name:   "KEEP_PREFIX with VisibleCharacters 0 masks everything",
			value:  "1234567890123",
			config: models.MaskConfig{Strategy: "KEEP_PREFIX", VisibleCharacters: 0, MaskCharacter: "*"},
			want:   "*************",
		},

		// ---------- KEEP_PREFIX_SUFFIX ----------
		{
			name:   "KEEP_PREFIX_SUFFIX keeps both ends, masks the middle",
			value:  "1234567890123",
			config: models.MaskConfig{Strategy: "KEEP_PREFIX_SUFFIX", VisibleCharacters: 4, MaskCharacter: "*"},
			want:   "1234*****0123",
		},
		{
			name:   "KEEP_PREFIX_SUFFIX with VisibleCharacters 0 masks everything",
			value:  "1234567890123",
			config: models.MaskConfig{Strategy: "KEEP_PREFIX_SUFFIX", VisibleCharacters: 0, MaskCharacter: "*"},
			want:   "*************",
		},

		// ---------- PRESERVE_FORMAT ----------
		{
			name:   "PRESERVE_FORMAT masks digits, keeps dashes visible",
			value:  "081-234-5678",
			config: models.MaskConfig{Strategy: "PRESERVE_FORMAT", MaskCharacter: "*"},
			want:   "***-***-****",
		},
		{
			name:   "PRESERVE_FORMAT on a value with no formatting characters masks everything",
			value:  "1234567890123",
			config: models.MaskConfig{Strategy: "PRESERVE_FORMAT", MaskCharacter: "*"},
			want:   "*************",
		},
		{
			name:   "PRESERVE_FORMAT keeps multiple different formatting characters intact",
			value:  "081-234 5678",
			config: models.MaskConfig{Strategy: "PRESERVE_FORMAT", MaskCharacter: "*"},
			want:   "***-*** ****",
		},

		// ---------- EMAIL ----------
		{
			name:   "EMAIL masks local part, keeps domain fully visible",
			value:  "john.doe@example.com",
			config: models.MaskConfig{Strategy: "EMAIL", VisibleCharacters: 0, MaskCharacter: "*"},
			want:   "********@example.com",
		},
		{
			name:   "EMAIL with a short local part still stops masking at @",
			value:  "jd@example.com",
			config: models.MaskConfig{Strategy: "EMAIL", VisibleCharacters: 0, MaskCharacter: "*"},
			want:   "**@example.com",
		},

		// ---------- unrecognized strategy ----------
		{
			name:   "unrecognized strategy falls through to empty string",
			value:  "1234567890123",
			config: models.MaskConfig{Strategy: "NOT_A_REAL_STRATEGY", MaskCharacter: "*"},
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pii.Mask(tt.value, tt.config)
			if got != tt.want {
				t.Errorf("Mask(%q, %+v) = %q, want %q", tt.value, tt.config, got, tt.want)
			}
		})
	}
}
