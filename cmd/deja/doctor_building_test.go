package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// doctor is what people run when memory looks absent, and during the first
// build it called the index missing and told them to start the build that was
// already running (#873).
func TestDoctorSaysWhenTheIndexIsBeingBuilt(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := filepath.Join(tmp, "idx")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	status := warmupStatus{Phase: "reading sessions", Total: 100, Done: 42, Started: time.Now().UnixNano(), Updated: time.Now().UnixNano()}
	b, err := json.Marshal(status)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(warmupStatusPath(dir), b, 0o600); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	doctorIndex(&out, doctorComponent{State: "missing", Path: dir}, dir)
	got := out.String()
	if !strings.Contains(got, "building now (reading sessions 42%)") {
		t.Errorf("doctor does not mention the build in flight:\n%s", got)
	}
	if strings.Contains(got, "run `deja warmup`") {
		t.Errorf("doctor still tells the reader to start what is running:\n%s", got)
	}

	// With no build running, the advice is the right one.
	if err := os.Remove(warmupStatusPath(dir)); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	doctorIndex(&out, doctorComponent{State: "missing", Path: dir}, dir)
	if got := out.String(); !strings.Contains(got, "not built (run `deja warmup`)") {
		t.Errorf("an index that is really missing lost its advice:\n%s", got)
	}
}
