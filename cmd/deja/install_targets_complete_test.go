package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// installTargetNames feeds `deja help`, shell completion, doctor's coverage
// check, and the expansion uninstall uses to reach hooks. A target implemented
// in installTarget but missing from that list is therefore invisible in all
// four at once, and nothing noticed: grok-auto shipped that way, so
// `uninstall --all` left ~/.grok/hooks/deja.json behind pointing at a binary
// the user had just removed, and doctor never checked it.
//
// The other direction is already covered — the help test asserts every listed
// name appears in the help text. This is the missing half: every name the
// switch answers to must be listed.
func TestEveryImplementedTargetIsListed(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "install.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, n := range installTargetNames() {
		listed[n] = true
	}

	var fn *ast.FuncDecl
	ast.Inspect(file, func(n ast.Node) bool {
		if d, ok := n.(*ast.FuncDecl); ok && d.Name.Name == "installTarget" {
			fn = d
			return false
		}
		return true
	})
	if fn == nil {
		t.Fatal("installTarget not found in install.go; this test needs updating")
	}

	// One listed name per clause is enough: several names sharing a body are
	// aliases of the same install, and advertising every spelling would only
	// pad the help. What must never happen is a clause none of whose names is
	// listed — that is a whole install path nothing can see.
	found := 0
	ast.Inspect(fn, func(n ast.Node) bool {
		clause, ok := n.(*ast.CaseClause)
		if !ok || len(clause.List) == 0 {
			return true
		}
		var names []string
		for _, expr := range clause.List {
			lit, ok := expr.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				continue
			}
			name, err := strconv.Unquote(lit.Value)
			if err == nil && name != "" {
				names = append(names, name)
			}
		}
		if len(names) == 0 {
			return true
		}
		found++
		for _, name := range names {
			if listed[name] {
				return true
			}
		}
		t.Errorf("installTarget answers to %v but installTargetNames() lists none of them: "+
			"help, completion, doctor and uninstall --all all miss that install", names)
		return true
	})
	if found == 0 {
		t.Fatal("no case clauses read out of installTarget; this test needs updating")
	}
}
