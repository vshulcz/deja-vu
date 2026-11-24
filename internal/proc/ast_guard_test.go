package proc

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoRawExecCommandOutsideProc(t *testing.T) {
	t.Parallel()
	repoRoot := filepath.Join("..", "..")
	fset := token.NewFileSet()
	var checked int

	for _, root := range []string{filepath.Join(repoRoot, "cmd"), filepath.Join(repoRoot, "internal")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
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
			rel, err := filepath.Rel(repoRoot, path)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if strings.HasPrefix(rel, "internal/proc/") {
				return nil
			}
			checked++

			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", rel, err)
			}
			execAlias, dotImport := execImport(file)
			if dotImport {
				t.Errorf("%s: dot-import of os/exec is forbidden", rel)
				return nil
			}
			if execAlias == "" {
				return nil
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					if id, ok := sel.X.(*ast.Ident); ok && id.Name == execAlias {
						if sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext" {
							pos := fset.Position(call.Pos())
							t.Errorf("%s:%d: raw %s.%s forbidden outside internal/proc; use proc.%s (or proc.InteractiveCommandContext for foreground TTY)",
								rel, pos.Line, execAlias, sel.Sel.Name, sel.Sel.Name)
						}
					}
				}
				return true
			})
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

func TestProcConstructorsApplyHideconsole(t *testing.T) {
	t.Parallel()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "proc.go", nil, 0)
	if err != nil {
		t.Fatalf("parse proc.go: %v", err)
	}
	hideAlias := ""
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) == "github.com/vshulcz/deja-vu/internal/hideconsole" {
			if imp.Name != nil {
				hideAlias = imp.Name.Name
			} else {
				hideAlias = "hideconsole"
			}
		}
	}
	if hideAlias == "" {
		t.Fatal("proc.go must import internal/hideconsole")
	}

	seen := map[string]bool{}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || fn.Recv != nil {
			continue
		}
		name := fn.Name.Name
		if name != "Command" && name != "CommandContext" && name != "InteractiveCommandContext" {
			continue
		}
		seen[name] = true
		applied := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == hideAlias && sel.Sel.Name == "Apply" {
					applied = true
				}
			}
			return true
		})
		if name == "InteractiveCommandContext" && applied {
			t.Errorf("InteractiveCommandContext must not call hideconsole.Apply")
		}
		if name != "InteractiveCommandContext" && !applied {
			t.Errorf("%s must call hideconsole.Apply", name)
		}
	}
	for _, want := range []string{"Command", "CommandContext", "InteractiveCommandContext"} {
		if !seen[want] {
			t.Errorf("missing proc.%s", want)
		}
	}
}

func execImport(file *ast.File) (alias string, dot bool) {
	for _, imp := range file.Imports {
		if strings.Trim(imp.Path.Value, `"`) != "os/exec" {
			continue
		}
		if imp.Name == nil {
			return "exec", false
		}
		if imp.Name.Name == "." {
			return "", true
		}
		if imp.Name.Name == "_" {
			return "", false
		}
		return imp.Name.Name, false
	}
	return "", false
}
