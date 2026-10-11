package index

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// checkFrictionTexts holds frictext.bin in dir to what reading the sessions
// back says: the walls of m, an entry for every session carrying one, each
// entry's lines and stamp as the session's tool output gives them, and
// TopFriction's answer the same with the file and without. It returns the
// file's content in a form a rebuild can be compared on.
func checkFrictionTexts(t *testing.T, dir string, m Manifest) string {
	t.Helper()
	ft, ok := decodeFrictionTextsFile(filepath.Join(dir, frictionTextsFile))
	if !ok {
		if len(m.Sessions) > 0 {
			t.Errorf("%s: no readable %s", dir, frictionTextsFile)
		}
		return ""
	}
	walls := frictionWalls(m)
	carriers := frictionCarriers(m, walls)
	if !sameWalls(ft.walls, walls) {
		t.Errorf("friction texts were written for %d walls, the manifest has %d", len(ft.walls), len(walls))
	}
	if !sameKeys(ft.entries, carriers) {
		t.Errorf("friction texts hold %d sessions, %d carry a wall", len(ft.entries), len(carriers))
	}
	all := map[uint64]string{}
	for h := range walls {
		all[h] = ""
	}
	if len(walls) > 0 && loadFrictionTexts(dir, m, all) == nil {
		t.Errorf("friction texts do not answer for the log they sit beside")
	}
	rd := newFrictionReader(dir, m)
	for k, meta := range carriers {
		stamp, lines := uint64(fnvOffset64), map[uint64]string{}
		outs, _ := rd.toolOutput(meta)
		for _, o := range outs {
			stamp = stampText(stamp, o)
			firstFrictionLines(o, walls, lines)
		}
		e := ft.entries[k]
		if fmt.Sprint(e.lines) != fmt.Sprint(lines) {
			t.Errorf("friction texts for %s:\n  file %q\n  read %q", k, e.lines, lines)
		}
		if e.stamp != stamp {
			t.Errorf("friction texts for %s: stamp %x, the session reads %x", k, e.stamp, stamp)
		}
	}
	top := func() string {
		var b strings.Builder
		for _, f := range TopFriction(dir, 0, nil) {
			fmt.Fprintf(&b, "%d %q\n", len(f.Sessions), f.Text)
		}
		return b.String()
	}
	with := top()
	frictionTextsOff = true
	without := top()
	frictionTextsOff = false
	if with != without {
		t.Errorf("TopFriction with the file:\n%s\nreading the sessions:\n%s", with, without)
	}
	var keys []string
	for k := range ft.entries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	fmt.Fprintf(&b, "walls %d\n", len(ft.walls))
	for _, k := range keys {
		fmt.Fprintf(&b, "%s %x %v\n", k, ft.entries[k].stamp, ft.entries[k].lines)
	}
	return b.String()
}
