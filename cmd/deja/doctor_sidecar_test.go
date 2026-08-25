package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/embed"
	"github.com/vshulcz/deja-vu/internal/index"
)

// writeTruncatedSidecar lays down a sidecar with a whole header and a third of
// one vector record after it — a process killed mid-write, which is the failure
// #1319 was about.
func writeTruncatedSidecar(t *testing.T, dir string) {
	t.Helper()
	var b bytes.Buffer
	b.WriteString("DJV1")
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))  // version
	_ = binary.Write(&b, binary.LittleEndian, uint16(8))  // dim
	writeSidecarString(&b, "test-model")                  // model
	writeSidecarString(&b, "gen-1")                       // generation
	_ = binary.Write(&b, binary.LittleEndian, uint64(12)) // count
	_ = binary.Write(&b, binary.LittleEndian, uint64(12)) // covered
	b.Write(make([]byte, 6))                              // and then it stopped
	if err := os.WriteFile(embed.Path(dir), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeSidecarString(b *bytes.Buffer, s string) {
	_ = binary.Write(b, binary.LittleEndian, uint32(len(s)))
	b.WriteString(s)
}

// A sidecar deja cannot parse is a different thing from no sidecar, and every
// other section of the report already says so — sync, policy and a session
// store each have an unreadable state with the reason. Embedding did not, so
// the file that #1319 is about was reported as a file that was never built
// (#1960).
func TestDoctorNamesASidecarItCannotRead(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTruncatedSidecar(t, dir)

	got := collectDoctorEmbed(dir)
	if got == nil {
		t.Fatal("the report has no embedding section at all, so a corrupt sidecar is invisible")
	}
	if got.Sidecar != "unreadable" {
		t.Errorf("sidecar = %q, want unreadable — the file is there and deja cannot parse it", got.Sidecar)
	}
	if got.Error == "" {
		t.Error("nothing says why the sidecar could not be read")
	}

	var out bytes.Buffer
	doctorEmbed(&out, *got)
	// Named as the sidecar, with the reason. "endpoint unreadable" would carry
	// the same word and send the reader after the endpoint instead.
	line := out.String()
	if !strings.Contains(line, "sidecar    unreadable") || !strings.Contains(line, got.Error) {
		t.Errorf("the text report does not name the sidecar and why it failed:\n%s", line)
	}
	if !strings.Contains(line, "endpoint") {
		t.Errorf("the endpoint line is gone, and it is what says whether re-embedding can fix this:\n%s", line)
	}
}

// And silence stays silence: no sidecar and no endpoint is not a fault to
// report, it is a deja nobody has asked to embed anything.
func TestDoctorSaysNothingWhenThereIsNoSidecarAtAll(t *testing.T) {
	hermeticEnv(t)
	dir := index.DefaultDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Nothing embedded and no endpoint is not a fault: the section is left out
	// entirely, which is what the caller checks for.
	if got := collectDoctorEmbed(dir); got != nil {
		t.Errorf("a deja that was never asked to embed anything reported: %#v", got)
	}
}
