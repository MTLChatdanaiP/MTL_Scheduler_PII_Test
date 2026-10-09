package live

import (
	"fmt"
	"strings"
)

// RFC-010 §15 Subscription Scopes.
//
// A client names what it wants with one or more `scope` parameters on the live route:
//
//	/live/activity?scope=workers&scope=queue:orders&scope=execution-chain:01J...
//
// Scopes are OR-ed: an event is delivered if ANY scope matches it. No scope at all means the route's old behaviour (everything the
// caller is authorised to see), so every existing caller is unchanged.
//
// A scope narrows what is DELIVERED. It never widens what is AUTHORISED -- see Scope.Permission and the per-event check in the handler.

// MaxScopes bounds how many scopes one connection may name (RFC-010 §19 subscription limits).
const MaxScopes = 16

const maxScopeIDLen = 200

// ScopeError is returned for a scope string that is not part of the vocabulary. The live handler answers it with 400.
type ScopeError struct{ Msg string }

func (e *ScopeError) Error() string { return e.Msg }

// scope kinds
const (
	kindPlatformSummary = "platform.summary"
	kindRuns            = "runs"
	kindRun             = "run"
	kindChain           = "execution-chain"
	kindQueues          = "queues"
	kindQueue           = "queue"
	kindWorkers         = "workers"
	kindWorker          = "worker"
	kindSchedules       = "schedules"
	kindAlerts          = "alerts"
	kindPIISummary      = "pii.summary"
	kindMonitoring      = "monitoring.health"
	kindComponents      = "components"
)

// plainKinds take no id; idKinds require one.
var plainKinds = map[string]bool{
	kindPlatformSummary: true, kindRuns: true, kindQueues: true, kindWorkers: true, kindSchedules: true,
	kindAlerts: true, kindPIISummary: true, kindMonitoring: true, kindComponents: true,
}
var idKinds = map[string]bool{kindRun: true, kindChain: true, kindQueue: true, kindWorker: true}

// Scope is one parsed subscription scope.
type Scope struct {
	Kind string
	ID   string // only for run, execution-chain, queue, worker
}

// String is the scope as the client wrote it.
func (s Scope) String() string {
	if s.ID == "" {
		return s.Kind
	}
	return s.Kind + ":" + s.ID
}

// ParseScope takes one scope string and returns it parsed, or a *ScopeError.
func ParseScope(raw string) (Scope, error) {
	raw = strings.TrimSpace(raw)
	kind, id, hasID := strings.Cut(raw, ":")

	switch {
	case plainKinds[kind] && !hasID:
		return Scope{Kind: kind}, nil
	case idKinds[kind]:
		if !hasID || id == "" {
			return Scope{}, &ScopeError{fmt.Sprintf("scope %q needs an id, e.g. %s:<id>", kind, kind)}
		}
		if len(id) > maxScopeIDLen {
			return Scope{}, &ScopeError{fmt.Sprintf("scope %q: id is longer than %d characters", kind, maxScopeIDLen)}
		}
		return Scope{Kind: kind, ID: id}, nil
	case plainKinds[kind] && hasID:
		return Scope{}, &ScopeError{fmt.Sprintf("scope %q does not take an id", kind)}
	default:
		return Scope{}, &ScopeError{fmt.Sprintf("unknown scope %q", raw)}
	}
}

// ParseScopes parses every scope string, drops exact duplicates and refuses more than MaxScopes distinct scopes.
func ParseScopes(raw []string) ([]Scope, error) {
	seen := map[Scope]bool{}
	var out []Scope
	for _, r := range raw {
		sc, err := ParseScope(r)
		if err != nil {
			return nil, err
		}
		if seen[sc] {
			continue
		}
		seen[sc] = true
		out = append(out, sc)
	}
	if len(out) > MaxScopes {
		return nil, &ScopeError{fmt.Sprintf("too many scopes: %d (the limit is %d)", len(out), MaxScopes)}
	}
	return out, nil
}

// Permission is the extra API scope needed to subscribe to this scope, or "" when the route's own scope is enough (RFC-010 §15
// "authorization applies to the effective subscription"). It uses the same names as the per-event rules in live_scopes.go.
func (s Scope) Permission() string {
	switch s.Kind {
	case kindAlerts:
		return RequiredScope("alert.x")
	case kindPIISummary:
		return RequiredScope("pii.x")
	}
	return ""
}

// prefixes are the event-type families a scope covers; nil for scopes that select by something else.
func (s Scope) prefixes() []string {
	switch s.Kind {
	case kindRuns, kindRun:
		return []string{"task.", "run.", "attempt."}
	case kindQueues, kindQueue:
		return []string{"queue."}
	case kindWorkers, kindWorker:
		return []string{"worker."}
	case kindSchedules:
		return []string{"schedule."}
	case kindAlerts:
		return []string{"alert."}
	case kindPIISummary:
		return []string{"pii."}
	case kindMonitoring:
		return []string{"monitoring."}
	case kindComponents:
		return []string{"component."}
	}
	return nil
}

// Matches reports whether an event belongs to this scope.
func (s Scope) Matches(e Event) bool {
	switch s.Kind {
	case kindPlatformSummary:
		return e.Type == TypePlatformSummary
	case kindChain:
		return e.ChainID != "" && e.ChainID == s.ID
	}
	if !hasAnyPrefix(e.Type, s.prefixes()) {
		return false
	}
	if s.ID != "" {
		return e.Subject == s.ID
	}
	return true
}

// SQL returns the condition (and its arguments) that selects this scope's rows from event_envelopes, so a replay can filter BEFORE
// its row cap exactly as the live path filters in memory. ok is false for the synthetic summary scope, which has no stored rows.
func (s Scope) SQL() (cond string, args []interface{}, ok bool) {
	switch s.Kind {
	case kindPlatformSummary:
		return "", nil, false
	case kindChain:
		return "execution_chain_id = ?", []interface{}{s.ID}, true
	}

	prefixes := s.prefixes()
	parts := make([]string, len(prefixes))
	for i, p := range prefixes {
		parts[i] = "event_type LIKE ?"
		args = append(args, p+"%")
	}
	cond = "(" + strings.Join(parts, " OR ") + ")"
	if s.ID != "" {
		cond += " AND job_id = ?"
		args = append(args, s.ID)
	}
	return "(" + cond + ")", args, true
}

func hasAnyPrefix(s string, prefixes []string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// Subscription is the set of scopes one connection asked for.
type Subscription struct{ Scopes []Scope }

// Empty reports whether the client named no scope (the legacy, un-narrowed subscription).
func (sub Subscription) Empty() bool { return len(sub.Scopes) == 0 }

// Match returns the in-memory filter for the hub, or nil for an empty subscription.
func (sub Subscription) Match() func(Event) bool {
	if sub.Empty() {
		return nil
	}
	scopes := sub.Scopes
	return func(e Event) bool {
		for _, s := range scopes {
			if s.Matches(e) {
				return true
			}
		}
		return false
	}
}

// WantsSummary reports whether the platform.summary scope is among them.
func (sub Subscription) WantsSummary() bool {
	for _, s := range sub.Scopes {
		if s.Kind == kindPlatformSummary {
			return true
		}
	}
	return false
}

// Permissions returns the distinct extra API scopes the subscription needs.
func (sub Subscription) Permissions() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range sub.Scopes {
		if p := s.Permission(); p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

// ReplaySQL returns one condition covering every scope that has stored rows. ok is false when there is nothing to replay (no scopes,
// or only platform.summary).
func (sub Subscription) ReplaySQL() (cond string, args []interface{}, ok bool) {
	var parts []string
	for _, s := range sub.Scopes {
		c, a, has := s.SQL()
		if !has {
			continue
		}
		parts = append(parts, c)
		args = append(args, a...)
	}
	if len(parts) == 0 {
		return "", nil, false
	}
	return "(" + strings.Join(parts, " OR ") + ")", args, true
}
