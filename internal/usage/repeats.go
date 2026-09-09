package usage

import (
	"sort"
	"time"
)

// Repeat is one agent session handed the same kind of injection several times
// in the same second.
type Repeat struct {
	Into  string
	Kind  string
	Count int
	At    time.Time
}

// RepeatedInjections reports sessions that were served the same kind of
// injection more than once inside a second, newest first.
//
// Nothing legitimate does that: the hooks dedupe per session and per prompt,
// and one prompt is one injection. Several inside a second mean several hooks
// running for one event — a config that collected deja's entry more than once,
// which every check before this answered "wired" to. It is the symptom the
// reader actually sees, in the log deja already keeps: the machine in #3421
// was serving one session eight identical blocks per prompt for a day.
func RepeatedInjections(indexDir string) []Repeat {
	type key struct {
		into, kind string
		sec        int64
	}
	seen := map[key]*Repeat{}
	for _, e := range read(Path(indexDir)) {
		// Only what an injection is: an agent asking a tool twice in a second
		// is a busy agent, not a doubled hook.
		if e.Into == "" || !injectedKind(e.Kind) {
			continue
		}
		k := key{e.Into, e.Kind, e.Time.Unix()}
		if r := seen[k]; r != nil {
			r.Count++
			if e.Time.After(r.At) {
				r.At = e.Time
			}
			continue
		}
		seen[k] = &Repeat{Into: e.Into, Kind: e.Kind, Count: 1, At: e.Time}
	}
	var out []Repeat
	for _, r := range seen {
		if r.Count > 1 {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].At.Equal(out[j].At) {
			return out[i].At.After(out[j].At)
		}
		return out[i].Count > out[j].Count
	})
	return out
}
