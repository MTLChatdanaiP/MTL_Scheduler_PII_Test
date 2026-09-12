package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
)

type Principal struct {
	Name   string   `json:"name"`
	Key    string   `json:"key"`
	Scopes []string `json:"scopes"`
}

func (p Principal) HasScope(scope string) bool {
	for _, s := range p.Scopes {
		if s == scope {
			return true
		}
	}
	return false
}

var principals map[string]Principal // keyed by Principal.Key

func LoadPrincipals(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading principal config: %w", err)
	}

	var list []Principal
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("parsing principal config: %w", err)
	}

	m := make(map[string]Principal, len(list))
	for _, p := range list {
		m[p.Key] = p
	}
	principals = m
	return nil
}

func lookup(key string) (Principal, bool) {
	p, ok := principals[key]
	return p, ok
}

func RequireScope(scope string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader("X-API-Key")

		principal, ok := lookup(key)
		if !ok || !principal.HasScope(scope) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"error": "missing scope: " + scope,
			})
			return
		}

		c.Set("actor", principal.Name) // read back later via c.GetString("actor")
		c.Next()
	}
}
