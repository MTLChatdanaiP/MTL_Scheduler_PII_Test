package lint

// RFC-010 §6 Context Boundary: Real-Time Monitoring "owns subscription semantics, delivery, backpressure"; it does NOT own durable facts,
// projections or snapshots. In this code base that boundary is enforced by one rule: the live package imports no other package of this
// module, so it cannot read a table, a projection or a model. Everything it needs reaches it as an Event value from the layer above.

import (
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestLivePackageImportsNothingFromThisModule(t *testing.T) {
	var offenders []string
	err := filepath.WalkDir("../../internal/live", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if parseErr != nil {
			t.Fatalf("%s does not parse: %v", path, parseErr)
		}
		for _, imp := range file.Imports {
			if strings.Contains(imp.Path.Value, "MTL_Scheduler_PII_Test/") {
				offenders = append(offenders, filepath.ToSlash(path)+" imports "+imp.Path.Value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("RFC-010 §6: the live package must not depend on durable-fact or projection packages:\n  %s", strings.Join(offenders, "\n  "))
	}
}
