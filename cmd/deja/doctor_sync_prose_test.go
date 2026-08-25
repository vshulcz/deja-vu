package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/termwidth"
)

// The Sync section of docs/json-output.md described both strings a peer row
// carries as bounded. Only the error is: a host is a name deja is handed back
// to act on — `deja sync ssh <host>` — and a bounded name names no machine
// (#1839). The encoder escapes a control byte on its own, which is why the text
// report needs a bound and this does not.
//
// This pins the two halves the paragraph describes against what deja emits,
// which is the check the section has not had while it grew (#1869).
func TestTheSyncSectionDescribesWhatTheRowsCarry(t *testing.T) {
	tmp := hermeticEnv(t)
	path := filepath.Join(tmp, "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	host := "box" + strings.Repeat("y", 400) + "\x1b[31m"
	body, err := json.Marshal(map[string]any{"peers": []map[string]string{{
		"host":       host,
		"last_push":  "2026-08-20T10:00:00Z",
		"last_error": "boom " + strings.Repeat("z", 400),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}

	var out strings.Builder
	if err := runDoctor(&out, []string{"--json", "--offline"}, stubLookup("1.0.0", false), index.DefaultDir()); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Sync struct {
			Peers []struct {
				Host      string `json:"host"`
				LastError string `json:"last_error"`
			} `json:"peers"`
		} `json:"sync"`
	}
	if err := json.Unmarshal([]byte(out.String()), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Sync.Peers) != 1 {
		t.Fatalf("want one peer, got %#v", report.Sync.Peers)
	}
	row := report.Sync.Peers[0]
	if row.Host != host {
		t.Errorf("the host was altered, so what the JSON hands back is not a name to sync with: %q", row.Host)
	}
	// In columns, which is the bound safeForStatusline applies: a byte count
	// would call a 200-column line of CJK unbounded and a bound written in
	// bytes correct.
	if cols := termwidth.Columns(row.LastError); cols > 201 {
		t.Errorf("the error is unbounded at %d columns; a remote writes it", cols)
	}
	// The escape reaches the reader as an escape sequence in the JSON string,
	// never as a raw byte a terminal would act on.
	if strings.Contains(out.String(), "\x1b") {
		t.Error("a control byte was written into the JSON unescaped")
	}

	// The same bound with a wide script: two columns to the rune, so a bound
	// that counted bytes or runes would cut in the wrong place — and a test
	// fed only ASCII could not tell the three apart.
	wide := strings.Repeat("計", 400)
	if got := termwidth.Columns(safeForStatusline(wide, 200)); got > 201 {
		t.Errorf("a wide-script error came back %d columns wide", got)
	}

	doc, err := os.ReadFile("../../docs/json-output.md")
	if err != nil {
		t.Fatal(err)
	}
	// Line breaks are the document's own layout: the claim is about the words.
	prose := strings.Join(strings.Fields(string(doc)), " ")
	if strings.Contains(prose, "Both `host` and `last_error` are written elsewhere") {
		t.Error("docs/json-output.md still says both strings are bounded; the host is reported as written")
	}
}
