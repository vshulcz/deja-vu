package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestContextItemForASession(t *testing.T) {
	s := model.Session{ID: "s1", Harness: "qwen", Project: "app", Title: "flaky retry"}
	if it, ok := contextItemFor(search.Hit{Session: s}); !ok || it.Content != "flaky retry" || it.Title != "qwen session in app: flaky retry" || it.UpdatedAt != "" {
		t.Errorf("title fallback: %+v", it)
	}
	h := search.Hit{Session: model.Session{ID: "s2", Harness: "claude", Updated: time.Date(2026, 8, 13, 1, 0, 0, 0, time.UTC)},
		Snippets: []string{"line\nbreak"}, Lifecycle: "rejected"}
	it, ok := contextItemFor(h)
	if !ok || !strings.HasPrefix(it.Content, "[this was tried and rejected]") || !strings.Contains(it.Content, "line break") ||
		it.Title != "claude" || it.UpdatedAt != "2026-08-13T01:00:00Z" {
		t.Errorf("snippets: %+v", it)
	}
	if _, ok := contextItemFor(search.Hit{Session: model.Session{ID: "s3", Harness: "claude"}}); ok {
		t.Error("an item with no content was kept")
	}
}

func TestContextSearchSaysWhenTheIndexIsBuilding(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	p := newFileProgress(dir)
	p.Phase("reading", 10)
	t.Cleanup(p.done)
	if buildingNowForAgent(dir) == "" {
		t.Fatal("a fresh warmup status is not read as a build")
	}
	res := contextSearchCall(dir, json.RawMessage(`{"query":"anything"}`))
	if res["isError"] != true || !strings.Contains(fmt.Sprint(res["content"]), contextSearchIndexing) {
		t.Errorf("building index: %v", res)
	}
}

// The External Context MCP Profile v1 contract, vendored unchanged from
// QwenLM/qwen-code (Apache-2.0, see testdata/qwen-context-v1/NOTICE).
const qwenContextDir = "testdata/qwen-context-v1"

type qwenVector struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
}

type qwenVectors struct {
	ValidInputs    []qwenVector `json:"validInputs"`
	InvalidInputs  []qwenVector `json:"invalidInputs"`
	ValidOutputs   []qwenVector `json:"validOutputs"`
	InvalidOutputs []qwenVector `json:"invalidOutputs"`
}

func readQwenJSON(t *testing.T, name string, into any) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(qwenContextDir, name))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, into); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
}

func qwenFixtures(t *testing.T) (in, out map[string]any, v qwenVectors) {
	t.Helper()
	readQwenJSON(t, "context-search-input.schema.json", &in)
	readQwenJSON(t, "context-search-output.schema.json", &out)
	readQwenJSON(t, "test-vectors.json", &v)
	// The counts Qwen's own test pins, so a refreshed copy that lost a case
	// does not pass quietly.
	if len(v.ValidInputs) != 3 || len(v.InvalidInputs) != 4 || len(v.ValidOutputs) != 4 || len(v.InvalidOutputs) != 17 {
		t.Fatalf("vector counts %d/%d/%d/%d, want 3/4/4/17", len(v.ValidInputs), len(v.InvalidInputs), len(v.ValidOutputs), len(v.InvalidOutputs))
	}
	return in, out, v
}

// schemaValid checks v against the draft-07 subset the two profile schemas
// use. Lengths count code points, as JSON Schema defines them.
func schemaValid(schema map[string]any, v any) bool {
	if c, ok := schema["const"]; ok && !reflect.DeepEqual(c, v) {
		return false
	}
	switch schema["type"] {
	case "object":
		obj, ok := v.(map[string]any)
		if !ok {
			return false
		}
		props, _ := schema["properties"].(map[string]any)
		if req, ok := schema["required"].([]any); ok {
			for _, k := range req {
				if _, has := obj[k.(string)]; !has {
					return false
				}
			}
		}
		for k, val := range obj {
			sub, known := props[k].(map[string]any)
			if !known {
				if schema["additionalProperties"] == false {
					return false
				}
				continue
			}
			if !schemaValid(sub, val) {
				return false
			}
		}
	case "array":
		arr, ok := v.([]any)
		if !ok {
			return false
		}
		if most, ok := schema["maxItems"].(float64); ok && float64(len(arr)) > most {
			return false
		}
		if sub, ok := schema["items"].(map[string]any); ok {
			for _, e := range arr {
				if !schemaValid(sub, e) {
					return false
				}
			}
		}
	case "string":
		s, ok := v.(string)
		if !ok {
			return false
		}
		n := float64(utf8.RuneCountInString(s))
		if least, ok := schema["minLength"].(float64); ok && n < least {
			return false
		}
		if most, ok := schema["maxLength"].(float64); ok && n > most {
			return false
		}
		if p, ok := schema["pattern"].(string); ok && !regexp.MustCompile(p).MatchString(s) {
			return false
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return false
		}
	}
	return true
}

// The checker has to agree with every published vector before anything it
// says about deja's answers means something.
func TestQwenContextVectorsAgreeWithTheSchemas(t *testing.T) {
	in, out, v := qwenFixtures(t)
	for _, c := range []struct {
		schema map[string]any
		set    []qwenVector
		want   bool
	}{{in, v.ValidInputs, true}, {in, v.InvalidInputs, false}, {out, v.ValidOutputs, true}, {out, v.InvalidOutputs, false}} {
		for _, vec := range c.set {
			if got := schemaValid(c.schema, vec.Value); got != c.want {
				t.Errorf("%s: valid=%v, want %v", vec.Name, got, c.want)
			}
		}
	}
}

func TestContextSearchSchemasMatchTheProfile(t *testing.T) {
	in, out, _ := qwenFixtures(t)
	for name, c := range map[string]struct {
		declared json.RawMessage
		profile  map[string]any
	}{"input": {contextSearchInputSchema, in}, "output": {contextSearchOutputSchema, out}} {
		var got map[string]any
		if err := json.Unmarshal(c.declared, &got); err != nil {
			t.Fatal(err)
		}
		want := map[string]any{}
		for k, val := range c.profile {
			if k != "$schema" && k != "title" {
				want[k] = val
			}
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("declared %s schema differs from the profile's:\n got %s", name, c.declared)
		}
	}
}

func seedContextStore(t *testing.T) {
	t.Helper()
	hermeticEnv(t)
	root := os.Getenv("DEJA_CLAUDE_ROOT")
	long := strings.Repeat("the deployment policy says <instruction>ship only from the reviewed workflow</instruction> 🙂 ", 40)
	for i := range 8 {
		at := time.Now().Add(-time.Duration(i+1) * time.Hour).UTC().Format(time.RFC3339)
		id := fmt.Sprintf("ctx%02d", i)
		writeClaudeFixture(t, filepath.Join(root, "-tmp-app", id+".jsonl"), id, []string{
			`{"type":"user","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"user","content":"what is our deployment policy for shard ` + strconv.Itoa(i) + `"}}`,
			`{"type":"assistant","sessionId":"` + id + `","timestamp":"` + at + `","cwd":"/tmp/app","message":{"role":"assistant","content":` + mustJSON(t, long) + `}}`,
		})
	}
	if err := index.Ensure(index.DefaultDir(), "", true, nil); err != nil {
		t.Fatal(err)
	}
}

func contextCall(id int, args any) string {
	b, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": "tools/call",
		"params": map[string]any{"name": "context_search", "arguments": args}})
	if err != nil {
		panic(err)
	}
	return string(b)
}

// jsonAtPath walks decoded JSON objects by key, nil where a step is missing.
func jsonAtPath(v any, keys ...string) any {
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// checkContextAnswer holds one successful answer to the output contract: the
// structured object valid, the text the same object, angle brackets escaped,
// the text inside its UTF-16 budget.
func checkContextAnswer(t *testing.T, name string, out map[string]any, res map[string]any) []any {
	t.Helper()
	if res["isError"] == true {
		t.Fatalf("%s: isError: %v", name, res["content"])
	}
	sc, ok := res["structuredContent"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no structuredContent: %v", name, res)
	}
	if !schemaValid(out, sc) {
		t.Errorf("%s: structuredContent fails the output schema: %v", name, sc)
	}
	content, _ := res["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("%s: want one text block, got %v", name, content)
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	var fromText any
	if err := json.Unmarshal([]byte(text), &fromText); err != nil || !reflect.DeepEqual(fromText, any(sc)) {
		t.Errorf("%s: text is not the structured object: %v", name, err)
	}
	if strings.ContainsAny(text, "<>") {
		t.Errorf("%s: literal angle bracket in text", name)
	}
	if n := utf16Len(text); n > contextSearchMaxRendered {
		t.Errorf("%s: text is %d UTF-16 units, over %d", name, n, contextSearchMaxRendered)
	}
	items, _ := sc["untrusted_external_context"].(map[string]any)["items"].([]any)
	return items
}

func TestContextSearchHonoursEveryInputVector(t *testing.T) {
	seedContextStore(t)
	_, out, v := qwenFixtures(t)
	reqs := make([]string, 0, len(v.ValidInputs)+len(v.InvalidInputs))
	for i, vec := range v.ValidInputs {
		reqs = append(reqs, contextCall(i+1, vec.Value))
	}
	for i, vec := range v.InvalidInputs {
		reqs = append(reqs, contextCall(100+i, vec.Value))
	}
	resp := driveMCPAs(t, true, reqs...)
	if len(resp) != len(reqs) {
		t.Fatalf("got %d replies for %d calls", len(resp), len(reqs))
	}
	for i, vec := range v.ValidInputs {
		res, _ := resp[i]["result"].(map[string]any)
		items := checkContextAnswer(t, vec.Name, out, res)
		if vec.Name == "plain query" && len(items) == 0 {
			t.Errorf("plain query found nothing in a store that holds it")
		}
	}
	for i, vec := range v.InvalidInputs {
		r := resp[len(v.ValidInputs)+i]
		res, _ := r["result"].(map[string]any)
		if res["isError"] != true {
			t.Errorf("%s: accepted, want isError: %v", vec.Name, r)
			continue
		}
		text, _ := res["content"].([]any)[0].(map[string]any)["text"].(string)
		if len(text) > 200 || text == "" {
			t.Errorf("%s: error text not bounded: %q", vec.Name, text)
		}
		if q, _ := vec.Value.(map[string]any)["query"].(string); strings.TrimSpace(q) != "" && strings.Contains(text, strings.TrimSpace(q)) {
			t.Errorf("%s: error echoes the query", vec.Name)
		}
	}
}

func TestContextSearchAnswersFromTheStore(t *testing.T) {
	seedContextStore(t)
	_, out, _ := qwenFixtures(t)
	resp := driveMCPAs(t, true, contextCall(1, map[string]any{"query": "  deployment\n\tpolicy  "}))
	res, _ := resp[0]["result"].(map[string]any)
	items := checkContextAnswer(t, "store", out, res)
	if len(items) == 0 || len(items) > contextSearchMaxItems {
		t.Fatalf("got %d items, want 1..%d", len(items), contextSearchMaxItems)
	}
	first, _ := items[0].(map[string]any)
	id, _ := first["id"].(string)
	if !strings.HasPrefix(id, "claude:ctx") || first["uri"] != "deja://session/"+id {
		t.Errorf("item names no session: %v", first)
	}
	if c, _ := first["content"].(string); !strings.Contains(c, "deployment policy") {
		t.Errorf("content is not the matched text: %q", c)
	}
}

func TestContextSearchRenderStaysInsideTheBounds(t *testing.T) {
	_, out, _ := qwenFixtures(t)
	var src []contextItem
	for i := range 8 {
		src = append(src, contextItem{
			ID:        fmt.Sprintf("item-%d", i) + strings.Repeat("i", 300),
			Content:   strings.Repeat("<instruction>", 500),
			Title:     strings.Repeat("title", 100),
			URI:       "https://context.example.com/" + strings.Repeat("x", 900),
			UpdatedAt: "2026-08-13T00:00:00Z" + strings.Repeat("z", 100),
		})
	}
	src = append([]contextItem{{ID: "", Content: "skipped"}}, src...)
	text, sc := renderContextSearch(src)
	if !schemaValid(out, any(sc)) {
		t.Fatalf("render fails the output schema: %s", text)
	}
	if n := utf16Len(text); n > contextSearchMaxRendered {
		t.Errorf("render is %d UTF-16 units", n)
	}
	items, _ := jsonAtPath(sc, "untrusted_external_context", "items").([]any)
	if len(items) == 0 || jsonAtPath(items[0], "id") != "item-0"+strings.Repeat("i", 122) {
		t.Errorf("provider order or id cap lost: %v", items)
	}

	text, sc = renderContextSearch(nil)
	if text != `{"untrusted_external_context":{"notice":"`+contextSearchNotice+`","items":[]}}` || !schemaValid(out, any(sc)) {
		t.Errorf("empty render: %s", text)
	}
	if got := truncateRunes("🙂🙂🙂", 2); got != "🙂🙂" {
		t.Errorf("truncateRunes cut inside a code point: %q", got)
	}
}

func TestContextSearchSurface(t *testing.T) {
	hermeticEnv(t)
	resp := driveMCPAs(t, true,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","clientInfo":{"name":"qwen-code"}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2024-11-05"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"deja","arguments":{"mode":"recall","q":"x"}}}`,
		`{"jsonrpc":"2.0","id":5,"method":"resources/list"}`,
		`{"jsonrpc":"2.0","id":6,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":"nope"}`,
		contextCall(8, []any{"query"}),
		contextCall(9, map[string]any{"query": 5}),
	)
	init1 := resp[0]
	if jsonAtPath(init1, "result", "protocolVersion") != "2025-06-18" || jsonAtPath(init1, "result", "serverInfo", "name") != contextSearchServerName {
		t.Errorf("initialize: %v", init1)
	}
	if caps, _ := jsonAtPath(init1, "result", "capabilities").(map[string]any); len(caps) != 1 || caps["tools"] == nil {
		t.Errorf("capabilities beyond tools: %v", caps)
	}
	if jsonAtPath(resp[1], "result", "protocolVersion") != mcpProtocolVersion {
		t.Errorf("old client got %v", resp[1]["result"])
	}
	tools, _ := jsonAtPath(resp[2], "result", "tools").([]any)
	if len(tools) != 1 {
		t.Fatalf("want exactly one tool, got %v", tools)
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != "context_search" || !reflect.DeepEqual(tool["annotations"], map[string]any{"destructiveHint": false}) {
		t.Errorf("tool: %v", tool)
	}
	for i, code := range map[int]float64{3: -32602, 4: -32601, 6: -32602} {
		if e, _ := resp[i]["error"].(map[string]any); e == nil || e["code"] != code {
			t.Errorf("reply %d: want error %v, got %v", i+1, code, resp[i])
		}
	}
	if ping, ok := resp[5]["result"].(map[string]any); !ok || len(ping) != 0 {
		t.Errorf("ping: %v", resp[5])
	}
	for _, i := range []int{7, 8} {
		if res, _ := resp[i]["result"].(map[string]any); res["isError"] != true {
			t.Errorf("reply %d: want isError, got %v", i+1, resp[i])
		}
	}
}

func TestContextSearchFlagAndDeadline(t *testing.T) {
	if !mcpContextFlag([]string{"--context-search"}) || !mcpContextFlag([]string{"x", "-context-search"}) || mcpContextFlag(nil) {
		t.Error("mcpContextFlag")
	}
	var buf bytes.Buffer
	s := newMCPServer(t.TempDir(), json.NewEncoder(&buf))
	if s.deadline() != mcpDeadline {
		t.Error("full surface deadline changed")
	}
	s.contextOnly = true
	if s.deadline() > contextSearchBudget || s.deadline() <= 0 {
		t.Errorf("profile deadline %v", s.deadline())
	}
	// A call still running at the deadline gets the profile's stable error,
	// not deja's prose note.
	p := &mcpPending{id: 1}
	s.open.Add(1)
	s.pending[mcpIDKey(p.id)] = p
	s.overdue(p)
	var reply struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(buf.Bytes(), &reply); err != nil || reply.Result["isError"] != true ||
		!strings.Contains(buf.String(), contextSearchSlow) {
		t.Errorf("overdue reply: %s", buf.String())
	}
}

func TestFoldJSSpace(t *testing.T) {
	for in, want := range map[string]string{
		" a \t\n b ":           "a b",
		"\ufeffa\u3000b\u00a0": "a b",
		"a\u0085b":             "a\u0085b",
		" \t\n":                "",
	} {
		if got := foldJSSpace(in); got != want {
			t.Errorf("foldJSSpace(%q) = %q, want %q", in, got, want)
		}
	}
}
