package policy

import (
	"os"
	"path/filepath"
	"testing"
)

// "auto_index": false is the file form of DEJA_NO_AUTO_INDEX (#5385), and a
// key Diagnose knows.
func TestAutoIndexOff(t *testing.T) {
	p := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", p)
	for body, want := range map[string]bool{
		`{}`:                    false,
		`{"auto_index": true}`:  false,
		`{"auto_index": false}`: true,
	} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := AutoIndexOff(); got != want {
			t.Errorf("%s: AutoIndexOff = %v, want %v", body, got, want)
		}
		if _, unknown, err := Diagnose(); err != nil || len(unknown) != 0 {
			t.Errorf("%s: Diagnose unknown=%v err=%v", body, unknown, err)
		}
	}
}

// "search_only": true is the file form of DEJA_SEARCH_ONLY (#5395).
func TestSearchOnlyOn(t *testing.T) {
	p := filepath.Join(t.TempDir(), "policy.json")
	t.Setenv("DEJA_POLICY_FILE", p)
	for body, want := range map[string]bool{
		`{}`:                     false,
		`{"search_only": false}`: false,
		`{"search_only": true}`:  true,
	} {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := SearchOnlyOn(); got != want {
			t.Errorf("%s: SearchOnlyOn = %v, want %v", body, got, want)
		}
		if _, unknown, err := Diagnose(); err != nil || len(unknown) != 0 {
			t.Errorf("%s: Diagnose unknown=%v err=%v", body, unknown, err)
		}
	}
}
