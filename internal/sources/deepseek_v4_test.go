package sources

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// writeFramedLog writes body the way dsh does by default: one zstd frame per
// line, the frames concatenated.
func writeFramedLog(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for _, line := range strings.SplitAfter(body, "\n") {
		if line == "" {
			continue
		}
		cmd := exec.CommandContext(t.Context(), "zstd", "-q", "-c")
		cmd.Stdin = strings.NewReader(line)
		frame, err := cmd.Output()
		if err != nil {
			t.Fatalf("zstd: %v", err)
		}
		out.Write(frame)
	}
	if err := os.WriteFile(path, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

const deepSeekV0Log = `{"type":"session","version":0,"id":"session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f","createdAt":1790506922540,"cwd":"/work/pgbouncer-lab","delegationDepth":0}
{"type":"user/message","seq":1,"time":1790506922600,"data":{"content":[{"type":"text","text":"oldneedle what holds the pool size"}],"source":{"kind":"user"},"role":"user"}}
{"type":"assistant/message","seq":2,"time":1790506922700,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"pgbouncer.ini does"}],"source":{"kind":"model","provider":"mini"}}}}
`

// dsh 2.0.15 (dsh-base 0.1.7-rc.2) migrates a session it opens into a new
// generation file with the whole history and leaves the old one as it was. v4
// lifts a tool result out of its tool-result wrapper into a tool-role message
// that carries isError itself.
const deepSeekV4Log = `{"type":"session","version":4,"id":"session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f","createdAt":1790506922540,"cwd":"/work/pgbouncer-lab","isSeeded":false,"delegationDepth":0}
{"type":"user/message","seq":1,"time":1790506922600,"data":{"content":[{"type":"text","text":"oldneedle what holds the pool size"}],"source":{"kind":"user"},"role":"user"}}
{"type":"assistant/message","seq":2,"time":1790506922700,"data":{"turn":1,"step":1,"message":{"role":"assistant","content":[{"type":"text","text":"pgbouncer.ini does"}],"source":{"kind":"model","provider":"mini"}}}}
{"type":"user/message","seq":3,"time":1790593322600,"data":{"content":[{"type":"text","text":"v4needle raise it to 60"}],"source":{"kind":"user"},"role":"user"}}
{"type":"tool/call","seq":4,"time":1790593322700,"data":{"turn":2,"step":1,"callId":"denied","name":"edit","arguments":"{\"file_path\": \"/work/pgbouncer-lab/denied.ini\", \"old_string\": \"pool_size = 40\", \"new_string\": \"pool_size = 60\"}"}}
{"type":"tool/result","seq":5,"time":1790593322710,"data":{"turn":2,"step":1,"message":{"role":"tool","source":{"kind":"tool","callId":"denied"},"toolCallId":"denied","content":[{"type":"text","text":"Error: rejected"}],"isError":true,"id":"t1"}}}
{"type":"tool/call","seq":6,"time":1790593322720,"data":{"turn":2,"step":1,"callId":"good","name":"edit","arguments":"{\"file_path\": \"/work/pgbouncer-lab/pgbouncer.ini\", \"old_string\": \"pool_size = 40\", \"new_string\": \"pool_size = 60\"}"}}
{"type":"tool/result","seq":7,"time":1790593322730,"data":{"turn":2,"step":1,"message":{"role":"tool","source":{"kind":"tool","callId":"good"},"toolCallId":"good","content":[{"type":"text","text":"v4output the file has been updated"}],"id":"t2"}}}
`

// A session dsh has migrated keeps its old log beside the new generation, and
// the new one holds everything. Reading the old one froze the session at the
// upgrade; reading both listed it twice (#4600).
func TestDeepSeekReadsTheNewestLogGeneration(t *testing.T) {
	if !ZstdAvailable() {
		t.Skip("zstd CLI not available")
	}
	tmp := hermeticSourcesEnv(t)
	root := filepath.Join(tmp, "dsh", "sessions")
	t.Setenv("DSH_HOME", filepath.Join(tmp, "dsh"))
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)
	ws := filepath.Join(root, "--work-pgbouncer-lab--")

	migrated := filepath.Join(ws, "session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f")
	writeFramedLog(t, filepath.Join(migrated, "session.jsonl.zstd"), deepSeekV0Log)
	writeFramedLog(t, filepath.Join(migrated, "session.v4.jsonl.zstd"), deepSeekV4Log)
	fresh := filepath.Join(ws, "session-v4only")
	writeFramedLog(t, filepath.Join(fresh, "session.v4.jsonl.zstd"),
		strings.ReplaceAll(deepSeekV4Log, "session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f", "session-v4only"))
	// A generation this reader has not seen: dsh's own filename grammar is
	// open-ended and the event envelope did not change across v3 and v4, so it
	// is read rather than dropped.
	future := filepath.Join(ws, "session-v5")
	writeFramedLog(t, filepath.Join(future, "session.v5.jsonl.zstd"),
		strings.ReplaceAll(strings.ReplaceAll(deepSeekV4Log, `"version":4`, `"version":5`), "session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f", "session-v5"))

	files := DeepSeekSessionFiles()
	got := make([]string, 0, len(files))
	for _, f := range files {
		got = append(got, filepath.Join(filepath.Base(filepath.Dir(f)), filepath.Base(f)))
	}
	slices.Sort(got)
	want := []string{
		"session-7c2e9f40-3b1a-4d5e-8f60-1a2b3c4d5e6f/session.v4.jsonl.zstd",
		"session-v4only/session.v4.jsonl.zstd",
		"session-v5/session.v5.jsonl.zstd",
	}
	for i := range want {
		want[i] = filepath.FromSlash(want[i])
	}
	if !slices.Equal(got, want) {
		t.Fatalf("DeepSeekSessionFiles = %q, want %q", got, want)
	}

	ss := LoadDeepSeek()
	if len(ss) != 3 {
		t.Fatalf("LoadDeepSeek = %d sessions, want 3", len(ss))
	}
	for _, s := range ss {
		var text []string
		for _, m := range s.Messages {
			text = append(text, m.Text)
		}
		all := strings.Join(text, "\n")
		if !strings.Contains(all, "oldneedle") || !strings.Contains(all, "v4needle") || !strings.Contains(all, "v4output") {
			t.Errorf("%s read as %q, want the whole migrated history", s.ID, all)
		}
	}
}

func TestDeepSeekLogNameGrammar(t *testing.T) {
	for name, want := range map[string]bool{
		"session.jsonl":          true,
		"session.jsonl.zstd":     true,
		"session.v3.jsonl.zstd":  true,
		"session.v4.jsonl":       true,
		"session.v4.jsonl.zstd":  true,
		"session.v12.jsonl.zstd": true,
		"session.v0.jsonl":       false,
		"session.v04.jsonl":      false,
		"session.V4.jsonl":       false,
		"session.v4.jsonl.tmp":   false,
		"session.vx.jsonl":       false,
	} {
		if got := isDeepSeekLog(filepath.Join("s", "session-a", name)); got != want {
			t.Errorf("isDeepSeekLog(%s) = %v, want %v", name, got, want)
		}
	}
}

// v4 moved isError from the tool-result block onto the message, so a refused
// edit has to be read off the message or it is credited as made.
func TestParseDeepSeekFileReadsAV4ErrorResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session-x", "session.v4.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(deepSeekV4Log), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var edits, out []string
	for _, m := range ss[0].Messages {
		switch m.Role {
		case RoleEdit:
			edits = append(edits, m.Text)
		case RoleToolOutput:
			out = append(out, m.Text)
		}
	}
	if got := strings.Join(edits, "|"); got != "/work/pgbouncer-lab/pgbouncer.ini\npool_size = 40" {
		t.Errorf("edits = %q, want only the edit whose result came back clean", got)
	}
	if got := strings.Join(out, "|"); got != "Error: rejected|v4output the file has been updated" {
		t.Errorf("tool output = %q", got)
	}
}

// A v4 result's blocks are the content itself, so an image or a block a plugin
// wrote sits beside the text. Only the text is what the tool printed.
func TestParseDeepSeekFileKeepsOnlyTextFromAV4Result(t *testing.T) {
	log := `{"type":"session","version":4,"id":"session-blocks","createdAt":1790506922540,"cwd":"/work/pgbouncer-lab","isSeeded":false,"delegationDepth":0}
{"type":"user/message","seq":1,"time":1790506922600,"data":{"content":[{"type":"text","text":"show the chart"}],"source":{"kind":"user"},"role":"user"}}
{"type":"tool/call","seq":2,"time":1790506922700,"data":{"callId":"c1","name":"read_image","arguments":"{\"file_path\": \"/work/pgbouncer-lab/pool.png\"}"}}
{"type":"tool/result","seq":3,"time":1790506922710,"data":{"message":{"role":"tool","source":{"kind":"tool","callId":"c1"},"toolCallId":"c1","content":[{"type":"text","text":"chartoutput pool usage"},{"type":"image","text":"imageleak","data":"AAAA","mediaType":"image/png"},{"type":"plugin:annotate","text":"pluginleak","content":[{"type":"text","text":"nestedleak"}]}],"id":"t1"}}}
`
	path := filepath.Join(t.TempDir(), "session-blocks", "session.v4.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(log), 0o644); err != nil {
		t.Fatal(err)
	}
	ss, err := ParseDeepSeekFile(path)
	if err != nil || len(ss) != 1 {
		t.Fatalf("parse: %v, %d sessions", err, len(ss))
	}
	var out []string
	for _, m := range ss[0].Messages {
		if m.Role == RoleToolOutput {
			out = append(out, m.Text)
		}
	}
	if got := strings.Join(out, "|"); got != "chartoutput pool usage" {
		t.Errorf("tool output = %q, want only the text block", got)
	}
}

// One generation held raw and framed means dsh's encoding setting changed, and
// dsh reads only the one it is set to: the file written last is the session.
func TestDeepSeekKeepsOneEncodingOfAGeneration(t *testing.T) {
	tmp := hermeticSourcesEnv(t)
	root := filepath.Join(tmp, "dsh", "sessions")
	t.Setenv("DSH_HOME", filepath.Join(tmp, "dsh"))
	t.Setenv("DEJA_DEEPSEEK_ROOT", root)
	dir := filepath.Join(root, "--work-pgbouncer-lab--", "session-tie")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	framed, raw := filepath.Join(dir, "session.v4.jsonl.zstd"), filepath.Join(dir, "session.v4.jsonl")
	for _, p := range []string{framed, raw} {
		if err := os.WriteFile(p, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(framed, old, old); err != nil {
		t.Fatal(err)
	}
	if got := DeepSeekSessionFiles(); !slices.Equal(got, []string{raw}) {
		t.Errorf("DeepSeekSessionFiles = %q, want only the raw log written last", got)
	}
	if !DeepSeekLogSupersedes(raw, framed) {
		t.Error("the raw log written last does not supersede the framed one")
	}
}
