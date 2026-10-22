package sources

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The Windsurf fixtures are encrypted protobuf, which no reviewer can read.
// This file is their readable source: each trajectory is described below,
// encoded with the field numbers from #308 and sealed the way the language
// server seals one, with a fixed nonce so the bytes are reproducible.
// TestWindsurfFixturesMatchSource fails when a committed .pb drifts from it;
// DEJA_WRITE_WINDSURF_FIXTURES=1 rewrites them.

const (
	windsurfFixtureChat  = "c4f3a2b1-7e6d-4c5b-9a8f-1e2d3c4b5a69"
	windsurfFixtureError = "8a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
)

func windsurfFixtureDir() string {
	return filepath.Join("..", "..", "fixtures", "registry", "windsurf")
}

// A minimal protobuf encoder, the inverse of pbWalk.
type pbMsg []byte

func (m pbMsg) key(num, wire uint64) pbMsg { return binary.AppendUvarint(m, num<<3|wire) }

func (m pbMsg) varint(num, v uint64) pbMsg {
	return binary.AppendUvarint(m.key(num, pbVarint), v)
}

func (m pbMsg) bytes(num uint64, b []byte) pbMsg {
	m = binary.AppendUvarint(m.key(num, pbBytes), uint64(len(b)))
	return append(m, b...)
}

func (m pbMsg) str(num uint64, s string) pbMsg { return m.bytes(num, []byte(s)) }

func (m pbMsg) fixed32(num uint64, v uint32) pbMsg {
	return binary.LittleEndian.AppendUint32(m.key(num, pbFixed32), v)
}

func (m pbMsg) fixed64(num uint64, v uint64) pbMsg {
	return binary.LittleEndian.AppendUint64(m.key(num, pbFixed64), v)
}

func pbStamp(t time.Time) pbMsg {
	m := pbMsg(nil).varint(1, uint64(t.Unix())) //nolint:gosec // fixture times are after 1970
	if t.Nanosecond() != 0 {
		m = m.varint(2, uint64(t.Nanosecond())) //nolint:gosec // nanoseconds are never negative
	}
	return m
}

func windsurfStepMeta(created, completed time.Time) pbMsg {
	m := pbMsg(nil)
	if !created.IsZero() {
		m = m.bytes(1, pbStamp(created))
	}
	if !completed.IsZero() {
		m = m.bytes(8, pbStamp(completed))
	}
	return m
}

func windsurfUserStep(at time.Time, userResponse, query string) pbMsg {
	in := pbMsg(nil)
	if query != "" {
		in = in.str(1, query)
	}
	if userResponse != "" {
		in = in.str(2, userResponse)
	}
	return pbMsg(nil).bytes(5, windsurfStepMeta(at, time.Time{})).bytes(19, in)
}

func windsurfPlannerStep(created, completed time.Time, response, thinking, modified string) pbMsg {
	p := pbMsg(nil).str(1, response)
	if thinking != "" {
		p = p.str(3, thinking)
	}
	if modified != "" {
		p = p.str(8, modified)
	}
	return pbMsg(nil).bytes(5, windsurfStepMeta(created, completed)).bytes(20, p)
}

func windsurfMeta(created time.Time, workspaces ...[2]string) pbMsg {
	m := pbMsg(nil)
	for _, ws := range workspaces {
		w := pbMsg(nil).str(1, ws[0])
		if ws[1] != "" {
			w = w.str(2, ws[1])
		}
		m = m.bytes(1, w.str(4, "main"))
	}
	return m.bytes(2, pbStamp(created))
}

func windsurfSeal(plain []byte, nonce byte) []byte {
	block, err := aes.NewCipher(windsurfKey)
	if err != nil {
		panic(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	// A nonce of one repeated byte made the committed .pb files start with
	// 01 01 01 01, the MMDF mailbox magic, and file scanners stopped there.
	sum := sha256.Sum256([]byte{nonce})
	n := sum[:windsurfNonce]
	return gcm.Seal(n, n, plain, nil)
}

// windsurfChatTrajectory is a finished conversation: a renamed title, a git
// root above the opened folder, thinking that must stay out, a tool step and
// unknown fields of every wire type, and a reply only in modified_response.
func windsurfChatTrajectory() []byte {
	t0 := time.Date(2026, 7, 28, 10, 0, 0, 0, time.UTC)
	steps := []pbMsg{
		windsurfUserStep(t0.Add(5*time.Second), "why does GET /notes return duplicates after the cursor", ""),
		windsurfPlannerStep(t0.Add(6*time.Second), t0.Add(20*time.Second),
			"The cursor compares created_at, which ties for notes saved in the same second; filter on (created_at, id) instead.",
			"private chain of thought", ""),
		// A tool call deja does not read, beside fields this reader has never
		// seen.
		pbMsg(nil).bytes(5, windsurfStepMeta(t0.Add(21*time.Second), time.Time{})).
			bytes(31, pbMsg(nil).str(1, "run_command").str(2, "go test ./notes")).
			varint(40, 7).fixed32(41, 0xdeadbeef).fixed64(42, 1),
		windsurfUserStep(t0.Add(40*time.Second), "add a test for the tie", ""),
		windsurfPlannerStep(t0.Add(41*time.Second), t0.Add(55*time.Second), "", "",
			"Added TestListCursorTie, which saves two notes in one second and pages through them."),
	}
	m := pbMsg(nil).str(1, "0d9e8f7a-6b5c-4d3e-2f1a-0b9c8d7e6f5a")
	for _, st := range steps {
		m = m.bytes(2, st)
	}
	m = m.varint(4, 1).str(6, windsurfFixtureChat).
		bytes(7, windsurfMeta(t0, [2]string{"file:///home/dev/src/notes-api/server", "file:///home/dev/src/notes-api"})).
		varint(8, 2).str(10, "Cursor pagination duplicates")
	return windsurfSeal(m, 1)
}

// windsurfErrorTrajectory is the shape the live check in #308 produced: the
// service refused the model twice, so there are two prompts, steps holding
// only metadata and an error, and no planner response at all.
func windsurfErrorTrajectory() []byte {
	t0 := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	prompt := "Do not use tools or edit files. Reply exactly with: DEJA_SYNTHETIC_ASSISTANT_01"
	errStep := func(at time.Time) pbMsg {
		return pbMsg(nil).bytes(5, windsurfStepMeta(at, at)).
			bytes(24, pbMsg(nil).str(1, "permission_denied: We're currently facing high demand for this model."))
	}
	m := pbMsg(nil).str(1, "5e4d3c2b-1a09-4f8e-9d7c-6b5a4f3e2d1c").
		bytes(2, windsurfUserStep(t0.Add(time.Second), prompt, "")).
		bytes(2, errStep(t0.Add(3*time.Second))).
		bytes(2, windsurfUserStep(t0.Add(30*time.Second), prompt, "")).
		bytes(2, errStep(t0.Add(32*time.Second))).
		bytes(2, pbMsg(nil).bytes(5, windsurfStepMeta(t0.Add(33*time.Second), time.Time{}))).
		str(6, windsurfFixtureError).
		bytes(7, windsurfMeta(t0, [2]string{"file:///tmp/deja-windsurf-synthetic", ""}))
	return windsurfSeal(m, 2)
}

func TestWindsurfFixturesMatchSource(t *testing.T) {
	for name, want := range map[string][]byte{
		windsurfFixtureChat:  windsurfChatTrajectory(),
		windsurfFixtureError: windsurfErrorTrajectory(),
	} {
		path := filepath.Join(windsurfFixtureDir(), "cascade", name+".pb")
		if os.Getenv("DEJA_WRITE_WINDSURF_FIXTURES") == "1" {
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s differs from its source in this file; rerun with DEJA_WRITE_WINDSURF_FIXTURES=1", path)
		}
	}
}
