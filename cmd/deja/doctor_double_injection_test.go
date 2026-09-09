package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// The wiring in #3421 served one session eight identical blocks per prompt for
// a day, and nothing said so: every check read a config and answered "is the
// hook here", which a file holding eight copies answers yes to. The log deja
// already keeps had the evidence — the same session, the same second, eight
// rows.
func TestDoctorNamesASessionServedTwiceInASecond(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	at := time.Now().UTC()
	writeInjectionLog(t, dir,
		usage.Event{Time: at, Kind: usage.KindDejaVu, Bytes: 833, Into: "agent-1"},
		usage.Event{Time: at.Add(300 * time.Microsecond), Kind: usage.KindDejaVu, Bytes: 833, Into: "agent-1"},
		usage.Event{Time: at.Add(600 * time.Microsecond), Kind: usage.KindDejaVu, Bytes: 833, Into: "agent-1"},
	)

	var out bytes.Buffer
	doctorDoubleInjections(&out, dir)
	got := out.String()
	if !strings.Contains(got, "served 3 dejavu injections inside a second") {
		t.Errorf("doctor did not count the repeated injections:\n%s", got)
	}
	if !strings.Contains(got, "deja install --auto") {
		t.Errorf("doctor named no way out of it:\n%s", got)
	}
}

// One injection per prompt is the normal case, and two prompts a second apart
// are not a doubled hook.
func TestDoctorSaysNothingAboutOrdinaryInjections(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	at := time.Now().UTC()
	writeInjectionLog(t, dir,
		usage.Event{Time: at, Kind: usage.KindDejaVu, Bytes: 400, Into: "agent-1"},
		usage.Event{Time: at.Add(3 * time.Second), Kind: usage.KindDejaVu, Bytes: 400, Into: "agent-1"},
		// Two sessions served in the same second is a fleet, not a repeat.
		usage.Event{Time: at, Kind: usage.KindDejaVu, Bytes: 400, Into: "agent-2"},
	)

	var out bytes.Buffer
	doctorDoubleInjections(&out, dir)
	if got := out.String(); got != "" {
		t.Errorf("doctor invented a repeat:\n%s", got)
	}
}

func writeInjectionLog(t *testing.T, dir string, events ...usage.Event) {
	t.Helper()
	var b bytes.Buffer
	for _, e := range events {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(usage.Path(dir), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}
