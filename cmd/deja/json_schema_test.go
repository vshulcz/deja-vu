package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/search"
	"github.com/vshulcz/deja-vu/internal/stats"
)

func TestJSONSchemaVersionPresent(t *testing.T) {
	t.Run("search", func(t *testing.T) {
		var b bytes.Buffer
		search.Print(&b, nil, search.Options{JSON: true, Fuzzy: true})
		assertSchemaVersion(t, b.Bytes())
	})
	t.Run("search-semantic", func(t *testing.T) {
		var b bytes.Buffer
		search.Print(&b, nil, search.Options{JSON: true, Semantic: true})
		assertSchemaVersion(t, b.Bytes())
	})
	t.Run("stats", func(t *testing.T) {
		report := stats.Build(nil, time.Now())
		b, err := json.Marshal(report)
		if err != nil {
			t.Fatal(err)
		}
		assertSchemaVersion(t, b)
	})
	t.Run("doctor", func(t *testing.T) {
		tmp := hermeticEnv(t)
		if err := os.MkdirAll(filepath.Join(tmp, "home"), 0o755); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		if err := runDoctor(&out, []string{"--json"}, nil, indexDirForTest()); err != nil {
			t.Fatal(err)
		}
		assertSchemaVersion(t, out.Bytes())
	})
}

func assertSchemaVersion(t *testing.T, raw []byte) {
	t.Helper()
	var env struct {
		SchemaVersion int             `json:"schema_version"`
		Hits          json.RawMessage `json:"hits"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatalf("unmarshal: %v\n%s", err, raw)
	}
	if env.SchemaVersion != jsonout.Version {
		t.Fatalf("schema_version = %d, want %d", env.SchemaVersion, jsonout.Version)
	}
}

func TestJSONSchemaRequiredFields(t *testing.T) {
	withStatsStores(t)
	out, err := captureRun(t, "stats", "--json")
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"schema_version"`, `"total_sessions"`, `"total_messages"`, `"harnesses"`, `"date_range"`, `"recalls_served"`} {
		if !strings.Contains(out, key) {
			t.Fatalf("stats json missing %s: %s", key, out)
		}
	}
	out, err = captureRun(t, "blame", "--json", "parser.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Fatalf("blame json should remain a top-level array: %s", out)
	}
}
