package live

import (
	"fmt"
	"strings"
)

// RFC-010 §25: a live event must not reach a caller who could not read the
// same information through the REST API.
type eventScopeRule struct {
	Prefix string
	Scope  string
}

var eventScopeRules = []eventScopeRule{
	{Prefix: "alert.", Scope: "alerts.read"},
	{Prefix: "pii.policy", Scope: "pii.policy.read"},
	{Prefix: "pii.", Scope: "pii.findings.read"},
}

// requiredScope returns the extra scope needed to see an event type, or "" if
// the route's own scope is enough (task.* and anything unclassified).
func RequiredScope(eventType string) string {
	for _, r := range eventScopeRules {
		if strings.HasPrefix(eventType, r.Prefix) {
			return r.Scope
		}
	}
	return ""
}

// hiddenEventSQL returns a SQL condition that matches every event_type the
// caller may NOT see, or "" when nothing is hidden from them. A rule is only
// applied to types that no EARLIER rule claims, mirroring "first match wins"
// in requiredScope. Prefixes are compile-time constants, never user input.
func HiddenEventSQL(has func(scope string) bool) string {
	var hidden []string

	for i, r := range eventScopeRules {
		if has(r.Scope) {
			continue
		}

		cond := fmt.Sprintf("event_type LIKE '%s%%'", r.Prefix)
		for _, earlier := range eventScopeRules[:i] {
			cond += fmt.Sprintf(" AND event_type NOT LIKE '%s%%'", earlier.Prefix)
		}
		hidden = append(hidden, "("+cond+")")
	}

	return strings.Join(hidden, " OR ")
}
