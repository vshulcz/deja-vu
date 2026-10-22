package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

func tomlShapeTargets() []struct {
	target string
	path   func() string
} {
	return []struct {
		target string
		path   func() string
	}{
		{"codex", func() string { return filepath.Join(sources.CodexHome(), "config.toml") }},
		{"grok", func() string { return filepath.Join(sources.GrokHome(), "config.toml") }},
	}
}

func seedTOML(t *testing.T, p, seed string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(seed), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Codex takes multi-line text in developer_instructions and compact_prompt. A
// line of it starting with `[` was read as a table header: install and
// uninstall refused the file as broken, and a line spelling deja's own header
// had deja's command written over the string, after which codex would not load
// the config (#5158).
func TestTOMLMultilineStringLinesAreText(t *testing.T) {
	seeds := map[string]string{
		"ref-link": "model = \"o3\"\ndeveloper_instructions = \"\"\"\nFollow the style guide.\n[style]: https://example.com/style\n\"\"\"\n\n[mcp_servers.zz-mine]\ncommand = \"my-server\"\n",
		"literal":  "model = \"o3\"\ndeveloper_instructions = '''\nChecklist:\n[1] run the tests\n'''\n\n[mcp_servers.zz-mine]\ncommand = \"my-server\"\n",
		"header":   "compact_prompt = \"\"\"\n[mcp_servers.deja]\nnot a table\n\"\"\"\n\n[mcp_servers.zz-mine]\ncommand = \"my-server\"\n",
	}
	for _, tg := range tomlShapeTargets() {
		for name, seed := range seeds {
			t.Run(tg.target+"/"+name, func(t *testing.T) {
				hermeticEnv(t)
				p := tg.path()
				seedTOML(t, p, seed)
				if _, err := captureRun(t, "install", tg.target, "--no-index", "--no-guidance"); err != nil {
					t.Fatalf("install refused: %v", err)
				}
				got, _ := os.ReadFile(p)
				s := string(got)
				// The string survives whole, and deja's table follows it.
				str, _, _ := strings.Cut(seed, "\n\n")
				if !strings.HasPrefix(s, str+"\n") {
					t.Errorf("the string changed:\n%s", s)
				}
				if n := strings.Count(s[len(str):], "[mcp_servers.deja]"); n != 1 {
					t.Errorf("%d deja tables after the string, want 1:\n%s", n, s)
				}
				if _, err := captureRun(t, "uninstall", tg.target); err != nil {
					t.Fatalf("uninstall refused: %v", err)
				}
				if got, _ := os.ReadFile(p); string(got) != seed {
					t.Errorf("uninstall left:\n%s\nwant:\n%s", got, seed)
				}
			})
		}
	}
}

// `[ mcp_servers.deja ]`, `[mcp_servers . deja]` and `[mcp_servers."deja"]`
// all name the table `[mcp_servers.deja]` does. Install appended a second one
// beside the spaced spellings and codex refused the file with "duplicate key";
// doctor called the machine not wired (#5159).
func TestTOMLHeaderSpellingsNameDejasTable(t *testing.T) {
	for _, tg := range tomlShapeTargets() {
		for _, header := range []string{"[ mcp_servers.deja ]", "[mcp_servers . deja]", `[mcp_servers."deja"]`, "[mcp_servers.'deja']  # mine"} {
			t.Run(tg.target+"/"+header, func(t *testing.T) {
				hermeticEnv(t)
				p := tg.path()
				seedTOML(t, p, "model = \"o3\"\n\n"+header+"\ncommand = \"/old/deja\"\nargs = [\"mcp\"]\nstartup_timeout_sec = 30\n")
				if !doctorTOMLWired(p) {
					t.Errorf("doctor does not see %s", header)
				}
				out, err := captureRun(t, "install", tg.target, "--no-index", "--no-guidance")
				if err != nil {
					t.Fatal(err)
				}
				got, _ := os.ReadFile(p)
				s := string(got)
				if strings.Contains(s, "[mcp_servers.deja]") || !strings.Contains(s, header) {
					t.Errorf("a second table beside %s:\n%s", header, s)
				}
				if strings.Contains(s, "/old/deja") || !strings.Contains(s, "startup_timeout_sec = 30") {
					t.Errorf("the table was not updated where it stands:\n%s", s)
				}
				if strings.Contains(out, "instead of adding a second entry") {
					t.Errorf("deja's own table reported as a hand-named one:\n%s", out)
				}
				if _, err := captureRun(t, "uninstall", tg.target); err != nil {
					t.Fatal(err)
				}
				if got, _ := os.ReadFile(p); strings.Contains(string(got), "deja") {
					t.Errorf("uninstall left deja's table:\n%s", got)
				}
			})
		}
	}
}

// The name is a TOML key, so a quoted one with a dot in it is one server, not
// a sub-table of another: uninstall must not take `"deja.old"` for deja's.
func TestTOMLQuotedDottedServerIsNotASubTable(t *testing.T) {
	hermeticEnv(t)
	p := filepath.Join(sources.CodexHome(), "config.toml")
	seed := "[mcp_servers.\"deja.old\"]\ncommand = \"other-server\"\n"
	seedTOML(t, p, seed)
	if _, err := captureRun(t, "install", "codex", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if _, err := captureRun(t, "uninstall", "codex"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); string(got) != seed {
		t.Errorf("uninstall left:\n%s\nwant:\n%s", got, seed)
	}
}

// The keys in deja's own table that deja does not write are the reader's.
// Every install rewrote the table whole and dropped them — a timeout, the
// env_vars that hand deja its index dir (#5124) — while the same keys under a
// hand-named table survived (#5161).
func TestTOMLOwnTableKeepsTheReadersKeys(t *testing.T) {
	for _, tg := range tomlShapeTargets() {
		t.Run(tg.target, func(t *testing.T) {
			hermeticEnv(t)
			p := tg.path()
			kept := []string{"[mcp_servers.deja]  # memory", "startup_timeout_sec = 30", "tool_timeout_sec = 120", `env_vars = ["DEJA_INDEX_DIR"]`, `enabled_tools = ["search"]`}
			seedTOML(t, p, "model = \"o3\"\n\n"+kept[0]+"\ncommand = \"/old/deja\"\nargs = [\n  \"mcp\",\n]\n"+strings.Join(kept[1:], "\n")+"\n\n[mcp_servers.zz-mine]\ncommand = \"my-server\"\n")
			if _, err := captureRun(t, "install", tg.target, "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(p)
			s := string(got)
			for _, k := range kept {
				if !strings.Contains(s, k+"\n") {
					t.Errorf("install dropped %q:\n%s", k, s)
				}
			}
			if strings.Contains(s, "/old/deja") || strings.Count(s, "[mcp_servers.deja]") != 1 || strings.Count(s, "args =") != 1 {
				t.Errorf("deja's command was not rewritten in place:\n%s", s)
			}
			if _, err := captureRun(t, "install", tg.target, "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			if again, _ := os.ReadFile(p); string(again) != s {
				t.Errorf("a second install changed the file:\n%s", again)
			}
		})
	}
}

// The control: a table named deja that runs something else is still replaced
// whole — keeping its url beside deja's command would leave codex a server
// with two transports.
func TestTOMLForeignTableUnderDejasNameIsReplaced(t *testing.T) {
	hermeticEnv(t)
	p := filepath.Join(sources.CodexHome(), "config.toml")
	seedTOML(t, p, "[mcp_servers.deja]\nurl = \"https://example.com/mcp\"\n")
	if _, err := captureRun(t, "install", "codex", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); strings.Contains(string(got), "url =") || !strings.Contains(string(got), "command =") {
		t.Errorf("the foreign table was not replaced:\n%s", got)
	}
}

// A triple quote that opens nothing — inside a one-line string, a literal, or
// escaped in a multi-line one — must not hide the tables after it, or install
// misses deja's own and appends a second one codex refuses (#5158).
func TestTOMLStrayTripleQuoteHidesNoTable(t *testing.T) {
	for name, pre := range map[string]string{
		"literal-in-basic-ml": "developer_instructions = \"\"\"Wrap SQL in ''' fences.\nKeep diffs small.\n\"\"\"\n",
		"in-basic":            "note = \"use ''' for raw\"\n",
		"in-literal":          "note = 'say \"\"\" here'\n",
		"escaped":             "developer_instructions = \"\"\"\nwrite \\\"\"\" for docstrings\n\"\"\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			hermeticEnv(t)
			p := filepath.Join(sources.CodexHome(), "config.toml")
			seed := pre + "\n[mcp_servers.deja]\ncommand = \"/old/deja\"\nargs = [\"mcp\"]\n"
			seedTOML(t, p, seed)
			if _, err := captureRun(t, "install", "codex", "--no-index", "--no-guidance"); err != nil {
				t.Fatal(err)
			}
			got, _ := os.ReadFile(p)
			if n := strings.Count(string(got), "[mcp_servers.deja]"); n != 1 || strings.Contains(string(got), "/old/deja") {
				t.Errorf("%d deja tables, want 1 updated in place:\n%s", n, got)
			}
			if _, err := captureRun(t, "uninstall", "codex"); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(p); strings.Contains(string(got), "mcp_servers") || !strings.HasPrefix(string(got), pre) {
				t.Errorf("uninstall left:\n%s", got)
			}
		})
	}
}

// An install before #5159 recorded an adopted hand-named table by its header
// as written, quotes and all; uninstall still takes it back.
func TestTOMLUninstallFindsAQuotedRecord(t *testing.T) {
	for _, hdr := range []string{`"deja-vu"`, `'deja-vu'`, `'deja.vu'`} {
		t.Run(hdr, func(t *testing.T) {
			hermeticEnv(t)
			p := filepath.Join(sources.CodexHome(), "config.toml")
			seedTOML(t, p, "model = \"o3\"\n\n[mcp_servers."+hdr+"]\ncommand = \"/old/deja\"\nargs = [\"mcp\"]\n")
			noteBlockAdded(p, "mcp_servers."+hdr)
			if _, err := captureRun(t, "uninstall", "codex"); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(p); strings.Contains(string(got), "/old/deja") {
				t.Errorf("uninstall left the adopted table:\n%s", got)
			}
		})
	}
}

// A url beside deja's command is two transports; install replaces the table
// whole, as it did before the rewrite kept the reader's keys.
func TestTOMLDejaTableWithAURLIsReplaced(t *testing.T) {
	hermeticEnv(t)
	p := filepath.Join(sources.CodexHome(), "config.toml")
	seedTOML(t, p, "[mcp_servers.deja]\nurl = \"http://192.0.2.1/mcp\"\ncommand = \"/old/deja\"\nargs = [\"mcp\"]\n")
	if _, err := captureRun(t, "install", "codex", "--no-index", "--no-guidance"); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(p); strings.Contains(string(got), "url =") {
		t.Errorf("url kept beside deja's command:\n%s", got)
	}
}
