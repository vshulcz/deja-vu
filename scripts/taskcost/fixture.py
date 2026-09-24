"""The repository and the history a task-cost run is measured on.

day0compare asks what a tool can answer the minute it is installed. This asks
the other half: what finishing one piece of work costs on a machine whose
history already holds the answer. The repository is big enough that reading it
is the expensive part — 40 packages of 8 files each — and the answer is in two
places an agent will not guess, a build tag and an environment variable, with a
stale document pointing at the wrong variable name.

    python3 scripts/taskcost/fixture.py /tmp/taskcost

writes <dir>/project (the repository) and <dir>/history (two prior Claude Code
sessions, in the real ~/.claude/projects layout, that did this work here and
recorded the command that passed).
"""

import json
import os
import pathlib
import sys

PACKAGES = 40
FILES_PER_PACKAGE = 8

PART = '''package {pkg}

import "strings"

// Rule{n} is one of the routing rules this service applies in order. The
// comment is here because the file has to be worth opening: a repository of
// empty stubs is not a repository an agent spends reads on.
type Rule{n} struct {{
	Name   string
	Fields []string
}}

func NewRule{n}(name string, fields ...string) *Rule{n} {{
	return &Rule{n}{{Name: name, Fields: fields}}
}}

// Apply joins the fields the rule selected. The store package holds the golden
// files these rules are checked against.
func (r *Rule{n}) Apply(in map[string]string) string {{
	var out []string
	for _, f := range r.Fields {{
		if v, ok := in[f]; ok {{
			out = append(out, f+"="+v)
		}}
	}}
	return strings.Join(out, "&")
}}
'''

STORE = {
    "store.go": '''package store

import (
	"os"
	"path/filepath"
)

type Store struct{ root string }

func Open() (*Store, error) {
	dir, err := fixtureDir()
	if err != nil {
		return nil, err
	}
	return &Store{root: dir}, nil
}

func (s *Store) Golden() ([]byte, error) {
	return os.ReadFile(filepath.Join(s.root, "store.golden"))
}
''',
    "env.go": '''package store

// fixtureEnv is the variable CI sets before running the suite.
const fixtureEnv = "SVC_FIXTURES"
''',
    "fixtures.go": '''package store

import (
	"errors"
	"os"
	"path/filepath"
)

// fixtureDir resolves the directory the golden files live in. The path is not
// relative to the package: the suite runs from several working directories in
// CI, so the location is passed in from the environment instead.
func fixtureDir() (string, error) {
	root := os.Getenv(fixtureEnv)
	p := filepath.Join(root, "store.golden")
	if _, err := os.Stat(p); err != nil {
		return "", errors.New("open " + p + ": no such file or directory")
	}
	return root, nil
}
''',
    "store_test.go": '''//go:build golden

package store

import "testing"

func TestOpen(t *testing.T) {
	s, err := Open()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Golden(); err != nil {
		t.Fatal(err)
	}
}
''',
}

# The document is stale on purpose: FIXTURES= is not the variable the code
# reads, and an agent that trusts it spends a run finding that out.
TESTING_MD = '''# Running the suite

    make test FIXTURES=./fixtures

Regenerate the golden files with `go run ./cmd/svc -regen` if they drift. CI
runs the same target on every push.
'''

MAKEFILE = '''.PHONY: test build
test:
\t@go test ./... $(GOFLAGS_EXTRA)
build:
\t@go build ./...
'''

WINNING = 'SVC_FIXTURES=$PWD/fixtures GOFLAGS_EXTRA="-tags golden" make test'


def write(path: pathlib.Path, text: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(text)


def project(root: pathlib.Path) -> None:
    write(root / "go.mod", "module example.com/svc\n\ngo 1.22\n")
    write(root / "Makefile", MAKEFILE)
    write(root / "cmd" / "svc" / "main.go", 'package main\n\nimport "fmt"\n\nfunc main() { fmt.Println("svc") }\n')
    write(root / "docs" / "TESTING.md", TESTING_MD)
    write(root / "fixtures" / "store.golden", "golden-v1\n")
    for name, body in STORE.items():
        write(root / "internal" / "store" / name, body)
    for p in range(1, PACKAGES + 1):
        for f in range(1, FILES_PER_PACKAGE + 1):
            write(root / "internal" / f"svc{p}" / f"part{f}.go", PART.format(pkg=f"svc{p}", n=f))


def history(root: pathlib.Path, project_dir: str) -> None:
    """Two sessions that solved this here, in the Claude Code layout.

    They are what a machine that has worked in a repository has, and what its
    first day does not: the reads that found the build tag and the variable, and
    the command that finally passed.
    """
    slug = "-" + project_dir.strip("/").replace("/", "-")
    out = root / ".claude" / "projects" / slug
    out.mkdir(parents=True, exist_ok=True)
    for sid, day in (("5d2ff901-prior", "08-19"), ("8ab41c37-prior", "09-02")):
        at = lambda hh, mm: f"2026-{day}T{hh:02d}:{mm:02d}:00.000Z"  # noqa: E731
        rows = [
            {"type": "user", "sessionId": sid, "timestamp": at(11, 40), "cwd": project_dir,
             "message": {"role": "user", "content": "the golden test in internal/store never runs, make test just says no test files"}},
            # The conclusion is written before the tool calls on purpose: this is
            # how a transcript reads back, the summary line stamped when the
            # session ended and the calls stamped as they ran.
            {"type": "assistant", "sessionId": sid, "timestamp": at(11, 46), "cwd": project_dir,
             "message": {"role": "assistant", "content":
                 "store_test.go sits behind the golden build tag, so the default go test skips the package, and the suite also "
                 "reads its fixture directory from SVC_FIXTURES. Both together: " + WINNING + " — green. docs/TESTING.md is "
                 "stale, the FIXTURES=... variable it names is not the one the code reads."}},
            {"type": "assistant", "sessionId": sid, "timestamp": at(11, 41), "cwd": project_dir,
             "message": {"role": "assistant", "content": [
                 {"type": "tool_use", "id": "t1", "name": "Bash", "input": {"command": "make test", "description": "run the suite"}}]}},
            {"type": "user", "sessionId": sid, "timestamp": at(11, 41), "cwd": project_dir,
             "message": {"role": "user", "content": [
                 {"type": "tool_result", "tool_use_id": "t1", "content": "?   \texample.com/svc/internal/store\t[no test files]"}]}},
        ]
        for i, (tid, path, body) in enumerate((
            ("t2", "internal/store/store_test.go", "//go:build golden\n\npackage store"),
            ("t3", "internal/store/fixtures.go", "root := os.Getenv(fixtureEnv)"),
            ("t4", "internal/store/env.go", 'const fixtureEnv = "SVC_FIXTURES"'),
        )):
            rows += [
                {"type": "assistant", "sessionId": sid, "timestamp": at(11, 42 + i), "cwd": project_dir,
                 "message": {"role": "assistant", "content": [
                     {"type": "tool_use", "id": tid, "name": "Read", "input": {"file_path": os.path.join(project_dir, path)}}]}},
                {"type": "user", "sessionId": sid, "timestamp": at(11, 42 + i), "cwd": project_dir,
                 "message": {"role": "user", "content": [{"type": "tool_result", "tool_use_id": tid, "content": body}]}},
            ]
        rows += [
            {"type": "assistant", "sessionId": sid, "timestamp": at(11, 45), "cwd": project_dir,
             "message": {"role": "assistant", "content": [
                 {"type": "tool_use", "id": "t5", "name": "Bash", "input": {"command": WINNING, "description": "run the golden suite"}}]}},
            {"type": "user", "sessionId": sid, "timestamp": at(11, 45), "cwd": project_dir,
             "message": {"role": "user", "content": [
                 {"type": "tool_result", "tool_use_id": "t5", "content": "ok  \texample.com/svc/internal/store\t0.6s"}]}},
            {"type": "user", "sessionId": sid, "timestamp": at(11, 47), "cwd": project_dir,
             "message": {"role": "user", "content": "confirmed: ok example.com/svc/internal/store"}},
        ]
        (out / f"{sid}.jsonl").write_text("".join(json.dumps(r) + "\n" for r in rows))


def main() -> int:
    if len(sys.argv) < 2:
        print(__doc__)
        return 2
    root = pathlib.Path(sys.argv[1]).resolve()
    proj = root / "project"
    project(proj)
    history(root / "history", str(proj))
    print(f"project: {proj} ({sum(1 for _ in proj.rglob('*') if _.is_file())} files)")
    print(f"history: {root / 'history'} (2 sessions)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
