package main

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The Spec Kit extension is installed by `specify extension add`, which
// refuses a manifest that breaks its schema (extensions/EXTENSION-API-REFERENCE.md
// in github/spec-kit) and installs nothing. These are the rules it checks, plus
// the ones only we know: the version is the release's, and every deja call in
// the command keeps the query behind `--`.
func TestSpecKitExtensionManifest(t *testing.T) {
	m := parseSimpleYAML(t, string(repoFile(t, "extensions/spec-kit/extension.yml")))

	if m.str("schema_version") != "1.0" {
		t.Fatalf("schema_version = %q, Spec Kit reads 1.0", m.str("schema_version"))
	}
	ext := m.mapping("extension")
	id := ext.str("id")
	if !regexp.MustCompile(`^[a-z0-9-]+$`).MatchString(id) {
		t.Fatalf("extension.id %q does not match ^[a-z0-9-]+$", id)
	}
	for _, k := range []string{"name", "version", "description", "author", "repository", "license"} {
		if ext.str(k) == "" {
			t.Errorf("extension.%s is empty; the schema requires it", k)
		}
	}
	if d := ext.str("description"); len(d) >= 200 {
		t.Errorf("description is %d characters; the catalog wants under 200", len(d))
	}
	// X.Y.Z only: Spec Kit rejects 1.0 and 1.0.0-beta.
	version := ext.str("version")
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(version) {
		t.Fatalf("extension.version %q is not X.Y.Z", version)
	}

	// scripts/pinmanifests moves this with every other release manifest, and
	// the release packs the directory after that pin. A version that disagrees
	// with the Gemini manifest means the pin missed it.
	var gemini struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(repoFile(t, "gemini-extension.json"), &gemini); err != nil {
		t.Fatal(err)
	}
	if version != gemini.Version {
		t.Errorf("extension.version is %s, the other release manifests say %s — run scripts/pinmanifests", version, gemini.Version)
	}
	release := string(repoFile(t, ".github/workflows/release.yml"))
	pin, pack := strings.Index(release, "go run ./scripts/pinmanifests -version"), strings.Index(release, `zip -qr "$GITHUB_WORKSPACE/dist/deja-spec-kit.zip"`)
	if pin < 0 || pack < 0 || pack < pin {
		t.Errorf("release.yml must pack deja-spec-kit.zip after pinning the manifests (pin at %d, pack at %d)", pin, pack)
	}
	if !strings.Contains(release, "gh release upload \"${GITHUB_REF_NAME}\" dist/deja-spec-kit.zip") {
		t.Error("release.yml packs deja-spec-kit.zip and never attaches it to the release")
	}

	req := m.mapping("requires")
	if !strings.HasPrefix(req.str("speckit_version"), ">=") {
		t.Errorf("requires.speckit_version %q has no lower bound", req.str("speckit_version"))
	}
	tools := req.list("tools")
	if len(tools) != 1 || tools[0].str("name") != "deja" || tools[0].str("required") != "true" {
		t.Errorf("requires.tools should declare the deja binary as required, got %v", tools)
	}

	cmdName := regexp.MustCompile(`^speckit\.` + regexp.QuoteMeta(id) + `\.[a-z0-9-]+$`)
	provided := map[string]bool{}
	cmds := m.mapping("provides").list("commands")
	if len(cmds) == 0 {
		t.Fatal("provides.commands is empty")
	}
	for _, c := range cmds {
		name, file := c.str("name"), c.str("file")
		if !cmdName.MatchString(name) {
			t.Errorf("command %q does not match speckit.%s.<command>", name, id)
		}
		if c.str("description") == "" {
			t.Errorf("command %s has no description", name)
		}
		if file == "" || strings.HasPrefix(file, "/") || strings.Contains(file, "..") {
			t.Errorf("command %s file %q must be a relative path inside the extension", name, file)
			continue
		}
		provided[name] = true
		body := string(repoFile(t, "extensions/spec-kit/"+file))
		front, ok := strings.CutPrefix(body, "---\n")
		if !ok || !strings.Contains(front, "\n---\n") {
			t.Errorf("%s has no frontmatter", file)
			continue
		}
		front, rest, _ := strings.Cut(front, "\n---\n")
		if parseSimpleYAML(t, front).str("description") == "" {
			t.Errorf("%s frontmatter has no description", file)
		}
		if !strings.Contains(rest, "$ARGUMENTS") {
			t.Errorf("%s never reads $ARGUMENTS", file)
		}
		// A query is a feature's words and may start with a dash; without --
		// deja reads it as a flag and fails.
		for _, call := range regexp.MustCompile("deja (recall|files|search|fix|how) [^`\\n]*").FindAllString(rest, -1) {
			if f := strings.Fields(call); len(f) < 3 || f[2] != "--" {
				t.Errorf("%s runs %q without -- before the query", file, call)
			}
		}
	}

	hooks := m.mapping("hooks")
	if len(hooks) == 0 {
		t.Fatal("no hooks: the before_implement suggestion is gone")
	}
	for event, v := range hooks {
		h, ok := v.(yamlMap)
		if !ok {
			t.Errorf("hook %s is not a single mapping", event)
			continue
		}
		if !provided[h.str("command")] {
			t.Errorf("hook %s runs %q, which this extension does not provide", event, h.str("command"))
		}
		// optional renders as a suggestion the user answers; false would run
		// the recall on every implement without asking.
		if h.str("optional") != "true" || h.str("prompt") == "" {
			t.Errorf("hook %s must stay optional with a prompt", event)
		}
	}
}

// yamlMap is the subset of YAML the Spec Kit manifest uses: nested mappings,
// lists of mappings and scalars, quoted or plain. Enough to read the fields
// Spec Kit validates without taking a YAML dependency for one test.
type yamlMap map[string]any

func (m yamlMap) str(k string) string {
	s, _ := m[k].(string)
	return s
}

func (m yamlMap) mapping(k string) yamlMap {
	v, _ := m[k].(yamlMap)
	return v
}

func (m yamlMap) list(k string) []yamlMap {
	items, _ := m[k].([]any)
	var out []yamlMap
	for _, it := range items {
		if mm, ok := it.(yamlMap); ok {
			out = append(out, mm)
		}
	}
	return out
}

type yamlLine struct {
	indent int
	text   string
}

func parseSimpleYAML(t *testing.T, src string) yamlMap {
	t.Helper()
	var lines []yamlLine
	for i, raw := range strings.Split(src, "\n") {
		if strings.Contains(raw, "\t") {
			t.Fatalf("line %d has a tab; YAML indentation is spaces", i+1)
		}
		text := strings.TrimSpace(raw)
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		lines = append(lines, yamlLine{len(raw) - len(strings.TrimLeft(raw, " ")), text})
	}
	p := &yamlParser{t: t, lines: lines}
	m := p.mapping(0)
	if p.pos != len(p.lines) {
		t.Fatalf("unparsed YAML from %q", p.lines[p.pos].text)
	}
	return m
}

type yamlParser struct {
	t     *testing.T
	lines []yamlLine
	pos   int
}

func (p *yamlParser) mapping(indent int) yamlMap {
	m := yamlMap{}
	for p.pos < len(p.lines) && p.lines[p.pos].indent == indent && !strings.HasPrefix(p.lines[p.pos].text, "- ") {
		k, v := p.pair(p.lines[p.pos].text)
		p.pos++
		if _, dup := m[k]; dup {
			p.t.Fatalf("duplicate key %q", k)
		}
		m[k] = v
		if v == nil {
			m[k] = p.block(indent)
		}
	}
	return m
}

// block reads what follows a "key:" with nothing after it.
func (p *yamlParser) block(parent int) any {
	if p.pos >= len(p.lines) || p.lines[p.pos].indent < parent || (p.lines[p.pos].indent == parent && !strings.HasPrefix(p.lines[p.pos].text, "- ")) {
		return ""
	}
	ln := p.lines[p.pos]
	if !strings.HasPrefix(ln.text, "- ") {
		return p.mapping(ln.indent)
	}
	var items []any
	for p.pos < len(p.lines) && p.lines[p.pos].indent == ln.indent && strings.HasPrefix(p.lines[p.pos].text, "- ") {
		item := strings.TrimPrefix(p.lines[p.pos].text, "- ")
		if !strings.Contains(item, ": ") && !strings.HasSuffix(item, ":") {
			items = append(items, unquoteYAML(p.t, item))
			p.pos++
			continue
		}
		// "- key: value" opens a mapping whose other keys sit two columns in.
		p.lines[p.pos] = yamlLine{ln.indent + 2, item}
		items = append(items, p.mapping(ln.indent+2))
	}
	return items
}

func (p *yamlParser) pair(text string) (string, any) {
	k, v, ok := strings.Cut(text, ":")
	if !ok || strings.ContainsAny(k, " \"'") {
		p.t.Fatalf("not a key: value line: %q", text)
	}
	v = strings.TrimSpace(v)
	if v == "" {
		return k, nil
	}
	return k, unquoteYAML(p.t, v)
}

func unquoteYAML(t *testing.T, v string) string {
	if strings.HasPrefix(v, `"`) {
		s, err := strconv.Unquote(v)
		if err != nil {
			t.Fatalf("bad quoted scalar %s: %v", v, err)
		}
		return s
	}
	if strings.HasPrefix(v, "[") || strings.HasPrefix(v, "{") || strings.HasPrefix(v, "'") {
		t.Fatalf("flow and single-quoted scalars are outside this parser: %s", v)
	}
	return v
}
