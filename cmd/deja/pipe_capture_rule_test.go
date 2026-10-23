package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A test that captures output through os.Pipe has to drain it while the code
// under test writes, not after it returns. A pipe holds one buffer — 64 KB on
// Linux, 4 KB on Windows — and a writer that fills it blocks until somebody
// reads, so a capture that reads afterwards deadlocks the moment the output
// grows past that. It did: the windows leg hung when the harness table crossed
// 4096 bytes, and it hung on main rather than on the pull request, because a
// change that grows printed output is not one anybody labels `windows` (#3493,
// #3504, #3505).
//
// The rule is mechanical, so it is checked mechanically: where a test hands the
// write end of a pipe to the code under test, the read of the other end has to
// happen off the calling path — in a goroutine, or through a helper that starts
// one. That is what every capture helper here already does.
func TestNoTestReadsAPipeAfterTheCallThatFillsIt(t *testing.T) {
	files := parseTestFiles(t, ".", filepath.Join("..", "..", "internal"))
	if len(files) == 0 {
		t.Fatal("no test files were checked, so the rule was not applied to anything")
	}
	drainers := drainingHelpers(files)
	for path, parsed := range files {
		checkPipeReads(t, path, parsed.fset, parsed.file, drainers)
	}
}

type parsedFile struct {
	fset *token.FileSet
	file *ast.File
}

func parseTestFiles(t *testing.T, roots ...string) map[string]parsedFile {
	t.Helper()
	out := map[string]parsedFile{}
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			// Other tests build and remove scratch trees while this walks.
			if err != nil {
				return nil //nolint:nilerr // a file that vanished mid-walk is not this test's business
			}
			if info.IsDir() {
				if strings.HasPrefix(info.Name(), ".") && info.Name() != "." && info.Name() != ".." {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			file, perr := parser.ParseFile(fset, path, nil, 0)
			if perr != nil {
				t.Fatalf("%s: %v", path, perr)
			}
			out[path] = parsedFile{fset: fset, file: file}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

// drainingHelpers names the functions that take a pipe and read it in a
// goroutine — handing a reader to one of those is the safe shape, not a read on
// the calling path.
func drainingHelpers(files map[string]parsedFile) map[string]bool {
	out := map[string]bool{}
	for _, parsed := range files {
		ast.Inspect(parsed.file, func(n ast.Node) bool {
			fn, ok := n.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Type.Params == nil {
				return true
			}
			params := map[string]bool{}
			for _, p := range fn.Type.Params.List {
				for _, name := range p.Names {
					params[name.Name] = true
				}
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				goStmt, ok := n.(*ast.GoStmt)
				if !ok {
					return true
				}
				ast.Inspect(goStmt.Call, func(n ast.Node) bool {
					if ident, ok := n.(*ast.Ident); ok && params[ident.Name] {
						out[fn.Name.Name] = true
					}
					return true
				})
				return false
			})
			return true
		})
	}
	return out
}

func checkPipeReads(t *testing.T, _ string, fset *token.FileSet, file *ast.File, drainers map[string]bool) {
	t.Helper()
	ast.Inspect(file, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			return true
		}
		reader, writer := pipeEnds(fn.Body)
		if reader == "" || writer == "" {
			return true
		}
		// A pipe the test fills itself and closes before the call is standing
		// in for a file, not capturing output: it cannot block, because the
		// writing is the test's own and it has already happened. The rule is
		// about the other shape — the write end handed to the code under test.
		if !handsOver(fn.Body, writer) {
			return true
		}
		var offending token.Pos
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.GoStmt:
				// Everything the goroutine does is off the calling path, which
				// is where a read of this pipe belongs.
				return false
			case *ast.CallExpr:
				if ident, ok := node.Fun.(*ast.Ident); ok && drainers[ident.Name] {
					return false
				}
				if readsFrom(node, reader) && offending == 0 {
					offending = node.Pos()
				}
			}
			return true
		})
		if offending != 0 {
			t.Errorf("%s: %s reads %s on the calling path — drain it in a goroutine or hand it to a helper that does, or the write blocks once it fills the buffer",
				fset.Position(offending), fn.Name.Name, reader)
		}
		return true
	})
}

// pipeEnds returns the variables the function assigned the two ends of an
// os.Pipe to, or empty strings when it makes no pipe.
func pipeEnds(body *ast.BlockStmt) (reader, writer string) {
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 || len(assign.Lhs) < 2 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok || !isSelector(call.Fun, "os", "Pipe") {
			return true
		}
		if ident, ok := assign.Lhs[0].(*ast.Ident); ok && ident.Name != "_" {
			reader = ident.Name
		}
		if ident, ok := assign.Lhs[1].(*ast.Ident); ok && ident.Name != "_" {
			writer = ident.Name
		}
		return false
	})
	return reader, writer
}

// handsOver reports whether the write end leaves the test: put on os.Stdout or
// os.Stderr, or passed to something that will write to it. Writing to it and
// closing it in the test itself is not handing it over.
func handsOver(body *ast.BlockStmt, writer string) bool {
	handed := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range node.Lhs {
				if isSelector(lhs, "os", "Stdout") || isSelector(lhs, "os", "Stderr") {
					for _, rhs := range node.Rhs {
						if ident, ok := rhs.(*ast.Ident); ok && ident.Name == writer {
							handed = true
						}
					}
				}
			}
		case *ast.CallExpr:
			if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
				if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == writer {
					// w.WriteString(...) and w.Close() are the test's own use.
					return true
				}
			}
			for _, arg := range node.Args {
				if ident, ok := arg.(*ast.Ident); ok && ident.Name == writer {
					handed = true
				}
			}
		}
		return true
	})
	return handed
}

// readsFrom reports whether the call takes the pipe's reader as what it reads:
// io.ReadAll(r), io.Copy(dst, r), buf.ReadFrom(r), bufio.NewScanner(r).
func readsFrom(call *ast.CallExpr, reader string) bool {
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Close" {
		return false
	}
	for _, arg := range call.Args {
		if ident, ok := arg.(*ast.Ident); ok && ident.Name == reader {
			return true
		}
	}
	if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == reader {
			// r.Read(...) and friends, on the reader itself.
			return sel.Sel.Name != "Close" && sel.Sel.Name != "Fd" && sel.Sel.Name != "Name"
		}
	}
	return false
}

func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}
