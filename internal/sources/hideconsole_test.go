package sources

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestSourcesExecCallsApplyHideconsole(t *testing.T) {
	t.Parallel()
	dirs := []string{".", "../gitread"}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, entry := range entries {
			name := entry.Name()
			if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			checkHideconsoleFile(t, filepath.Join(dir, name))
		}
	}
}

func checkHideconsoleFile(t *testing.T, path string) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	execAlias := pkgImportAlias(file, "os/exec", "exec")
	if execAlias == "" {
		return
	}
	hideAlias := pkgImportAlias(file, "github.com/vshulcz/deja-vu/internal/hideconsole", "hideconsole")

	ast.Inspect(file, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil {
				checkHideconsoleBody(t, fset, path, execAlias, hideAlias, fn.Body)
			}
		case *ast.FuncLit:
			if fn.Body != nil {
				checkHideconsoleBody(t, fset, path, execAlias, hideAlias, fn.Body)
			}
		}
		return true
	})
}

func checkHideconsoleBody(t *testing.T, fset *token.FileSet, path, execAlias, hideAlias string, body *ast.BlockStmt) {
	t.Helper()
	var execCalls []*ast.CallExpr
	hideCount := 0
	ast.Inspect(body, func(n ast.Node) bool {
		if _, isLit := n.(*ast.FuncLit); isLit {
			return false
		}
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if call, ok := sel.X.(*ast.CallExpr); ok && isPkgCall(call, execAlias, "Command", "CommandContext") {
				pos := fset.Position(call.Pos())
				t.Errorf("%s:%d: %s result chained without hideconsole.Apply", path, pos.Line, sel.Sel.Name)
			}
		}
		if call, ok := n.(*ast.CallExpr); ok {
			if isPkgCall(call, execAlias, "Command", "CommandContext") {
				execCalls = append(execCalls, call)
			} else if hideAlias != "" && isPkgCall(call, hideAlias, "Apply") {
				hideCount++
			}
		}
		return true
	})
	if len(execCalls) > hideCount {
		for _, call := range execCalls {
			pos := fset.Position(call.Pos())
			t.Errorf("%s:%d: exec call without matching hideconsole.Apply (%d exec calls, %d Apply calls in function)", path, pos.Line, len(execCalls), hideCount)
		}
	}
}

func pkgImportAlias(file *ast.File, importPath, defaultName string) string {
	for _, imp := range file.Imports {
		val := strings.Trim(imp.Path.Value, `"`)
		if val != importPath {
			continue
		}
		if imp.Name == nil {
			return defaultName
		}
		if imp.Name.Name == "_" || imp.Name.Name == "." {
			return ""
		}
		return imp.Name.Name
	}
	return ""
}

func isPkgCall(call *ast.CallExpr, alias string, names ...string) bool {
	if alias == "" {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok || ident.Name != alias {
		return false
	}
	return slices.Contains(names, sel.Sel.Name)
}
