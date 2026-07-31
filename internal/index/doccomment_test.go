package index

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Twice in one day a constant was added between a doc block and the function
// it documented, so Go attached the whole comment to the constant and the
// function lost its documentation entirely. Both compiled, both vetted clean,
// and both read fine in the source — the comment sits above *something*, and
// the eye does not notice that the something changed. It only shows in
// `go doc`, which nobody runs for unexported identifiers (#645).
//
// This is checkable because the repo follows one convention without exception:
// a doc block starts with the name of what it documents. So a block whose
// first word names a different declaration is a comment that drifted.
func TestDocCommentsNameWhatTheyDocument(t *testing.T) {
	roots := []string{"..", filepath.Join("..", "..", "cmd")}
	seen := 0
	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			// Other tests create and delete scratch trees while this walks, so
			// a vanished entry is normal rather than a failure.
			if err != nil {
				return nil //nolint:nilerr // a file that disappeared mid-walk is not this test's business
			}
			if info.IsDir() {
				if strings.HasPrefix(info.Name(), ".") && info.Name() != "." && info.Name() != ".." {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if perr != nil {
				return nil // not this test's business
			}
			for _, decl := range f.Decls {
				name, doc := declNameAndDoc(decl)
				if name == "" || doc == nil || len(doc.List) == 0 {
					continue
				}
				first := firstWord(doc.Text())
				if first == "" || first == name {
					continue
				}
				seen++
				// Only flag when the first word names some *other* declaration
				// in the same file: prose legitimately starts with any word.
				if declaredIn(f, first) {
					t.Errorf("%s: doc block above %q begins with %q, which is declared elsewhere in this file — the comment drifted off its declaration (`go doc -u` will show it on the wrong one)",
						fset.Position(decl.Pos()), name, first)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if seen == 0 {
		t.Fatal("walked no doc comments at all; the check is not looking at anything")
	}
}

func declNameAndDoc(d ast.Decl) (string, *ast.CommentGroup) {
	switch v := d.(type) {
	case *ast.FuncDecl:
		return v.Name.Name, v.Doc
	case *ast.GenDecl:
		// A grouped declaration's doc block describes the group, not its first
		// member: "// Event kinds." above a const block of six is correct and
		// naming a type that happens to exist is a coincidence.
		if len(v.Specs) != 1 {
			return "", nil
		}
		switch s := v.Specs[0].(type) {
		case *ast.TypeSpec:
			return s.Name.Name, v.Doc
		case *ast.ValueSpec:
			if len(s.Names) > 0 {
				return s.Names[0].Name, v.Doc
			}
		}
	}
	return "", nil
}

func firstWord(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], "`*_,.:;()")
}

func declaredIn(f *ast.File, name string) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.FuncDecl:
			if v.Name.Name == name {
				found = true
			}
		case *ast.TypeSpec:
			if v.Name.Name == name {
				found = true
			}
		case *ast.ValueSpec:
			for _, id := range v.Names {
				if id.Name == name {
					found = true
				}
			}
		}
		return !found
	})
	return found
}
