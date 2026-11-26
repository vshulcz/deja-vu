package sources

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
)

const dbCursorVersion = 1

var sqliteDecodedRows atomic.Int64

// ResetSQLiteDecodedRows zeros the SQLite row decode counter used by delta tests.
func ResetSQLiteDecodedRows() { sqliteDecodedRows.Store(0) }

// SQLiteDecodedRows returns the number of domain rows decoded from sqlite3 output.
func SQLiteDecodedRows() int64 { return sqliteDecodedRows.Load() }

// sqliteDecodeRow decodes one JSON row object and tracks it for delta budgets.
func sqliteDecodeRow(dec *json.Decoder, dst any) error {
	if err := dec.Decode(dst); err != nil {
		return err
	}
	sqliteDecodedRows.Add(1)
	return nil
}

type sqliteTableSpec struct {
	Name    string
	KeyExpr string
}

type sqliteTableCursor struct {
	MaxRowID   int64  `json:"max_rowid"`
	LastKey    string `json:"last_key,omitempty"`
	FirstRowID int64  `json:"first_rowid,omitempty"`
	FirstKey   string `json:"first_key,omitempty"`
}

type dbCursorState struct {
	Version int                          `json:"v"`
	Tables  map[string]sqliteTableCursor `json:"tables,omitempty"`
}

type sqliteInspectRow struct {
	T      string `json:"t"`
	MaxR   int64  `json:"max_r"`
	LastK  string `json:"last_k"`
	FirstR int64  `json:"first_r"`
	FirstK string `json:"first_k"`
	PrevK  string `json:"prev_k"`
}

func quoteSQLiteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func sqliteTableAnchorValid(p, cur sqliteTableCursor, keyAtPrevMax string) bool {
	if cur.MaxRowID < p.MaxRowID {
		return false
	}
	if p.MaxRowID == 0 {
		return true
	}
	if p.FirstRowID > 0 {
		if cur.FirstRowID < p.FirstRowID {
			return false
		}
		if cur.FirstRowID == p.FirstRowID && cur.FirstKey != p.FirstKey {
			return false
		}
	}
	if cur.MaxRowID == p.MaxRowID {
		return keyAtPrevMax == p.LastKey
	}
	if keyAtPrevMax != "" {
		return keyAtPrevMax == p.LastKey
	}
	return p.FirstRowID > 0 && cur.FirstRowID == p.FirstRowID && cur.FirstKey == p.FirstKey
}

func sqliteInspectTables(db string, specs []sqliteTableSpec, prev *dbCursorState) (next dbCursorState, prevOK, ok bool) {
	if len(specs) == 0 {
		return dbCursorState{}, false, false
	}
	if fi, err := os.Stat(db); err != nil || fi.Size() == 0 {
		return dbCursorState{}, false, false
	}
	parts := make([]string, 0, len(specs))
	for _, s := range specs {
		tbl := quoteSQLiteIdent(s.Name)
		keyCol := s.KeyExpr
		prevExpr := `''`
		if prev != nil {
			if pt, has := prev.Tables[s.Name]; has && pt.MaxRowID > 0 {
				prevExpr = fmt.Sprintf(`coalesce((select %s from %s where rowid=%d),'')`, keyCol, tbl, pt.MaxRowID)
			}
		}
		parts = append(parts, fmt.Sprintf(
			`select json_object('t',%s,'max_r',coalesce((select max(rowid) from %s),0),`+
				`'last_k',coalesce((select %s from %s order by rowid desc limit 1),''),`+
				`'first_r',coalesce((select min(rowid) from %s),0),`+
				`'first_k',coalesce((select %s from %s order by rowid asc limit 1),''),`+
				`'prev_k',%s)`,
			sqliteLiteral(s.Name), tbl, keyCol, tbl, tbl, keyCol, tbl, prevExpr))
	}
	out, err := sqliteOutput(db, strings.Join(parts, " union all "))
	if err != nil {
		return dbCursorState{}, false, false
	}
	var rows []sqliteInspectRow
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.UseNumber()
	for dec.More() {
		var r sqliteInspectRow
		if err := dec.Decode(&r); err != nil {
			return dbCursorState{}, false, false
		}
		rows = append(rows, r)
	}
	if len(rows) != len(specs) {
		return dbCursorState{}, false, false
	}
	next = dbCursorState{
		Version: dbCursorVersion,
		Tables:  make(map[string]sqliteTableCursor, len(rows)),
	}
	prevOK = prev != nil && prev.Version == dbCursorVersion && len(prev.Tables) > 0
	for _, r := range rows {
		cur := sqliteTableCursor{
			MaxRowID:   r.MaxR,
			LastKey:    r.LastK,
			FirstRowID: r.FirstR,
			FirstKey:   r.FirstK,
		}
		next.Tables[r.T] = cur
		if prevOK {
			pt, has := prev.Tables[r.T]
			if !has || !sqliteTableAnchorValid(pt, cur, r.PrevK) {
				prevOK = false
			}
		}
	}
	return next, prevOK, true
}

func sqliteLiteral(s string) string {
	return `'` + strings.ReplaceAll(s, `'`, `''`) + `'`
}

func decodeDBCursorState(raw string) (dbCursorState, bool) {
	if raw == "" {
		return dbCursorState{}, false
	}
	var st dbCursorState
	if err := json.Unmarshal([]byte(raw), &st); err != nil || st.Version != dbCursorVersion || len(st.Tables) == 0 {
		return dbCursorState{}, false
	}
	return st, true
}

func encodeDBCursorState(st dbCursorState) string {
	if st.Version != dbCursorVersion || len(st.Tables) == 0 {
		return ""
	}
	b, err := json.Marshal(st)
	if err != nil {
		return ""
	}
	return string(b)
}

func captureSQLiteTableCursor(db string, specs []sqliteTableSpec) string {
	cur, _, ok := sqliteInspectTables(db, specs, nil)
	if !ok {
		return ""
	}
	return encodeDBCursorState(cur)
}

func prepareSQLiteDelta(db, cursor string, specs []sqliteTableSpec) (prev dbCursorState, nextToken string, canDelta bool) {
	var prevPtr *dbCursorState
	if parsed, ok := decodeDBCursorState(cursor); ok {
		prevPtr = &parsed
	}
	cur, prevOK, ok := sqliteInspectTables(db, specs, prevPtr)
	if !ok {
		return dbCursorState{}, "", false
	}
	nextToken = encodeDBCursorState(cur)
	if prevPtr == nil || !prevOK {
		return dbCursorState{}, nextToken, false
	}
	return *prevPtr, nextToken, true
}

// sqliteBoundedIDs executes setQuery (returning a single column of text IDs)
// and returns distinct non-empty IDs up to limit. ok is false if the query fails
// or returns more than limit rows.
func sqliteBoundedIDs(db, setQuery string, limit int) ([]string, bool) {
	b, err := sqliteOutput(db, fmt.Sprintf("select * from (%s) limit %d", setQuery, limit+1))
	if err != nil {
		return nil, false
	}
	seen := map[string]bool{}
	var ids []string
	for ln := range strings.SplitSeq(strings.TrimSpace(string(b)), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" && !seen[ln] {
			seen[ln] = true
			ids = append(ids, ln)
		}
	}
	return ids, len(ids) <= limit
}

func sqlInValues(ids []string) string {
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = "'" + sqlEscape(id) + "'"
	}
	return strings.Join(quoted, ",")
}
