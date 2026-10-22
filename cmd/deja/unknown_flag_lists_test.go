package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A suggestion is only as good as the list it is drawn from, and the list sits
// in a different place from the parser that reads the flags. A flag added to a
// parser and not to its list costs a wrong suggestion — "did you mean --limit?"
// for a flag the command now takes — which is worse than no suggestion at all,
// and nothing else would catch it.
//
// So: whatever flag tokens a converted parser mentions, its list has to name.
func TestEachFlagListNamesEveryFlagItsParserTakes(t *testing.T) {
	files := parsePackageSources(t)

	lists := map[string][]string{}
	for _, f := range files {
		for _, d := range f.Decls {
			gen, ok := d.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != 1 || len(vs.Values) != 1 {
					continue
				}
				if v, ok := stringSliceLiteral(vs.Values[0]); ok {
					lists[vs.Names[0].Name] = v
				}
			}
		}
	}

	flagToken := regexp.MustCompile(`^--[a-z][a-z0-9-]*$`)
	seen := 0
	for _, f := range files {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			listName := unknownFlagListArg(fn)
			if listName == "" {
				continue
			}
			known, ok := lists[listName]
			if !ok {
				t.Errorf("%s passes %s to unknownFlag, which is not a slice literal of flags", fn.Name.Name, listName)
				continue
			}
			seen++
			for _, lit := range stringLiterals(fn.Body) {
				if !flagToken.MatchString(lit) || contains(known, lit) {
					continue
				}
				t.Errorf("%s takes %s but %s does not list it — a near miss would be told to use something else",
					fn.Name.Name, lit, listName)
			}
		}
	}
	// A rename or a deleted call site would otherwise leave this test passing
	// over nothing at all.
	if seen < 10 {
		t.Errorf("found only %d converted parsers; the helper was meant to cover eleven commands plus search", seen)
	}
}

// parsePackageSources reads this package's own non-test sources. One file at a
// time rather than parser.ParseDir, which is deprecated and which would also
// pull the tests in.
func parsePackageSources(t *testing.T) []*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		t.Fatal("no package sources parsed")
	}
	return files
}

// unknownFlagListArg names the flag list a function passes to unknownFlag, or
// "" when it does not call it.
func unknownFlagListArg(fn *ast.FuncDecl) string {
	name := ""
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || id.Name != "unknownFlag" || len(call.Args) != 3 {
			return true
		}
		if arg, ok := call.Args[2].(*ast.Ident); ok {
			name = arg.Name
		}
		return true
	})
	return name
}

func stringSliceLiteral(e ast.Expr) ([]string, bool) {
	comp, ok := e.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	arr, ok := comp.Type.(*ast.ArrayType)
	if !ok {
		return nil, false
	}
	if id, ok := arr.Elt.(*ast.Ident); !ok || id.Name != "string" {
		return nil, false
	}
	out := []string{}
	for _, el := range comp.Elts {
		lit, ok := el.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return nil, false
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// stringLiterals is the set of string constants written inside a node, each
// reported once: a flag a parser names in two places is one omission, not two.
func stringLiterals(n ast.Node) []string {
	seen := map[string]bool{}
	var out []string
	ast.Inspect(n, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil || seen[v] {
			return true
		}
		seen[v] = true
		out = append(out, v)
		return true
	})
	sort.Strings(out)
	return out
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}
