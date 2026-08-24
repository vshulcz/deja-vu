package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/index"
)

// ping is part of the protocol version this server claims, and a host that
// uses it for keepalive is entitled to read an error as a dead server. The
// templates list goes with it: we declare a resources capability, so clients
// ask what templates it has (#1720).
func TestMCPAnswersPingAndResourceTemplates(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	t.Setenv("DEJA_INDEX_DIR", t.TempDir()+"/index.db")

	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"resources/templates/list","params":{}}`,
	}, "\n") + "\n"

	var out bytes.Buffer
	if err := serveMCP(index.DefaultDir(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d responses: %q", len(lines), out.String())
	}
	for _, line := range lines {
		var resp struct {
			ID     int             `json:"id"`
			Error  json.RawMessage `json:"error"`
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal([]byte(line), &resp); err != nil {
			t.Fatal(err)
		}
		if len(resp.Error) > 0 {
			t.Errorf("id %d answered with an error: %s", resp.ID, resp.Error)
			continue
		}
		if resp.ID == 3 && !bytes.Contains(resp.Result, []byte("resourceTemplates")) {
			t.Errorf("templates list is not in the shape the protocol defines: %s", resp.Result)
		}
	}
}
