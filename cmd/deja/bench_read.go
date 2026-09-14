package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/bench"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/sources"
)

// `deja bench read` measures the one stage every other benchmark leaves out:
// getting a database-backed store off disk and into sessions.
//
// The ingest benchmark measures what an update costs once the sessions exist,
// and the recall, prompt, context and block benchmarks all run against a corpus
// that is already indexed. So the readers had no number at all, and the sqlite3
// shell's `-json` mode — quadratic in the characters it escapes — sat in every
// one of them for two months while every benchmark stayed flat. One 6.16 MB
// value on a real store needed 2287s of it; the fixtures that did run were
// kilobytes, where the same path costs microseconds (#3553, #3552).
//
// Two arms, because the cost is not a function of the store's size: an
// ordinary store, and the same store with one long escape-heavy tool output in
// it. What matters is the ratio — a reader that is linear in what it escapes
// keeps them within a few times each other, and one that is not blows the
// second arm up without touching the first.

// readClass is one store and what reading it cost.
type readClass struct {
	Name     string  `json:"name"`
	WallMS   float64 `json:"wall_ms"`
	StoreMB  float64 `json:"store_mb"`
	Sessions int     `json:"sessions"`
	Messages int     `json:"messages"`
}

type readReport struct {
	CorpusHash string      `json:"corpus_hash"`
	Seed       int64       `json:"seed"`
	Classes    []readClass `json:"classes"`
	Ratio      float64     `json:"ratio"`
	Skipped    string      `json:"skipped,omitempty"`
}

// readBenchLongValueKB is the single long value the second arm carries. Four
// megabytes is what a pasted build log or a diff-heavy tool result comes to on
// a real store, and it is where the difference between the two readers stops
// being arguable: 0.04s against 412s.
const readBenchLongValueKB = 4096

func runBenchRead(args []string) error {
	jsonOutput, seed, err := parseBenchArgs("read", args)
	if err != nil {
		return err
	}
	report, err := measureRead(seed)
	if err != nil {
		return err
	}
	if jsonOutput {
		return json.NewEncoder(os.Stdout).Encode(report)
	}
	printReadReport(os.Stdout, report)
	return nil
}

func measureRead(seed int64) (readReport, error) {
	corpus := bench.Generate(seed)
	report := readReport{CorpusHash: corpus.Hash, Seed: seed}
	if !sources.SQLite3Available() {
		report.Skipped = "sqlite3 is not installed, and the database-backed stores are read through it"
		return report, nil
	}
	dir, err := os.MkdirTemp("", "deja-bench-read-")
	if err != nil {
		return readReport{}, err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	for _, arm := range []struct {
		name   string
		longKB int
	}{
		{name: "ordinary store"},
		{name: "one long tool output", longKB: readBenchLongValueKB},
	} {
		db := filepath.Join(dir, strings.ReplaceAll(arm.name, " ", "-")+".db")
		if err := writeOpencodeBenchStore(db, corpus.Sessions, arm.longKB); err != nil {
			return readReport{}, err
		}
		start := time.Now()
		ss, err := sources.ParseOpencodeDB(db)
		took := time.Since(start)
		if err != nil {
			return readReport{}, fmt.Errorf("read %s: %w", arm.name, err)
		}
		msgs := 0
		for _, s := range ss {
			msgs += len(s.Messages)
		}
		size := int64(0)
		if fi, statErr := os.Stat(db); statErr == nil {
			size = fi.Size()
		}
		report.Classes = append(report.Classes, readClass{
			Name:     arm.name,
			WallMS:   float64(took.Microseconds()) / 1000,
			StoreMB:  float64(size) / (1024 * 1024),
			Sessions: len(ss),
			Messages: msgs,
		})
	}
	if len(report.Classes) == 2 && report.Classes[0].WallMS > 0 {
		report.Ratio = report.Classes[1].WallMS / report.Classes[0].WallMS
	}
	return report, nil
}

// writeOpencodeBenchStore puts the corpus into a store shaped like opencode's,
// which is the schema three of the database-backed readers resemble closely
// enough for one of them to stand for the stage. longKB, when set, adds one
// bash result of that size whose text is mostly quotes and backslashes — the
// shape a JSON blob or a diff pasted into a tool result has.
func writeOpencodeBenchStore(db string, ss []model.Session, longKB int) error {
	var b strings.Builder
	b.WriteString(`pragma journal_mode=off;
create table session(id text, directory text, time_created any, time_updated any);
create table message(id text, session_id text, time_created any, data text);
create table part(id text, message_id text, data text);
begin;
`)
	stamp := time.Date(2099, 1, 2, 3, 4, 5, 0, time.UTC)
	for i, s := range ss {
		id := fmt.Sprintf("bench-%04d", i)
		fmt.Fprintf(&b, "insert into session values(%s,%s,'%s','%s');\n",
			sqlText(id), sqlText("/bench/"+s.Project), stamp.Format(time.RFC3339), stamp.Format(time.RFC3339))
		for j, m := range s.Messages {
			mid := fmt.Sprintf("%s-m%03d", id, j)
			// The blobs are marshalled rather than assembled: a transcript is
			// full of quotes, and hand-written JSON around it is one apostrophe
			// away from a store that does not parse.
			message := benchJSON(benchMessageBlob{Role: roleOrUser(m.Role)})
			// Field order matters: the reader gates on `"type":"text"` inside
			// the first 120 bytes of the blob, which is what keeps it from
			// parsing every part in the table. A map would sort the keys and
			// push the type past a long turn, and the fixture would then
			// measure a store the reader skips.
			part := benchJSON(benchPartBlob{
				Type: "text",
				Text: m.Text,
				Time: benchPartTime{Start: stamp.Format(time.RFC3339)},
			})
			fmt.Fprintf(&b, "insert into message values(%s,%s,%d,%s);\n",
				sqlText(mid), sqlText(id), stamp.UnixMilli(), sqlText(message))
			fmt.Fprintf(&b, "insert into part values(%s,%s,%s);\n",
				sqlText(mid+"-p"), sqlText(mid), sqlText(part))
		}
	}
	if longKB > 0 {
		// Built inside SQLite so the script stays small: hex with every
		// sixteenth byte replaced by a quote is the escape-heavy shape, and a
		// bash result is where a store of this kind keeps its long values.
		fmt.Fprintf(&b, `insert into part select 'bench-long-p','bench-0000-m000',json_object(
  'type','tool','tool','bash',
  'state',json_object('input',json_object('command','go test ./...'),
                      'output',replace(hex(randomblob(%d)),'A','"')),
  'time',json_object('start','%s'));
`, longKB*1024/2, stamp.Format(time.RFC3339))
	}
	b.WriteString("commit;\n")

	script := db + ".sql"
	if err := os.WriteFile(script, []byte(b.String()), 0o600); err != nil {
		return err
	}
	defer func() { _ = os.Remove(script) }()
	f, err := os.Open(script)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	cmd := exec.Command("sqlite3", db)
	cmd.Stdin = f
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("sqlite3 setup: %w: %s", err, out)
	}
	return nil
}

func roleOrUser(role string) string {
	if role == "" {
		return "user"
	}
	return role
}

// sqlText renders a Go string as a SQL literal.
func sqlText(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// The blobs the fixture writes, in the field order opencode writes them.
type benchMessageBlob struct {
	Role string `json:"role"`
}

type benchPartBlob struct {
	Type string        `json:"type"`
	Text string        `json:"text"`
	Time benchPartTime `json:"time"`
}

type benchPartTime struct {
	Start string `json:"start"`
}

// benchJSON marshals a blob for the store, so nothing in a transcript can
// break out of the JSON it is written into.
func benchJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "{}"
	}
	return string(b)
}

func printReadReport(w io.Writer, r readReport) {
	fmt.Fprintln(w, "deja bench read")
	if r.Skipped != "" {
		fmt.Fprintf(w, "skipped: %s\n", r.Skipped)
		return
	}
	fmt.Fprintf(w, "corpus %s, seed %d\n", r.CorpusHash, r.Seed)
	fmt.Fprintf(w, "%-22s %10s %9s %9s %9s\n", "store", "wall ms", "store MB", "sessions", "messages")
	for _, c := range r.Classes {
		fmt.Fprintf(w, "%-22s %10.1f %9.2f %9d %9d\n", c.Name, c.WallMS, c.StoreMB, c.Sessions, c.Messages)
	}
	if r.Ratio > 0 {
		fmt.Fprintf(w, "one long value costs %.1fx the rest of the store put together\n", r.Ratio)
	}
}
