package index

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

type ForgetOptions struct {
	Session string
	Project string
	Before  time.Time
	DryRun  bool
}

type ForgetResult struct {
	Sessions   int
	Messages   int
	Tombstones int
}

func privacyDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		h, _ := os.UserHomeDir()
		base = filepath.Join(h, ".config")
	}
	return filepath.Join(base, "deja")
}

func tombstonePath() string { return filepath.Join(privacyDir(), "tombstones") }

func readTombstones() map[string]bool {
	out := map[string]bool{}
	f, err := os.Open(tombstonePath())
	if err != nil {
		return out
	}
	defer func() { _ = f.Close() }()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if v := strings.TrimSpace(s.Text()); v != "" && !strings.HasPrefix(v, "#") {
			out[v] = true
		}
	}
	return out
}

func writeTombstones(set map[string]bool) error {
	if err := os.MkdirAll(privacyDir(), 0o700); err != nil {
		return err
	}
	keys := make([]string, 0, len(set))
	for key := range set {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	f, err := os.OpenFile(tombstonePath(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	for _, key := range keys {
		if _, err := fmt.Fprintln(f, key); err != nil {
			return err
		}
	}
	return nil
}

func filterTombstoned(ss []model.Session) []model.Session {
	return filterTombstonedSet(ss, readTombstones())
}

func filterTombstonedSet(ss []model.Session, dead map[string]bool) []model.Session {
	if len(dead) == 0 {
		return ss
	}
	out := make([]model.Session, 0, len(ss))
	for _, s := range ss {
		if !dead[s.Harness+":"+s.ID] {
			out = append(out, s)
		}
	}
	return out
}

func sessionMatches(meta SessionMeta, o ForgetOptions) bool {
	if o.Session != "" && !strings.HasPrefix(meta.ID, o.Session) {
		return false
	}
	if o.Project != "" && !strings.Contains(strings.ToLower(meta.Project), strings.ToLower(o.Project)) {
		return false
	}
	if !o.Before.IsZero() && !meta.Updated.Before(o.Before) {
		return false
	}
	return o.Session != "" || o.Project != "" || !o.Before.IsZero()
}

func Forget(dir string, o ForgetOptions) (ForgetResult, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	unlock, err := lockDir(dir)
	if err != nil {
		return ForgetResult{}, err
	}
	defer unlock()
	m, err := readManifest(dir)
	if err != nil {
		return ForgetResult{}, err
	}
	dead := readTombstones()
	matched := map[string]bool{}
	result := ForgetResult{}
	for key, meta := range m.Sessions {
		if !sessionMatches(meta, o) {
			continue
		}
		matched[key] = true
		result.Sessions++
		if !dead[key] {
			result.Tombstones++
		}
		if !o.DryRun {
			dead[key] = true
		}
	}
	if o.DryRun || result.Sessions == 0 {
		return result, nil
	}
	for _, r := range readRecordsForForget(dir) {
		if matched[r.Record.Key] {
			result.Messages++
		}
	}
	if err := rebuildWithTombstones(dir, "", "", currentFiles(""), nil, dead); err != nil {
		return result, err
	}
	return result, writeTombstones(dead)
}

func readRecordsForForget(dir string) []OffsetRecord {
	r, err := ReadRecords(dir)
	if err != nil {
		return nil
	}
	return r
}

func Tombstones() []string {
	set := readTombstones()
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func Unforget(prefix string) error {
	set := readTombstones()
	for key := range set {
		if key == prefix || strings.HasSuffix(key, ":"+prefix) || strings.HasPrefix(key, prefix) {
			delete(set, key)
		}
	}
	return writeTombstones(set)
}

func RedactionReport(dir string) (RedactionStats, error) { return Redactions(dir) }
