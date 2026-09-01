package embed

import "testing"

// Without a switch, New probes localhost — so a developer running a local model
// gets a different answer from CI. That is how two doctor tests were red on a
// clean tree here while green everywhere else, and how `bench recall` quietly
// grew a hybrid arm mid-run.
func TestEmbeddingCanBeSwitchedOff(t *testing.T) {
	t.Setenv("DEJA_EMBED_URL", "")
	t.Setenv("DEJA_EMBED_OFF", "1")
	if c, err := New(); err == nil {
		t.Errorf("the probe ran with the switch on: %#v", c)
	}
}

// Saying where to embed is asking for it. The switch is about the probe, not
// about an endpoint someone configured — otherwise the tests that exercise the
// embedding path against a stub server would have no way to run.
func TestAConfiguredEndpointBeatsTheSwitch(t *testing.T) {
	t.Setenv("DEJA_EMBED_OFF", "1")
	t.Setenv("DEJA_EMBED_URL", "http://127.0.0.1:9/embed")
	c, err := New()
	if err != nil {
		t.Fatalf("a configured endpoint was refused: %v", err)
	}
	if c.URL != "http://127.0.0.1:9/embed" {
		t.Errorf("endpoint = %q", c.URL)
	}
}

// One value, the same as DEJA_NO_REDACT: "0" is somebody saying no, and
// reading it as yes turns the switch on for a person trying to turn it off.
func TestOnlyOneSwitchesItOff(t *testing.T) {
	for _, v := range []string{"", "0", "false", "no"} {
		t.Setenv("DEJA_EMBED_OFF", v)
		if Off() {
			t.Errorf("DEJA_EMBED_OFF=%q switched embedding off", v)
		}
	}
	t.Setenv("DEJA_EMBED_OFF", "1")
	if !Off() {
		t.Error(`DEJA_EMBED_OFF="1" did not switch embedding off`)
	}
}
