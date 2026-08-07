package sources

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Hermes can keep its session store in PostgreSQL instead of SQLite
// (sessiondb.provider: postgresql). When it does, ~/.hermes/state.db stops
// receiving writes and the whole harness goes dark — deja only globs state.db
// files. DEJA_HERMES_PG_DSN opts the Postgres store in: the same four columns
// (session_id, role, content, timestamp) with the same meaning live in the
// `messages` table, so the same query runs over a DSN (#1018).

// HermesPGDSN is the opt-in Postgres connection string, empty when unset.
func HermesPGDSN() string { return os.Getenv("DEJA_HERMES_PG_DSN") }

// hermesPGWhere is the row filter shared with the SQLite path: prose turns from
// the two roles, tool-call rows (null content) skipped.
const hermesPGWhere = `role in ('user','assistant') and content is not null and content <> ''`

// HermesPGRunner runs one SQL statement against a DSN and returns stdout. It is
// a package var so a test can stand in for psql without a live cluster; the
// default shells out exactly like the SQLite path shells out to sqlite3.
var HermesPGRunner = func(dsn, sql string) ([]byte, error) {
	cmd := exec.Command("psql", dsn, "-tAXq", "-v", "ON_ERROR_STOP=1", "-c", sql)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return nil, fmt.Errorf("psql: %s", strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, err
	}
	return out, nil
}

// SetHermesPGRunner swaps the runner and returns a restore func; for tests in
// other packages that drive the Postgres path through a fake.
func SetHermesPGRunner(f func(dsn, sql string) ([]byte, error)) func() {
	prev := HermesPGRunner
	HermesPGRunner = f
	return func() { HermesPGRunner = prev }
}

// HermesPGStorePath is the stable token that stands in for the Postgres store
// wherever the index expects a file path. The DSN can carry a password, so it
// is hashed rather than stored: the manifest and every diagnostic see
// `hermes-pg:<12 hex>`, never the credentials.
func HermesPGStorePath(dsn string) string {
	sum := sha256.Sum256([]byte(dsn))
	return "hermes-pg:" + hex.EncodeToString(sum[:6])
}

// IsHermesPGStore reports whether a path is the Postgres store token.
func IsHermesPGStore(p string) bool { return strings.HasPrefix(p, "hermes-pg:") }

// HermesPGFingerprint returns (rows, newest-timestamp-nanos) for the Postgres
// store, the two numbers the index diffs to decide the store changed. A DSN has
// no mtime; max(timestamp) is the mtime and count(*) is the size.
func HermesPGFingerprint(dsn string) (rows int64, newestNano int64, err error) {
	// Two columns split on psql's -A separator '|', not on whitespace: Hermes'
	// timestamp is epoch seconds like its SQLite store, but casting to text
	// would tear apart any value that carried a space.
	out, err := HermesPGRunner(dsn,
		`select count(*), coalesce(max(timestamp)::text, '0') from messages where `+hermesPGWhere)
	if err != nil {
		return 0, 0, err
	}
	fields := strings.SplitN(strings.TrimSpace(string(out)), "|", 2)
	if len(fields) != 2 {
		return 0, 0, fmt.Errorf("hermes pg: unexpected fingerprint %q", strings.TrimSpace(string(out)))
	}
	rows, err = strconv.ParseInt(strings.TrimSpace(fields[0]), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("hermes pg: row count %q: %w", fields[0], err)
	}
	ts := strings.TrimSpace(fields[1])
	newest := hermesTime(pgNumber(ts))
	if ts != "0" && newest.IsZero() {
		// An unreadable max(timestamp) would index with a broken watermark and
		// then silently miss every later row. Fail so doctor names it instead.
		return 0, 0, fmt.Errorf("hermes pg: cannot read max(timestamp) %q; deja expects epoch seconds like the sqlite store", ts)
	}
	return rows, newest.UnixNano(), nil
}

// pgNumber turns psql's plain-text number into something hermesTime reads. A
// timestamptz column comes back as an ISO string, which hermesTime also takes.
func pgNumber(s string) any {
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}
	return s
}

// ParseHermesPG reads the Postgres store, optionally only rows newer than
// sinceNano (0 for a full read). The query mirrors the SQLite path and comes
// back as one JSON array, decoded by the shared row reader.
func ParseHermesPG(dsn string, sinceNano int64) ([]model.Session, error) {
	where := hermesPGWhere
	if sinceNano > 0 {
		where += fmt.Sprintf(" and timestamp > %d", time.Unix(0, sinceNano).Unix())
	}
	sql := `select coalesce(json_agg(m),'[]') from (` +
		`select session_id,role,content,timestamp from messages where ` + where +
		` order by session_id,timestamp,id) m`
	out, err := HermesPGRunner(dsn, sql)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(strings.NewReader(string(out)))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("hermes pg: %w", err)
	}
	if d, ok := tok.(json.Delim); !ok || d != '[' {
		return nil, fmt.Errorf("hermes pg: expected a json array")
	}
	return decodeHermesArray(dec, "hermes", HermesPGStorePath(dsn))
}
