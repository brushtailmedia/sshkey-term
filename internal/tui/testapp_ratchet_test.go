package tui

// Two guards on how tests build things, kept by a go/ast walk over this
// package's test files (the server repo's sleep guard works the same way).
//
// 1. Ratchet: hand-built App literals may only go DOWN. Every test App
//    should come from newTestApp (testapp_test.go); a literal builds a
//    partial App that works for one test and panics in the next. The
//    baseline is the count on the day the builder landed — lower it as
//    files migrate, never raise it.
// 2. Rule: no hand-built ServerMsg. Frames come from App.serverMsg so they
//    carry the live connection generation; gen_token_test.go is exempt
//    because it tests the stamp itself.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// appLiteralBaseline is the number of `App{…}` literals in this package's
// tests when the ratchet was set (2026-10-25: 51 before the builder, 47
// after the first seven harnesses moved). Lower it when you remove some.
// Note for new tests: reflect on App with reflect.TypeFor[App](), since
// reflect.TypeOf(App{}) spells a literal this count includes.
const appLiteralBaseline = 47

// countLiterals counts composite literals of the named type across this
// package's *_test.go files, skipping the named files.
func countLiterals(t *testing.T, typeName string, skip ...string) (int, map[string]int) {
	t.Helper()
	skipped := map[string]bool{}
	for _, s := range skip {
		skipped[s] = true
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	perFile := map[string]int{}
	total := 0
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, "_test.go") || skipped[name] {
			continue
		}
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if id, ok := lit.Type.(*ast.Ident); ok && id.Name == typeName {
				perFile[name]++
				total++
			}
			return true
		})
	}
	return total, perFile
}

func formatPerFile(perFile map[string]int) string {
	files := make([]string, 0, len(perFile))
	for f, n := range perFile {
		files = append(files, f+"="+strconv.Itoa(n))
	}
	sort.Strings(files)
	return strings.Join(files, "\n")
}

func TestTestApp_LiteralRatchet(t *testing.T) {
	total, perFile := countLiterals(t, "App", "testapp_test.go")
	if total > appLiteralBaseline {
		t.Fatalf("App{} literals in tests = %d, above the baseline %d; build test Apps with newTestApp instead.\n%s",
			total, appLiteralBaseline, formatPerFile(perFile))
	}
	if total < appLiteralBaseline {
		t.Logf("App{} literals = %d, below the baseline %d: lower appLiteralBaseline to %d", total, appLiteralBaseline, total)
	}
}

func TestTestApp_NoHandBuiltServerMsg(t *testing.T) {
	total, perFile := countLiterals(t, "ServerMsg", "testapp_test.go", "gen_token_test.go")
	if total > 0 {
		t.Fatalf("hand-built ServerMsg literals in tests = %d; use a.serverMsg(type, raw) so the frame carries the live generation.\n%s",
			total, formatPerFile(perFile))
	}
}
