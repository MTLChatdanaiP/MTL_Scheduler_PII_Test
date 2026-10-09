package lint

// RFC-000 §5: the PII package owns finding records. No other package may CONSTRUCT a models.PIIRecord (an empty `&models.PIIRecord{}`
// used to name the table in a query is fine). Checked by parsing every non-test source file under internal/.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func piiRecordConstructions(t *testing.T, filename, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		t.Fatalf("%s does not parse: %v", filename, err)
	}
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || len(lit.Elts) == 0 {
			return true
		}
		if sel, ok := lit.Type.(*ast.SelectorExpr); ok && sel.Sel.Name == "PIIRecord" {
			found = append(found, fset.Position(lit.Pos()).String())
		}
		return true
	})
	return found
}

func TestNothingOutsideThePIIPackageBuildsAFindingRecord(t *testing.T) {
	var offenders []string
	err := filepath.WalkDir("../../internal", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		slash := filepath.ToSlash(path)
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || strings.Contains(slash, "/internal/pii/") || strings.Contains(slash, "internal/pii/") {
			return nil
		}
		src, readErr := readFile(path)
		if readErr != nil {
			return readErr
		}
		offenders = append(offenders, piiRecordConstructions(t, path, src)...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("these build a PIIRecord directly; use pii.RecordFinding instead:\n  %s", strings.Join(offenders, "\n  "))
	}
}

func TestThePIIRecordLinterCatchesAConstructionAndAllowsAMarker(t *testing.T) {
	bad := "package x\nfunc f() { _ = models.PIIRecord{JobID: \"j\"} }"
	if len(piiRecordConstructions(t, "bad.go", bad)) == 0 {
		t.Fatal("a record built with fields must be caught")
	}
	if len(piiRecordConstructions(t, "bad2.go", "package x\nfunc f() { _ = &models.PIIRecord{Type: \"t\"} }")) == 0 {
		t.Fatal("a pointer to a record built with fields must be caught")
	}
	ok := "package x\nfunc f() { db.Model(&models.PIIRecord{}).Count(&n) }"
	if got := piiRecordConstructions(t, "ok.go", ok); len(got) != 0 {
		t.Fatalf("an empty literal naming the table is not a construction: %v", got)
	}
}
