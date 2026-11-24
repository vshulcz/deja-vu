package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var allowForegroundExecFuncs = map[string]map[string]bool{
	"handoff.go":       {"runHandoff": true},
	"help_commands.go": {"runToolHelp": true},
	"install_aider.go": {"cmdAider": true},
	"install_goose.go": {"cmdGoose": true},
	"proc.go":          {"InteractiveCommandContext": true},
	"resume.go":        {"runResume": true},
	"shell_layer.go":   {"runShellCommand": true},
}

var skipDetachedExecFiles = map[string]bool{}

func TestBackgroundExecCallsApplyHideconsole(t *testing.T) {
	t.Parallel()
	roots := []string{
		filepath.Join("..", "..", "cmd"),
		filepath.Join("..", "..", "internal"),
	}
	var checked int
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				name := d.Name()
				if name == "testdata" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			name := d.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				return nil
			}
			if skipDetachedExecFiles[name] {
				return nil
			}
			checked++
			checkBackgroundHideconsoleFile(t, path, allowForegroundExecFuncs[name])
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if checked == 0 {
		t.Fatal("walked zero non-test Go files")
	}
}

func checkBackgroundHideconsoleFile(t *testing.T, path string, allowFuncs map[string]bool) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	if hasNonWindowsBuildTag(file) {
		return
	}
	execAlias := pkgImportAlias(file, "os/exec", "exec")
	if execAlias == "" {
		return
	}
	hideAlias := pkgImportAlias(file, "github.com/vshulcz/deja-vu/internal/hideconsole", "hideconsole")

	ast.Inspect(file, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil && !allowFuncs[fn.Name.Name] {
				checkBackgroundHideconsoleBody(t, fset, path, execAlias, hideAlias, fn.Body)
			}
		case *ast.FuncLit:
			if fn.Body != nil {
				checkBackgroundHideconsoleBody(t, fset, path, execAlias, hideAlias, fn.Body)
			}
		}
		return true
	})
}

func hasNonWindowsBuildTag(file *ast.File) bool {
	for _, cg := range file.Comments {
		if cg.Pos() >= file.Package {
			continue
		}
		for _, c := range cg.List {
			if strings.HasPrefix(c.Text, "//go:build ") && strings.Contains(c.Text, "!windows") {
				return true
			}
		}
	}
	return false
}

func checkBackgroundHideconsoleBody(t *testing.T, fset *token.FileSet, path, execAlias, hideAlias string, body *ast.BlockStmt) {
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

func TestRxRunHookExecutesChild(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv(hookEchoEnv, "1")
	out, err := rxRunHook(t.Context(), exe, []string{"hook-prompt"}, []byte("stdin-payload"))
	if err != nil {
		t.Fatalf("rxRunHook: %v", err)
	}
	want := "hook-prompt\nstdin-payload"
	if out != want {
		t.Fatalf("rxRunHook output = %q, want %q", out, want)
	}
}
