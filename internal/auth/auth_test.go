package auth

// RFC-006 §33 Security. In-package (not auth_test) because lookup() and the
// principals map are unexported, and "does a missing key resolve to a
// zero-value Principal" is exactly the behaviour worth pinning down.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestHasScope(t *testing.T) {
	p := Principal{
		Name:   "test-principal",
		Key:    "test-key",
		Scopes: []string{"pii.policy.read", "alerts.read"},
	}

	tests := []struct {
		name  string
		scope string
		want  bool
	}{
		{"scope present", "pii.policy.read", true},
		{"second scope present", "alerts.read", true},
		{"scope absent", "pii.raw_value.view", false},
		{"empty scope is not granted", "", false},
		// Scopes are compared as exact strings with no normalisation, so a
		// near-miss in a config file is a silent 403 rather than a match.
		// Pinning this down means a future "helpful" case-insensitive or
		// prefix match can't be added without a test failing.
		{"case mismatch does not match", "PII.POLICY.READ", false},
		{"prefix does not match", "pii.policy", false},
		{"superstring does not match", "pii.policy.read.extra", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.HasScope(tt.scope); got != tt.want {
				t.Errorf("HasScope(%q) = %v, want %v", tt.scope, got, tt.want)
			}
		})
	}
}

func TestHasScope_EmptyScopeList(t *testing.T) {
	// A principal with no scopes is a real, valid principal that can do
	// nothing -- distinct from a key that doesn't exist. See TestLookup.
	p := Principal{Name: "no-scopes", Key: "k"}

	if p.HasScope("anything") {
		t.Error("principal with no scopes granted a scope")
	}
}

func writeTempConfig(t *testing.T, contents string) string {
	t.Helper()

	dir := t.TempDir() // cleaned up automatically
	path := filepath.Join(dir, "config.json")

	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatalf("failed to write temp config: %v", err)
	}
	return path
}

func TestLoadPrincipals(t *testing.T) {
	path := writeTempConfig(t, `[
		{"name": "alpha", "key": "key-alpha", "scopes": ["alerts.read"]},
		{"name": "beta",  "key": "key-beta",  "scopes": ["alerts.read", "alerts.acknowledge"]}
	]`)

	if err := LoadPrincipals(path); err != nil {
		t.Fatalf("LoadPrincipals failed on valid config: %v", err)
	}

	if len(principals) != 2 {
		t.Fatalf("expected 2 principals loaded, got %d", len(principals))
	}

	// The map must be keyed by KEY, not by name -- the middleware looks up by
	// whatever arrived in the header.
	p, ok := principals["key-beta"]
	if !ok {
		t.Fatal("principals map is not keyed by Key")
	}
	if p.Name != "beta" {
		t.Errorf("key-beta resolved to principal %q, want %q", p.Name, "beta")
	}
}

func TestLoadPrincipals_Failures(t *testing.T) {
	t.Run("missing file returns error", func(t *testing.T) {
		if err := LoadPrincipals(filepath.Join(t.TempDir(), "nope.json")); err == nil {
			t.Error("expected an error for a missing config file, got nil")
		}
	})

	t.Run("malformed json returns error", func(t *testing.T) {
		path := writeTempConfig(t, `[{"name": "broken",`)
		if err := LoadPrincipals(path); err == nil {
			t.Error("expected an error for malformed json, got nil")
		}
	})

	// Startup calls log.Fatal on either of the above, which is the intended
	// behaviour: a broken auth config must stop the app rather than leave every
	// scoped route silently returning 403 with no explanation.
}

func TestLookup(t *testing.T) {
	path := writeTempConfig(t, `[
		{"name": "alpha", "key": "key-alpha", "scopes": ["alerts.read"]},
		{"name": "empty-scopes", "key": "key-empty", "scopes": []}
	]`)

	if err := LoadPrincipals(path); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	t.Run("known key returns principal and true", func(t *testing.T) {
		p, ok := lookup("key-alpha")
		if !ok {
			t.Fatal("lookup returned false for a known key")
		}
		if p.Name != "alpha" {
			t.Errorf("got principal %q, want %q", p.Name, "alpha")
		}
	})

	t.Run("unknown key returns false", func(t *testing.T) {
		_, ok := lookup("no-such-key")
		if ok {
			t.Error("lookup returned true for an unknown key")
		}
	})

	// THE REASON lookup RETURNS A BOOL AT ALL.
	// Without it, a missing key yields the zero-value Principal{} -- empty name,
	// no scopes -- which is indistinguishable from a real principal that simply
	// has no permissions yet. Those are different situations: one is "who are
	// you?", the other is "you can't do that". Same class of bug as
	// GetWorkerCounter's found-flag.
	t.Run("unknown key is distinguishable from a real principal with no scopes", func(t *testing.T) {
		_, unknownOK := lookup("no-such-key")
		real, realOK := lookup("key-empty")

		if unknownOK {
			t.Error("unknown key reported as found")
		}
		if !realOK {
			t.Fatal("real principal with empty scopes reported as not found")
		}
		if real.Name == "" {
			t.Error("real principal came back with an empty name -- indistinguishable from a zero value")
		}
	})
}

func TestRequireScope(t *testing.T) {
	gin.SetMode(gin.TestMode)

	path := writeTempConfig(t, `[
		{"name": "reader", "key": "key-reader", "scopes": ["alerts.read"]}
	]`)
	if err := LoadPrincipals(path); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	// buildRouter wires one route gated by the given scope, and records whether
	// the handler behind the gate actually ran. That last part is the point:
	// asserting only on status code would not catch middleware that returns 403
	// but still lets the handler execute.
	buildRouter := func(scope string, handlerRan *bool, seenActor *string) *gin.Engine {
		r := gin.New()
		r.GET("/gated", RequireScope(scope), func(c *gin.Context) {
			*handlerRan = true
			*seenActor = c.GetString("actor")
			c.Status(http.StatusOK)
		})
		return r
	}

	tests := []struct {
		name        string
		header      string
		key         string
		wantStatus  int
		wantHandler bool
		wantActor   string
	}{
		{"valid key with scope passes through", "X-API-Key", "key-reader", http.StatusOK, true, "reader"},
		{"unknown key is refused", "X-API-Key", "not-a-key", http.StatusForbidden, false, ""},
		{"missing header is refused", "", "", http.StatusForbidden, false, ""},
		// The retired admin header must not work any more. Without this case,
		// a leftover ADMIN_KEY check could be reintroduced and nothing would fail.
		{"old X-Admin-Key header is not accepted", "X-Admin-Key", "key-reader", http.StatusForbidden, false, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlerRan := false
			seenActor := ""

			req := httptest.NewRequest(http.MethodGet, "/gated", nil)
			if tt.header != "" {
				req.Header.Set(tt.header, tt.key)
			}

			w := httptest.NewRecorder()
			buildRouter("alerts.read", &handlerRan, &seenActor).ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d", w.Code, tt.wantStatus)
			}
			if handlerRan != tt.wantHandler {
				t.Errorf("handler ran = %v, want %v", handlerRan, tt.wantHandler)
			}
			if seenActor != tt.wantActor {
				t.Errorf("actor = %q, want %q", seenActor, tt.wantActor)
			}
		})
	}

	t.Run("valid key without the required scope is refused", func(t *testing.T) {
		handlerRan := false
		seenActor := ""

		req := httptest.NewRequest(http.MethodGet, "/gated", nil)
		req.Header.Set("X-API-Key", "key-reader")

		w := httptest.NewRecorder()
		// Gate on a scope this principal genuinely does not hold.
		buildRouter("alerts.acknowledge", &handlerRan, &seenActor).ServeHTTP(w, req)

		if w.Code != http.StatusForbidden {
			t.Errorf("status = %d, want %d", w.Code, http.StatusForbidden)
		}
		if handlerRan {
			t.Error("handler ran despite the principal lacking the scope")
		}
	})
}
