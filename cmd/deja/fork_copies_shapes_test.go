package main

import (
	"math/rand"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/search"
)

func TestCopyMessageShapesExact(t *testing.T) {
	at := time.Date(2026, 10, 21, 1, 2, 3, 4, time.UTC)
	zero := time.Time{}
	wrapped := time.Unix(0, zero.UnixNano())
	if wrapped.IsZero() || wrapped.UnixNano() != zero.UnixNano() {
		t.Fatal("fixture must share UnixNano while differing in IsZero")
	}
	m := model.Message{Role: "user", Time: at, Text: "quote\" slash\\ NUL\x00 мир"}
	other := m
	other.Text += "!"
	variants := [][]model.Message{
		nil, {}, {m}, {m}, {other}, {m, other}, {other, m}, {m, m},
		{{Role: "user", Time: zero, Text: "same"}},
		{{Role: "user", Time: wrapped, Text: "same"}},
		{{Role: "user\x00", Time: at, Text: "x"}},
		{{Role: "user", Time: at, Text: "\x00x"}},
		{{Role: "assistant", Time: at, Text: "No response requested."}},
		{{Role: "summary", Time: at, Text: " "}},
		{{Role: "user", Time: at.In(time.FixedZone("elsewhere", 7200)), Text: m.Text}},
	}
	ss := make([]model.Session, len(variants))
	for i, messages := range variants {
		ss[i] = model.Session{ID: "duplicate", Messages: messages}
	}
	classes := copyMessageShapes(ss)
	for i := range ss {
		for j := range ss {
			want := i == j || (i < 2 && j < 2) || ((i == 2 || i == 3 || i == 14) && (j == 2 || j == 3 || j == 14))
			if got := classes[i] == classes[j]; got != want {
				t.Fatalf("shape equality (%d,%d): got %v want %v", i, j, got, want)
			}
			if classes[i] == classes[j] && (search.CopyOf(ss[i], ss[j]) || search.CopyOf(ss[j], ss[i])) {
				t.Fatalf("equal shape changed CopyOf (%d,%d)", i, j)
			}
		}
	}
}

// referenceCopySources preserves the frozen candidate's selection and full-record fallback.
func referenceCopySources(dir string, ss []model.Session, maybe func(src, c int) bool) map[int]int {
	out := map[int]int{}
	var unread [][2]int
	for i, c := range ss {
		for j, src := range ss {
			if i == j || c.Harness != src.Harness || !maybe(j, i) {
				continue
			}
			if search.SourceOf(ss, src, c, src.Started) && !search.SourceOf(ss, c, src, c.Started) {
				out[i] = j
				break
			}
			unread = append(unread, [2]int{i, j})
		}
	}
	if len(unread) == 0 || dir == "" {
		return out
	}
	var ids []index.Identity
	seen := map[int]bool{}
	for _, p := range unread {
		for _, k := range p {
			if !seen[k] {
				seen[k] = true
				ids = append(ids, index.Identity{Harness: ss[k].Harness, ID: ss[k].ID})
			}
		}
	}
	full, err := index.FindManyByIdentity(dir, ids)
	if err != nil {
		return out
	}
	whole := map[string]model.Session{}
	for _, s := range full {
		whole[s.Harness+":"+s.ID] = s
	}
	for _, p := range unread {
		i, j := p[0], p[1]
		if _, done := out[i]; done {
			continue
		}
		c, okc := whole[ss[i].Harness+":"+ss[i].ID]
		src, oks := whole[ss[j].Harness+":"+ss[j].ID]
		if okc && oks && search.CopyOf(c, src) {
			out[i] = j
		}
	}
	return out
}

func TestCopySourcesShapesDifferential(t *testing.T) {
	rng := rand.New(rand.NewSource(23)) //nolint:gosec // seeded for a reproducible differential
	at := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	times := []time.Time{{}, at, at.Add(time.Second), time.Unix(0, time.Time{}.UnixNano())}
	roles := []string{"user", "assistant", "summary", "user\x00"}
	texts := []string{"same", " ", "No response requested.", "\"\\\x00мир"}
	kinds := []string{"", "fork", "subagent", "subagent_fork"}
	for round := range 300 {
		ss := make([]model.Session, 8)
		for i := range ss {
			ss[i] = model.Session{ID: strconv.Itoa(rng.Intn(5)), Harness: strconv.Itoa(rng.Intn(2)), Kind: kinds[rng.Intn(len(kinds))], Parent: strconv.Itoa(rng.Intn(5)), Started: times[rng.Intn(len(times))]}
			if i > 0 && rng.Intn(2) == 0 {
				ss[i].Messages = ss[rng.Intn(i)].Messages
			} else {
				for range rng.Intn(5) {
					ss[i].Messages = append(ss[i].Messages, model.Message{Role: roles[rng.Intn(len(roles))], Time: times[rng.Intn(len(times))], Text: texts[rng.Intn(len(texts))]})
				}
			}
		}
		maybe := func(src, c int) bool { return (src+c+round)%3 != 0 }
		want := referenceCopySources("", ss, maybe)
		got := copySources("", ss, func(src, c int, _ bool) bool { return maybe(src, c) })
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("round %d got %v want %v sessions=%#v", round, got, want, ss)
		}
	}
}

func TestCopySourcesShapesFullRecordFallback(t *testing.T) {
	dir := claudeForkStore(t, false)
	if err := cmdSearch(dir, []string{"zebracount", "--json"}, ""); err != nil {
		t.Fatal(err)
	}
	ids := []index.Identity{
		{Harness: "claude", ID: "aaaaaaaa-0000-4000-8000-00000000000a"},
		{Harness: "claude", ID: "ffffffff-0000-4000-8000-00000000000f"},
	}
	ss, err := index.FindManyByIdentity(dir, ids)
	if err != nil || len(ss) != 2 {
		t.Fatalf("full records: %d, %v", len(ss), err)
	}
	for i := range ss {
		ss[i].Messages = ss[i].Messages[:1]
	}
	if classes := copyMessageShapes(ss); classes[0] != classes[1] {
		t.Fatal("partial records must have equal shapes")
	}
	maybe := func(_, _ int) bool { return true }
	want := referenceCopySources(dir, ss, maybe)
	got := copySources(dir, ss, func(_, _ int, _ bool) bool { return true })
	if len(want) != 1 || !reflect.DeepEqual(got, want) {
		t.Fatalf("partial-equal/full-divergent: got %v want %v", got, want)
	}
}
