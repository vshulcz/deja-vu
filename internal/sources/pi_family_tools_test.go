package sources

import (
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// piSession is a pi-shaped transcript in /tmp/proj holding lines after the
// header and the first prompt.
func piSession(lines ...string) string {
	return `{"type":"session","version":3,"id":"s1","timestamp":"2026-10-01T10:00:00.000Z","cwd":"/tmp/proj"}` + "\n" +
		`{"type":"message","id":"u1","timestamp":"2026-10-01T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"fix the retry loop"}]}}` + "\n" +
		strings.Join(lines, "\n") + "\n"
}

func piCall(id, name, args string) string {
	return `{"type":"message","id":"a-` + id + `","timestamp":"2026-10-01T10:00:01.000Z","message":{"role":"assistant","content":[{"type":"toolCall","id":"` + id + `","name":"` + name + `","arguments":` + args + `}]}}`
}

func piResult(id, name, text, details string, failed bool) string {
	isErr := "false"
	if failed {
		isErr = "true"
	}
	return `{"type":"message","id":"r-` + id + `","timestamp":"2026-10-01T10:00:02.000Z","message":{"role":"toolResult","toolCallId":"` + id + `","toolName":"` + name + `","content":[{"type":"text","text":` + vocabJSON(text) + `}],"details":` + details + `,"isError":` + isErr + `}}`
}

// powershell is a built-in tool of the pi-coding-agent Kimchi and Senpi ship,
// beside bash and with the same {command, timeout}; the reader took only bash
// and exec for the shell, so a PowerShell run kept its output and lost the
// command, in a Senpi eval cell too (#4523).
func TestPowerShellRunIsACommand(t *testing.T) {
	for _, kind := range []string{"kimchi", "senpi", "pi"} {
		p := writePiFixture(t, piSession(
			piCall("c0", "powershell", `{"command":"go build ./..."}`),
			piResult("c0", "powershell", "package retry", `{}`, false),
			piCall("c1", "powershell", `{"command":"go vet ./retry"}`),
			piResult("c1", "powershell", "vet: unreachable\n\nCommand exited with code 1", `{}`, true),
		))
		got := commandsOf(parseKindForTest(t, kind, p))
		want := []string{"$ go build ./...  → exit 0", "$ go vet ./retry  → exit 1"}
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: commands = %q, want %q", kind, got, want)
		}
	}
	// Senpi's codemode runs powershell only inside an eval cell.
	p := writePiFixture(t, piSession(
		piCall("e0", "eval", `{"code":"await tool.powershell({command: \"go test ./retry\"})"}`),
		piResult("e0", "eval", `{"text":"ok"}`, `{"toolCalls":[{"name":"powershell","ok":true,"args":{"command":"go test ./retry"}}]}`, false),
	))
	if got := commandsOf(parseKindForTest(t, "senpi", p)); len(got) != 1 || got[0] != "$ go test ./retry" {
		t.Errorf("senpi eval: commands = %q, want the cell's powershell run", got)
	}
}

// changesOf is the files, edit and wrote records of a session, in order.
func changesOf(ss []model.Session) []string {
	var out []string
	for _, s := range ss {
		for _, m := range s.Messages {
			switch m.Role {
			case RoleFiles, RoleEdit, RoleWrote:
				out = append(out, m.Role+" "+m.Text)
			}
		}
	}
	return out
}

// omp and gjc edit in modes besides hashline and apply_patch, and the reader
// took none of their keys: omp's replace is {path, old_string, new_string},
// gjc's {path, edits:[{old_text, new_text}]}, and both patch modes are {path,
// edits:[{op, diff}]}. gjc picks replace for Claude, DeepSeek and Qwen, so
// those users' edits were a file name with nothing behind it (#4524).
func TestOmpAndGjcReplaceAndPatchEdits(t *testing.T) {
	const oldDelay, newDelay = "\tdelay := base", "\tdelay := base * time.Duration(attempt)"
	const newJitter = "\treturn time.Duration(rand.Int63n(int64(base)))"
	const created = "const maxAttempts = 5 // retries before the loop gives up"
	backoff, jitter, limits := "/tmp/proj/backoff.go", "/tmp/proj/jitter.go", "/tmp/proj/limits.go"
	rows := []struct {
		name, kind, args string
		failed           bool
		want             []string
	}{
		{"gjc replace", "gjc", `{"path":"backoff.go","edits":[{"old_text":"\tdelay := base","new_text":"\tdelay := base * time.Duration(attempt)","all":false}]}`, false,
			[]string{"files " + backoff, "edit " + backoff + "\n" + oldDelay, "wrote " + WroteRecord(backoff, newDelay)}},
		{"omp replace", "omp", `{"path":"backoff.go","old_string":"\tdelay := base","new_string":"\tdelay := base * time.Duration(attempt)"}`, false,
			[]string{"files " + backoff, "edit " + backoff + "\n" + oldDelay, "wrote " + WroteRecord(backoff, newDelay)}},
		{"omp replace, several", "omp", `{"path":"backoff.go","edits":[{"old_string":"\tdelay := base","new_string":"\tdelay := base * time.Duration(attempt)"}]}`, false,
			[]string{"files " + backoff, "edit " + backoff + "\n" + oldDelay, "wrote " + WroteRecord(backoff, newDelay)}},
		{"gjc patch", "gjc", `{"path":"jitter.go","edits":[{"op":"update","diff":"@@\n-\treturn 0\n+\treturn time.Duration(rand.Int63n(int64(base)))\n"}]}`, false,
			[]string{"files " + jitter, "edit " + jitter + "\n\treturn 0", "wrote " + WroteRecord(jitter, newJitter)}},
		{"omp patch, two hunks", "omp", `{"path":"jitter.go","edits":[{"op":"update","diff":"@@ func a\n context\n-\treturn 0\n+\treturn time.Duration(rand.Int63n(int64(base)))\n@@ func b\n-\treturn 1\n"}]}`, false,
			[]string{"files " + jitter, "edit " + jitter + "\n\treturn 0", "edit " + jitter + "\n\treturn 1", "wrote " + WroteRecord(jitter, newJitter)}},
		{"omp patch, create", "omp", `{"path":"limits.go","edits":[{"op":"create","diff":"package retry\n\n` + created + `\n"}]}`, false,
			[]string{"files " + limits, "wrote " + WroteRecord(limits, created)}},
		{"omp patch, delete", "omp", `{"path":"limits.go","edits":[{"op":"delete"}]}`, false,
			[]string{"files " + limits}},
		{"gjc replace refused", "gjc", `{"path":"backoff.go","edits":[{"old_text":"\tdelay := base","new_text":"\tdelay := base * time.Duration(attempt)"}]}`, true,
			[]string{"files " + backoff}},
	}
	for _, r := range rows {
		p := writePiFixture(t, piSession(piCall("c0", "edit", r.args), piResult("c0", "edit", "Updated", `{}`, r.failed)))
		got := changesOf(parseKindForTest(t, r.kind, p))
		if strings.Join(got, "|") != strings.Join(r.want, "|") {
			t.Errorf("%s: records = %q, want %q", r.name, got, r.want)
		}
	}
}

// omp's default edit mode is hashline in its own syntax: `[path#TAG]`
// sections, `PUT N.=M:` ops whose body rows start with "+", the whole wrapped
// in `*** Begin Patch`. The reader knew only gjc's `§path`, so every omp edit
// left nothing (#4525). The replaced lines come off the result's diff.
func TestOmpHashlineEdit(t *testing.T) {
	const newLoop = "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"
	retry, jitter := "/tmp/proj/retry.go", "/tmp/proj/jitter.go"
	rows := []struct {
		name, input, details string
		failed               bool
		want                 []string
	}{
		{"put", "*** Begin Patch\n[retry.go#1a2b]\nPUT 3.=3:\n+" + newLoop + "\n*** End Patch", `{"path":"retry.go","diff":"-3|\tfor {\n+3|` + strings.ReplaceAll(newLoop, "\t", `\t`) + `"}`, false,
			[]string{"files " + retry, "wrote " + WroteRecord(retry, newLoop), "edit " + retry + "\n\tfor {"}},
		{"bare, two sections", "[retry.go#1A2B]\nPUT >3:\n+" + newLoop + "\n[jitter.go#3c4d]\nCUT 5.=5\n", `{}`, false,
			[]string{"files " + retry + "\n" + jitter, "wrote " + WroteRecord(retry, newLoop)}},
		{"refused", "[retry.go#1a2b]\nPUT 3.=3:\n+" + newLoop, `{}`, true,
			[]string{"files " + retry}},
		// omp takes the path as everything between the brackets less the
		// tag, so a path with "#" in it or one quoted for its space is a
		// file too (qxo/IOa in omp's cli).
		{"hash and quotes", "[src/C#/Retry.cs#1a2b]\nPUT >3:\n+" + newLoop + "\n[\"my proj/jitter.go\"#3c4d]\nCUT 5.=5", `{}`, false,
			[]string{"files /tmp/proj/src/C#/Retry.cs\n/tmp/proj/my proj/jitter.go", "wrote " + WroteRecord("/tmp/proj/src/C#/Retry.cs", newLoop)}},
		// gjc's own form is read as before, a TOML section line in its body
		// included.
		{"gjc", "§retry.toml\n≔1rq\n[server]\nretry_attempts_before_giving_up = 5", `{}`, false,
			[]string{"files /tmp/proj/retry.toml", "wrote " + WroteRecord("/tmp/proj/retry.toml", "retry_attempts_before_giving_up = 5")}},
	}
	for _, r := range rows {
		p := writePiFixture(t, piSession(piCall("c0", "edit", `{"input":`+vocabJSON(r.input)+`}`), piResult("c0", "edit", "Updated retry.go", r.details, r.failed)))
		got := changesOf(parseKindForTest(t, "omp", p))
		if strings.Join(got, "|") != strings.Join(r.want, "|") {
			t.Errorf("%s: records = %q, want %q", r.name, got, r.want)
		}
	}
}

// prime's one tool is ipython, and its edit skill changes a file from inside a
// cell; each change lands on the result as details.diffs[{path, oldStr,
// newStr}], which prime itself reads back to list the files a session edited.
// The reader kept the cell's output only (#4526).
func TestPrimeEditInACellIsRecorded(t *testing.T) {
	const newLoop = "\tfor attempt := 0; attempt < maxAttempts; attempt++ {"
	const jitter = "func backoffJitter(attempt int) time.Duration { return 0 }"
	diffs := `{"status":"ok","stdout":"Edited /tmp/proj/retry.go","diffs":[` +
		`{"path":"/tmp/proj/retry.go","oldStr":"\tfor {","newStr":` + vocabJSON(newLoop) + `,"startLine":3},` +
		`{"path":"jitter.go","oldStr":"","newStr":` + vocabJSON(jitter) + `,"startLine":1}]}`
	p := writePiFixture(t, piSession(
		piCall("c0", "ipython", `{"code":"await edit(path=\"retry.go\", old_str=\"\\tfor {\", new_str=\"…\")"}`),
		piResult("c0", "ipython", "Edited /tmp/proj/retry.go", diffs, false),
		piCall("c1", "ipython", `{"code":"print(1)"}`),
		piResult("c1", "ipython", "1", `{"status":"ok","stdout":"1","diffs":[]}`, false),
	))
	got := changesOf(parseKindForTest(t, "prime", p))
	want := []string{
		"files /tmp/proj/retry.go\n/tmp/proj/jitter.go",
		"edit /tmp/proj/retry.go\n\tfor {",
		"wrote " + WroteRecord("/tmp/proj/retry.go", newLoop),
		"wrote " + WroteRecord("/tmp/proj/jitter.go", jitter),
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("records = %q, want %q", got, want)
	}
}
