package policy

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/atomicfile"
)

// Settled records the friction walls a person has said are fixed, by the
// handle `deja friction` prints, with when they said so (#5628).
//
// It lives beside policy.json rather than in the index for the reason the peer
// list does: a rebuild re-derives every wall from the transcripts, and a
// settle kept there would come back undone. A file of its own rather than a key
// in policy.json, because policy.json is written by hand and this one is
// written by deja.
type Settled map[string]time.Time

type settledFile struct {
	Walls map[string]time.Time `json:"walls"`
}

// SettledPath is where the settled walls live, next to the policy file. ""
// when the policy file has nowhere to be.
func SettledPath() string {
	p := Path()
	if p == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(p), "settled.json")
}

// LoadSettled reads the settled walls. A missing or malformed file settles
// nothing: the block it feeds then says what it said before, which is the
// failure that costs least.
func LoadSettled() Settled {
	b, err := os.ReadFile(SettledPath())
	if err != nil {
		return nil
	}
	var f settledFile
	if json.Unmarshal(b, &f) != nil {
		return nil
	}
	return Settled(f.Walls)
}

// Hides reports whether a settle still holds for a wall whose newest session
// started at newest. A session started after the settle that hit the wall
// again means it was not fixed after all, and the wall is back. Started rather
// than updated: the session the settle was typed in may well have hit the wall
// before it, and it goes on being updated afterwards.
func (s Settled) Hides(handle string, newest time.Time) bool {
	at, ok := s[strings.ToLower(handle)]
	return ok && !newest.After(at)
}

// SettleWall records handle as settled at at.
func SettleWall(handle string, at time.Time) error {
	s := LoadSettled()
	if s == nil {
		s = Settled{}
	}
	s[strings.ToLower(handle)] = at.UTC().Truncate(time.Second)
	return writeSettled(s)
}

// UnsettleWall drops a settle, and reports whether there was one.
func UnsettleWall(handle string) (bool, error) {
	s := LoadSettled()
	if _, ok := s[strings.ToLower(handle)]; !ok {
		return false, nil
	}
	delete(s, strings.ToLower(handle))
	return true, writeSettled(s)
}

func writeSettled(s Settled) error {
	p := SettledPath()
	if p == "" {
		return errors.New("no home directory to keep settled walls in")
	}
	b, err := json.MarshalIndent(settledFile{Walls: s}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return atomicfile.Write(p, append(b, '\n'), 0o600)
}
