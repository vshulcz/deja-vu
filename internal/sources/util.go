package sources

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

type Source struct{ Name, Root string }

func Home() string { h, _ := os.UserHomeDir(); return h }
func EnvPath(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func parseTimeAny(v any) time.Time {
	switch x := v.(type) {
	case string:
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return t
		}
	case float64:
		return unixGuess(int64(x))
	case json.Number:
		n, _ := x.Int64()
		return unixGuess(n)
	}
	return time.Time{}
}

func unixGuess(n int64) time.Time {
	if n > 1e12 {
		return time.UnixMilli(n)
	}
	if n > 0 {
		return time.Unix(n, 0)
	}
	return time.Time{}
}

func textFromContent(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var b strings.Builder
		for _, it := range c {
			if m, ok := it.(map[string]any); ok {
				if txt, _ := m["text"].(string); txt != "" {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(txt)
				} else if s, _ := m["content"].(string); s != "" {
					if b.Len() > 0 {
						b.WriteByte('\n')
					}
					b.WriteString(s)
				}
			}
		}
		return b.String()
	}
	return ""
}

func projectName(path string) string {
	if path == "" {
		return "-"
	}
	return filepath.Base(path)
}

func scanJSONL(path string, fn func(map[string]any)) error {
	return scanJSONLFromOffset(path, 0, fn)
}

func scanJSONLFromOffset(path string, offset int64, fn func(map[string]any)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	r := bufio.NewReaderSize(f, 1024*1024)
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			var m map[string]any
			d := json.NewDecoder(strings.NewReader(string(line)))
			d.UseNumber()
			if d.Decode(&m) == nil {
				fn(m)
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func walkFiles(root string, pred func(string) bool) []string {
	var out []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && d.Type()&os.ModeSymlink == 0 && !d.IsDir() && pred(p) {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func parseFiles(files []string, parse func(string) ([]model.Session, error)) []model.Session {
	files = append([]string(nil), files...)
	sort.Strings(files)
	type job struct {
		i int
		p string
	}
	jobs := make(chan job)
	outs := make(chan struct {
		i  int
		ss []model.Session
	})
	var wg sync.WaitGroup
	n := runtime.NumCPU()
	if len(files) < n {
		n = len(files)
	}
	if n == 0 {
		return nil
	}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				ss, _ := parse(j.p)
				outs <- struct {
					i  int
					ss []model.Session
				}{i: j.i, ss: ss}
			}
		}()
	}
	go func() {
		for i, f := range files {
			jobs <- job{i: i, p: f}
		}
		close(jobs)
		wg.Wait()
		close(outs)
	}()
	byFile := make([][]model.Session, len(files))
	for out := range outs {
		byFile[out.i] = out.ss
	}
	var all []model.Session
	for _, ss := range byFile {
		all = append(all, ss...)
	}
	return all
}
