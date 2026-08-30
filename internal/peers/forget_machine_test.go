package peers

import (
	"os"
	"path/filepath"
	"testing"
)

// A peer carries the ssh host and the name the machine calls itself, and every
// surface that shows where work came from prints the machine name. Forget
// matched the host alone, so the word a reader had in front of them was
// answered with "deja does not know a machine called that" (#2405).
func TestForgetTakesTheMachineNameToo(t *testing.T) {
	path := filepath.Join(t.TempDir(), "peers.json")
	t.Setenv("DEJA_PEERS_FILE", path)
	body := `{"peers":[{"host":"vlad@10.0.0.7","machine":"quicksilver"},{"host":"mini"}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	found, err := Forget("quicksilver")
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatalf("forget did not know the machine name deja prints everywhere else")
	}
	left := Load()
	if len(left) != 1 || left[0].Host != "mini" {
		t.Fatalf("forget took the wrong row: %+v", left)
	}

	if found, err := Forget("QuickSilver"); err != nil || found {
		t.Errorf("a machine already forgotten came back: found=%v err=%v", found, err)
	}
}
