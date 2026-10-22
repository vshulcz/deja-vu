package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"runtime"
	"strconv"
	"testing"
)

// installTargetNames feeds `deja help`, shell completion, doctor's coverage
// check, and the expansion uninstall uses to reach hooks. A target implemented
// in installTargetCase but missing from that list is therefore invisible in all
// four at once, and nothing noticed: grok-auto shipped that way, so
// `uninstall --all` left ~/.grok/hooks/deja.json behind pointing at a binary
// the user had just removed, and doctor never checked it.
//
// The other direction is already covered — the help test asserts every listed
// name appears in the help text. This is the missing half: every name the
// table answers to must be listed.
func TestEveryImplementedTargetIsListed(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "install_target_cases.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, n := range installTargetNames() {
		listed[n] = true
	}

	var fn *ast.FuncDecl
	ast.Inspect(file, func(n ast.Node) bool {
		if d, ok := n.(*ast.FuncDecl); ok && d.Name.Name == "installTargetCase" {
			fn = d
			return false
		}
		return true
	})
	if fn == nil {
		t.Fatal("installTargetCase not found in install_target_cases.go; this test needs updating")
	}

	// One listed name per writer is enough: several names sharing a writer are
	// aliases of the same install, and advertising every spelling would only
	// pad the help. What must never happen is a writer none of whose names is
	// listed — that is a whole install path nothing can see.
	var groups [][]string
	byWriter := map[string]int{}
	ast.Inspect(fn, func(n ast.Node) bool {
		kv, ok := n.(*ast.KeyValueExpr)
		if !ok {
			return true
		}
		lit, ok := kv.Key.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		name, err := strconv.Unquote(lit.Value)
		if err != nil || name == "" {
			return true
		}
		if id, ok := kv.Value.(*ast.Ident); ok {
			if g, seen := byWriter[id.Name]; seen {
				groups[g] = append(groups[g], name)
				return true
			}
			byWriter[id.Name] = len(groups)
		}
		groups = append(groups, []string{name})
		return true
	})
	found := len(groups)
	for _, names := range groups {
		ok := false
		for _, name := range names {
			if listed[name] {
				ok = true
				break
			}
			// A target can be implemented everywhere and offered only where it
			// works. sync-timer answers on every platform — with an
			// explanation naming the one it cannot schedule for, which is
			// better than "unknown target" — but it is listed only where deja
			// can write a unit, because the list is what `--all` installs.
			if name == "sync-timer" && !syncTimerSchedulable(runtime.GOOS) {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("installTarget answers to %v but installTargetNames() lists none of them: "+
				"help, completion, doctor and uninstall --all all miss that install", names)
		}
	}
	if found == 0 {
		t.Fatal("no targets read out of installTargetCase; this test needs updating")
	}
}
