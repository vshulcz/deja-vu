package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
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
	// Inside one second on purpose: the row groups by the second, so three
	// events 300µs apart around a second boundary land in two of them and the
	// assertion below reads 2 where it wrote 3. A 0.06% flake, which on a
	// suite this size is a red main every few hundred runs.
	at := time.Now().UTC().Truncate(time.Second).Add(100 * time.Millisecond)
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

// An install collapses the entries, so a repeat from before the last one deja
// recorded is fixed history. Reading the whole log kept the row naming the
// morning before the repair, with a remedy the reader had already applied
// (#3697).
func TestDoctorStopsNamingARepeatFromBeforeTheLastInstall(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	at := time.Now().UTC().Add(-2 * time.Hour)
	writeInjectionLog(t, dir,
		usage.Event{Time: at, Kind: usage.KindDejaVu, Bytes: 833, Into: "agent-1"},
		usage.Event{Time: at.Add(300 * time.Microsecond), Kind: usage.KindDejaVu, Bytes: 833, Into: "agent-1"},
	)
	var before bytes.Buffer
	doctorDoubleInjections(&before, dir)
	if before.String() == "" {
		t.Fatal("the row says nothing about a repeat from two hours ago, so there is nothing to bound")
	}
	// The record written since: an install has been through the file.
	if err := os.MkdirAll(filepath.Dir(wiringStatePath()), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wiringStatePath(), []byte(`{"version":"test"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var after bytes.Buffer
	doctorDoubleInjections(&after, dir)
	if got := after.String(); got != "" {
		t.Errorf("the row still names a repeat from before the install that fixed it:\n%s", got)
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
