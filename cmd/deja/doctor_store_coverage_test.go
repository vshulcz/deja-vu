package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// doctor is the surface that answers "where did deja look", so a store it reads
// and never names is a store whose absence from recall cannot be diagnosed.
// Both lists are hand-written; the registry is the one that decides what is
// indexed, so it is what they are checked against (#1738, the shape of #999).
func TestDoctorNamesEveryStoreTheIndexerReads(t *testing.T) {
	tmp := hermeticEnv(t)
	dir := tmp + "/index.db"

	var text bytes.Buffer
	if err := runDoctor(&text, nil, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
	rows := map[string]bool{}
	inStores := false
	for _, line := range strings.Split(text.String(), "\n") {
		if strings.HasPrefix(line, "Harness stores:") {
			inStores = true
			continue
		}
		if inStores {
			if strings.TrimSpace(line) == "" {
				break
			}
			if f := strings.Fields(line); len(f) > 0 {
				rows[f[0]] = true
			}
		}
	}

	var js bytes.Buffer
	if err := runDoctor(&js, []string{"--json"}, stubLookup("1.0.0", true), dir); err != nil {
		t.Fatal(err)
	}
	var report struct {
		Stores []struct {
			Name string `json:"name"`
		} `json:"stores"`
	}
	if err := json.Unmarshal(js.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	inJSON := map[string]bool{}
	for _, s := range report.Stores {
		inJSON[s.Name] = true
	}

	for _, h := range sources.Registry() {
		if !rows[h.Name] {
			t.Errorf("doctor never names the %s store in its Harness stores block", h.Name)
		}
		if !inJSON[h.Name] {
			t.Errorf("doctor --json never names the %s store", h.Name)
		}
	}
}
