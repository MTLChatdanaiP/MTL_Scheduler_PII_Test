package lint

// Rules about internal/routes/route.go, checked by PARSING the file (standard library only), so they run without compiling Gin
// and keep running after someone edits the routes.
//
//  1. every POST / PATCH / PUT / DELETE route carries auth.RequireScope(...)
//  2. the scope on such a route is not a ".read" scope (a read-only key must not be able to change anything)
//  3. DELETE /debug/reset is registered only inside `if config.DebugEndpointsEnabled() { ... }`

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

var mutating = map[string]bool{"POST": true, "PATCH": true, "PUT": true, "DELETE": true}

func lintRoutes(t *testing.T, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "route.go", src, 0)
	if err != nil {
		t.Fatalf("route.go does not parse: %v", err)
	}

	// the source ranges of every `if config.DebugEndpointsEnabled() { ... }` body
	var gated [][2]token.Pos
	ast.Inspect(file, func(n ast.Node) bool {
		if ifs, ok := n.(*ast.IfStmt); ok {
			if call, ok := ifs.Cond.(*ast.CallExpr); ok && selector(call.Fun) == "config.DebugEndpointsEnabled" {
				gated = append(gated, [2]token.Pos{ifs.Body.Pos(), ifs.Body.End()})
			}
		}
		return true
	})
	inGate := func(p token.Pos) bool {
		for _, g := range gated {
			if p >= g[0] && p <= g[1] {
				return true
			}
		}
		return false
	}

	var problems []string
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !mutating[sel.Sel.Name] || len(call.Args) < 2 {
			return true
		}
		path := ""
		if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
			path, _ = strconv.Unquote(lit.Value)
		} else {
			return true // not a route registration
		}
		where := sel.Sel.Name + " " + path

		scope := ""
		for _, a := range call.Args[1:] {
			if c, ok := a.(*ast.CallExpr); ok && selector(c.Fun) == "auth.RequireScope" && len(c.Args) == 1 {
				if lit, ok := c.Args[0].(*ast.BasicLit); ok {
					scope, _ = strconv.Unquote(lit.Value)
				}
			}
		}
		switch {
		case scope == "":
			problems = append(problems, where+": no auth.RequireScope(...)")
		case strings.HasSuffix(scope, ".read"):
			problems = append(problems, where+": a "+scope+" scope on a route that changes data")
		}
		if path == "/debug/reset" && !inGate(call.Pos()) {
			problems = append(problems, where+": not inside `if config.DebugEndpointsEnabled()`")
		}
		return true
	})
	return problems
}

func selector(e ast.Expr) string {
	if s, ok := e.(*ast.SelectorExpr); ok {
		if x, ok := s.X.(*ast.Ident); ok {
			return x.Name + "." + s.Sel.Name
		}
	}
	return ""
}

func TestRoutePolicy_TheRealRouteFileObeysTheRules(t *testing.T) {
	data, err := os.ReadFile("../../internal/routes/route.go")
	if err != nil {
		t.Fatalf("cannot read route.go: %v", err)
	}
	if problems := lintRoutes(t, string(data)); len(problems) > 0 {
		t.Fatalf("route.go breaks the route policy:\n  %s", strings.Join(problems, "\n  "))
	}
}

func TestRoutePolicy_TheLinterCatchesEachBreach(t *testing.T) {
	cases := map[string]string{
		"a route with no scope": `package routes
func f(r R) { r.POST("/x", handlers.X) }`,
		"a write route behind a read scope": `package routes
func f(r R) { r.PATCH("/x", auth.RequireScope("job.read"), handlers.X) }`,
		"an ungated debug route": `package routes
func f(r R) { r.DELETE("/debug/reset", auth.RequireScope("debug.reset"), handlers.X) }`,
		"a debug route in the wrong if": `package routes
func f(r R) { if other.Thing() { r.DELETE("/debug/reset", auth.RequireScope("debug.reset"), handlers.X) } }`,
	}
	for name, src := range cases {
		if len(lintRoutes(t, src)) == 0 {
			t.Errorf("%s: the linter missed it", name)
		}
	}

	good := `package routes
func f(r R) {
	r.GET("/x", auth.RequireScope("job.read"), handlers.X)
	r.POST("/y", auth.RequireScope("job.write"), handlers.Y)
	if config.DebugEndpointsEnabled() { r.DELETE("/debug/reset", auth.RequireScope("debug.reset"), handlers.Z) }
}`
	if problems := lintRoutes(t, good); len(problems) > 0 {
		t.Errorf("false positives on a compliant file: %v", problems)
	}
}

func readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	return string(data), err
}
