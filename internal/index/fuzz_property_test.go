package index

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
)

func seedMinimalRecordLog(t *testing.T, dir string) (*os.File, int64, [sha256.Size]byte) {
	t.Helper()
	path := filepath.Join(dir, "records.bin")
	payload := bytes.Repeat([]byte("synthetic-record-log-payload-01\n"), 4)
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	size := int64(len(payload))
	fp, err := recordLogFingerprint(f, size)
	if err != nil {
		t.Fatal(err)
	}
	return f, size, fp
}

func TestCorruptRecordOffsetsHugeCountFallsBackWithoutPanic(t *testing.T) {
	t.Run("huge count uvarint does not panic", func(t *testing.T) {
		dir := t.TempDir()
		f, size, fp := seedMinimalRecordLog(t, dir)
		valid := encodeRecordOffsets(size, 2, fp, map[string][]int64{
			"claude:s1": {0, 32},
		})
		keyIdx := bytes.Index(valid, []byte("claude:s1"))
		if keyIdx < 0 {
			t.Fatal("missing encoded session key")
		}
		cntPos := keyIdx + len("claude:s1")
		var hugeVarint [binary.MaxVarintLen64]byte
		vn := binary.PutUvarint(hugeVarint[:], uint64(1)<<60)
		corrupt := make([]byte, 0, len(valid)+vn)
		corrupt = append(corrupt, valid[:cntPos]...)
		corrupt = append(corrupt, hugeVarint[:vn]...)
		corrupt = append(corrupt, valid[cntPos+1:]...)

		if err := os.WriteFile(filepath.Join(dir, recordOffsetsFile), corrupt, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := readRecordOffsetsFor(dir, f, map[string]bool{"claude:s1": true}); !errors.Is(err, errNoRecordOffsets) {
			t.Fatalf("readRecordOffsetsFor err = %v, want %v", err, errNoRecordOffsets)
		}
		if ro, ok := readRawRecordOffsets(dir, f); ok {
			if _, ok := ro.lookup("claude:s1"); ok {
				t.Fatal("ro.lookup succeeded on corrupt huge count")
			}
		}
	})

	t.Run("huge directory offset is rejected cleanly", func(t *testing.T) {
		dir := t.TempDir()
		f, size, fp := seedMinimalRecordLog(t, dir)
		base := encodeRecordOffsets(size, 2, fp, map[string][]int64{
			"claude:s1": {0, 32},
		})
		for _, badOff := range []uint64{math.MaxUint64 - 16, uint64(1) << 62} {
			corrupt := append([]byte(nil), base...)
			binary.LittleEndian.PutUint64(corrupt[recordOffsetsHeaderSize+8:recordOffsetsHeaderSize+16], badOff)
			if err := os.WriteFile(filepath.Join(dir, recordOffsetsFile), corrupt, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readRecordOffsetsFor(dir, f, map[string]bool{"claude:s1": true}); !errors.Is(err, errNoRecordOffsets) {
				t.Fatalf("readRecordOffsetsFor(badOff=%d) err = %v, want %v", badOff, err, errNoRecordOffsets)
			}
			if ro, ok := readRawRecordOffsets(dir, f); ok {
				if _, ok := ro.lookup("claude:s1"); ok {
					t.Fatalf("ro.lookup(badOff=%d) unexpectedly succeeded", badOff)
				}
			}
		}
	})

	t.Run("huge nkeys header does not overflow dataStart", func(t *testing.T) {
		dir := t.TempDir()
		f, size, fp := seedMinimalRecordLog(t, dir)
		base := encodeRecordOffsets(size, 1, fp, map[string][]int64{
			"claude:s1": {0},
		})
		for _, badN := range []uint32{0x10000001, math.MaxUint32} {
			corrupt := append([]byte(nil), base...)
			binary.LittleEndian.PutUint32(corrupt[24+sha256.Size:28+sha256.Size], badN)
			if err := os.WriteFile(filepath.Join(dir, recordOffsetsFile), corrupt, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readRecordOffsetsFor(dir, f, nil); !errors.Is(err, errNoRecordOffsets) {
				t.Fatalf("readRecordOffsetsFor(nkeys=%d) err = %v, want %v", badN, err, errNoRecordOffsets)
			}
			if _, ok := readRawRecordOffsets(dir, f); ok {
				t.Fatalf("readRawRecordOffsets(nkeys=%d) unexpectedly succeeded", badN)
			}
		}
	})
}

func FuzzRecordCodecAndStubPrefilter(f *testing.F) {
	f.Add("claude:s1", "/work/app/s1.jsonl", "user", "short ascii message", int64(1777600000000000000), true, []byte{0, 1, 2})
	f.Add("claude:s2", "/work/app/s2.jsonl", roleToolOutput, strings.Repeat("output кириллица line 0123456789\n", 14), int64(0), false, []byte{0xff, 0x80, 0x01, 0x02})
	f.Add("opencode:s3", "/work/app/s3", "assistant", strings.Repeat("deflate prefix boundary ", 24), int64(-1), true, []byte{1})
	f.Add("", "", roleEdit, strings.Repeat("utf8-boundary-тест-", 40), int64(2500000000000000000), false, []byte{})

	f.Fuzz(func(t *testing.T, key, srcPath, role, text string, nanoSeed int64, intern bool, rawCorrupt []byte) {
		var ts time.Time
		switch nanoSeed % 4 {
		case 0:
			ts = time.Time{}
		case 1:
			ts = time.Date(2026, 5, 1, 10, 0, 0, int(abs64(nanoSeed)%1e9), time.UTC)
		case 2:
			ts = time.Date(2999, 1, 2, 3, 4, 5, 0, time.UTC)
		default:
			ts = time.Unix(0, nanoSeed).UTC()
		}

		var tables *recordTables
		if intern {
			tables = tablesFromStrings([]string{key, srcPath, role, "user", roleToolOutput})
		} else {
			tables = newRecordTables()
		}

		rec := Record{
			Key:        key,
			SourcePath: srcPath,
			Role:       role,
			Text:       text,
			Time:       ts,
		}
		enc, err := encodeRecordChecked(rec, tables)
		if err != nil {
			t.Fatalf("encodeRecordChecked failed: %v", err)
		}

		dec, err := decodeRecord(enc, tables)
		if err != nil {
			t.Fatalf("decodeRecord failed on encoded payload (len=%d): %v", len(enc), err)
		}
		if dec.Key != rec.Key || dec.SourcePath != rec.SourcePath || dec.Role != rec.Role || dec.Text != rec.Text {
			t.Fatalf("record round-trip mismatch:\ngot:  %+v\nwant: %+v", dec, rec)
		}
		if utf8.ValidString(rec.Text) && !utf8.ValidString(dec.Text) {
			t.Fatalf("decodeRecord corrupted valid UTF-8")
		}
		if recordNanos(dec.Time) != recordNanos(ts) {
			t.Fatalf("timestamp mismatch: got=%v (%d) want=%v (%d)", dec.Time, recordNanos(dec.Time), ts, recordNanos(ts))
		}

		hdr, flag, rest, err := decodeRecordHeader(enc, tables)
		if err != nil {
			t.Fatalf("decodeRecordHeader failed: %v", err)
		}
		if hdr.Key != rec.Key || hdr.SourcePath != rec.SourcePath || hdr.Role != rec.Role {
			t.Fatalf("header mismatch: got=%+v want=%+v", hdr, rec)
		}
		if !hdr.Time.IsZero() {
			t.Fatalf("expected zero header time, got %v", hdr.Time)
		}
		_ = flag
		_ = rest

		stub, err := decodeRecordStubRole(enc, tables)
		if err != nil {
			t.Fatalf("decodeRecordStubRole failed: %v", err)
		}
		if stub.Key != rec.Key || recordNanos(stub.Time) != recordNanos(ts) {
			t.Fatalf("stub mismatch: got=%+v want=%+v", stub, rec)
		}
		if rec.Role == roleToolOutput {
			if stub.Role != RoleStubToolOutput && stub.Role != roleToolOutput {
				t.Fatalf("tool_output stub role = %q", stub.Role)
			}
		} else if stub.Role != rec.Role && stub.Role != RoleStubOthers {
			t.Fatalf("unexpected stub role = %q for role %q", stub.Role, rec.Role)
		}

		if rKey, rRole, ok := recordRoleIn(enc, tables); ok {
			if rKey != rec.Key || (rRole != rec.Role && rRole != RoleStubOthers && rRole != RoleStubToolOutput) {
				t.Fatalf("recordRoleIn mismatch: key=%q role=%q (want key=%q role=%q)", rKey, rRole, rec.Key, rec.Role)
			}
		}

		// Feed arbitrary / truncated / XOR-mutated bytes into all record decoders to ensure no panics.
		if len(enc) > 0 && len(rawCorrupt) > 0 {
			mut := append([]byte(nil), enc...)
			pos := int(rawCorrupt[0]) % len(mut)
			mut[pos] ^= rawCorrupt[len(rawCorrupt)-1] | 0x80
			_, _ = decodeRecord(mut, tables)
			_, _, _, _ = decodeRecordHeader(mut, tables)
			_, _ = decodeRecordStubRole(mut, tables)
			_, _, _ = recordRoleIn(mut, tables)
		}
		_, _ = decodeRecord(rawCorrupt, tables)
		_, _, _, _ = decodeRecordHeader(rawCorrupt, tables)
		_, _ = decodeRecordStubRole(rawCorrupt, tables)
		_, _, _ = recordRoleIn(rawCorrupt, tables)
		_, _ = consumeField(rawCorrupt)
	})
}

func FuzzBinaryCatalogOffsetsAndSidecars(f *testing.F) {
	f.Add("claude:s1", "/work/app", "alpha", "beta", uint64(42), int64(512), []byte{1, 2, 3, 4})
	f.Add("claude:s2", "/work/app", "0", "\x00", uint64(42), int64(512), []byte("0"))
	f.Add("opencode:ses_01", "/Users/alex/repo", " кириллица ", "rollback", uint64(0), int64(64), []byte{0xff, 0xff, 0xff, 0xff, 0x7f})
	f.Add("codex:thread-1", "/work/app", "uvarint", "bucket", uint64(1)<<40, int64(0), bytes.Repeat([]byte{0x80}, 12))

	f.Fuzz(func(t *testing.T, key, project, termA, termB string, hashSeed uint64, off int64, rawNoise []byte) {
		if key == "" {
			key = "claude:fallback"
		}
		if termA == "" || termA == cooccurDiscourseKey {
			termA = "toka"
		}
		if termB == "" || termB == cooccurDiscourseKey || termB == termA {
			termB = termA + "_b"
		}
		off = abs64(off) & ((1 << 50) - 1)
		dir := t.TempDir()
		fuzzSessionMetaSidecar(t, dir, key, project, termA, termB, hashSeed, rawNoise)
		fuzzBucketCodec(t, termA, termB, off, rawNoise)
		fuzzCooccurSidecar(t, dir, termA, termB, rawNoise)
		fuzzRecordOffsetsSidecar(t, dir, key, rawNoise)
	})
}

func fuzzSessionMetaSidecar(t *testing.T, dir, key, project, termA, termB string, hashSeed uint64, rawNoise []byte) {
	t.Helper()
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	meta := SessionMeta{
		ID:         "sid-1",
		Harness:    "claude",
		Project:    project,
		Path:       "/work/app/.claude/sid-1.jsonl",
		Title:      termA,
		Started:    now.Add(-time.Hour),
		Updated:    now,
		Ord:        uint32(hashSeed % 10000),
		Asked:      []uint64{hashSeed, hashSeed ^ 0x9e37},
		Touched:    []string{"internal/index/store_io.go", "internal/digest/handoff.go"},
		Settled:    "verified " + termB,
		RanCommand: hashSeed%2 == 0,
	}
	encodedMeta, err := appendSessionMeta(nil, meta)
	if err != nil {
		t.Fatalf("appendSessionMeta: %v", err)
	}
	decodedMeta, decErr := decodeSessionMetaExact(encodedMeta)
	if decErr != nil {
		t.Fatalf("decodeSessionMetaExact: %v", decErr)
	}
	if decodedMeta.ID != meta.ID || decodedMeta.Project != meta.Project || decodedMeta.Settled != meta.Settled {
		t.Fatalf("SessionMeta mismatch: got=%+v want=%+v", decodedMeta, meta)
	}
	_, _ = decodeSessionMetaExact(rawNoise)

	sbin, encErr := encodeSessionsBin(map[string]SessionMeta{key: meta})
	if encErr != nil {
		t.Fatalf("encodeSessionsBin: %v", encErr)
	}
	if wErr := os.WriteFile(filepath.Join(dir, "sessions.bin"), sbin, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if gotMap, rErr := readSessionsBin(dir); rErr != nil || len(gotMap) != 1 || gotMap[key].Title != meta.Title {
		t.Fatalf("readSessionsBin mismatch: err=%v map=%+v", rErr, gotMap)
	}
	if gotOne, found, kErr := readSessionsBinKey(dir, key); kErr != nil || !found || gotOne.ID != meta.ID {
		t.Fatalf("readSessionsBinKey mismatch: found=%v err=%v", found, kErr)
	}
	_ = os.WriteFile(filepath.Join(dir, "sessions.bin"), rawNoise, 0o600)
	_, _ = readSessionsBin(dir)
	_, _, _ = readSessionsBinKey(dir, key)
}

func fuzzBucketCodec(t *testing.T, termA, termB string, off int64, rawNoise []byte) {
	t.Helper()
	bucketMap := map[string][]posting{
		termA: {{Off: off, Sid: 0, Tool: true}, {Off: off + 64, Sid: 1, Tool: false}},
		termB: {{Off: off + 128, Sid: 0, Tool: false}},
	}
	decodedBucket, err := decodeBucketBytes(encodeBucketBytes(bucketMap))
	if err != nil {
		t.Fatalf("decodeBucketBytes: %v", err)
	}
	wantA := 2
	if isBlankedWord(termA) {
		wantA = 0
	}
	wantB := 1
	if isBlankedWord(termB) {
		wantB = 0
	}
	if len(decodedBucket[termA]) != wantA || len(decodedBucket[termB]) != wantB {
		t.Fatalf("bucket round-trip wrong (blankA=%v blankB=%v): %+v", isBlankedWord(termA), isBlankedWord(termB), decodedBucket)
	}
	_, _ = decodeBucketBytes(rawNoise)
}

func fuzzCooccurSidecar(t *testing.T, dir, termA, termB string, rawNoise []byte) {
	t.Helper()
	cooccurMap := map[string][]string{
		termA: {termB, "fallback"},
		termB: {termA},
	}
	if err := writeGob(cooccurPath(dir), cooccurMap); err != nil {
		t.Fatalf("writeGob: %v", err)
	}
	if err := writeCooccurBinAtomic(dir, cooccurMap); err != nil {
		t.Fatalf("writeCooccurBinAtomic: %v", err)
	}
	gobSize, stamp, err := cooccurGobStamp(dir)
	if err != nil {
		t.Fatalf("cooccurGobStamp: %v", err)
	}
	if lookedUp, ok := lookupCooccurFromBin(dir, gobSize, stamp, []string{termA}); !ok || len(lookedUp[termA]) == 0 {
		t.Fatalf("lookupCooccurFromBin failed: ok=%v got=%v", ok, lookedUp)
	}
	rawBin := encodeCooccurBin(gobSize, stamp, cooccurMap)
	if wErr := os.WriteFile(filepath.Join(dir, cooccurBinFile), append(rawBin[:min(len(rawBin), 64)], rawNoise...), 0o600); wErr == nil {
		_, _ = lookupCooccurFromBin(dir, gobSize, stamp, []string{termA})
	}
	_ = decodeCooccurBucket(rawNoise, []string{termA, termB}, map[string][]string{})
}

func fuzzRecordOffsetsSidecar(t *testing.T, dir, key string, rawNoise []byte) {
	t.Helper()
	logPath := filepath.Join(dir, "records.bin")
	logData := bytes.Repeat([]byte("rec-offsets-payload-0123456789\n"), 4)
	if err := os.WriteFile(logPath, logData, 0o600); err != nil {
		t.Fatal(err)
	}
	logFile, err := os.Open(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = logFile.Close() }()
	logSize := int64(len(logData))
	fp, fpErr := recordLogFingerprint(logFile, logSize)
	if fpErr != nil {
		t.Fatal(fpErr)
	}
	recBytes := encodeRecordOffsets(logSize, 2, fp, map[string][]int64{
		key: {0, 31},
	})
	if wErr := os.WriteFile(filepath.Join(dir, recordOffsetsFile), recBytes, 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	if ro, rErr := readRecordOffsetsFor(dir, logFile, map[string]bool{key: true}); rErr != nil || !slices.Equal(ro.byKey[key], []int64{0, 31}) {
		t.Fatalf("readRecordOffsetsFor mismatch: err=%v got=%v", rErr, ro.byKey[key])
	}
	if rawRO, ok := readRawRecordOffsets(dir, logFile); !ok {
		t.Fatal("readRawRecordOffsets returned false on valid data")
	} else if gotOffs, ok := rawRO.lookup(key); !ok || !slices.Equal(gotOffs, []int64{0, 31}) {
		t.Fatalf("rawRO.lookup mismatch: ok=%v got=%v", ok, gotOffs)
	}

	if len(recBytes) > recordOffsetsHeaderSize {
		fuzzedOffsets := make([]byte, 0, recordOffsetsHeaderSize+len(rawNoise))
		fuzzedOffsets = append(fuzzedOffsets, recBytes[:recordOffsetsHeaderSize]...)
		fuzzedOffsets = append(fuzzedOffsets, rawNoise...)
		_ = os.WriteFile(filepath.Join(dir, recordOffsetsFile), fuzzedOffsets, 0o600)
		_, _ = readRecordOffsetsFor(dir, logFile, map[string]bool{key: true})
		if rawRO, ok := readRawRecordOffsets(dir, logFile); ok {
			_, _ = rawRO.lookup(key)
		}
	}
}

func TestPropertyIncrementalStateMachineMatchesColdRebuild(t *testing.T) {
	vocab := []string{
		"cobaltlattice", "ambervalve", "quartzrelay",
		"copperbridge", "silverkestrel", "graniteanvil",
	}
	hasSQLite := false
	if _, err := exec.LookPath("sqlite3"); err == nil {
		hasSQLite = true
	}

	for seed := range 3 {
		t.Run(fmt.Sprintf("seed_%d", seed), func(t *testing.T) {
			root, incDir := allHarnessEnv(t)
			claudeDir := filepath.Join(root, "claude", "-work-app")
			opencodeDB := filepath.Join(root, "opencode.db")
			if err := os.MkdirAll(claudeDir, 0o755); err != nil {
				t.Fatal(err)
			}

			rng := rand.New(rand.NewPCG(uint64(seed)+1, 7)) //nolint:gosec // deterministic property test PRNG
			baseTime := time.Date(2026, 4, 15, 10, 0, 0, 0, time.UTC)

			type claudeSessionState struct {
				id    string
				lines []string
			}
			var claudeSessions []*claudeSessionState
			var opencodeCount int
			sqliteReady := false

			writeClaudeSessionLine := func(sid, role, text string, at time.Time) string {
				ts := at.Format(time.RFC3339)
				if role == "assistant" {
					return fmt.Sprintf(`{"type":"assistant","cwd":"/work/app","sessionId":%q,"timestamp":%q,"message":{"role":"assistant","content":%q}}`+"\n", sid, ts, text)
				}
				return fmt.Sprintf(`{"type":"user","cwd":"/work/app","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":%q}}`+"\n", sid, ts, text)
			}

			flushClaude := func(cs *claudeSessionState) {
				p := filepath.Join(claudeDir, cs.id+".jsonl")
				if err := os.WriteFile(p, []byte(strings.Join(cs.lines, "")), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			runSQLite := func(sql string) {
				cmd := exec.CommandContext(t.Context(), "sqlite3", opencodeDB, sql)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("sqlite3 %s: %v\n%s", sql, err, out)
				}
			}

			pickTopic := func(step int) string {
				return vocab[(seed*3+step+rng.IntN(len(vocab)))%len(vocab)]
			}

			stepTime := baseTime
			for step := range 9 {
				stepTime = stepTime.Add(2 * time.Minute)
				op := rng.IntN(4)
				if len(claudeSessions) == 0 {
					op = 0
				}
				switch op {
				case 0: // Create a new Claude JSONL session.
					sid := fmt.Sprintf("prop-c-%d-%d", seed, len(claudeSessions)+1)
					tA, tB := pickTopic(step), pickTopic(step+1)
					cs := &claudeSessionState{
						id: sid,
						lines: []string{
							writeClaudeSessionLine(sid, "user", "Investigate "+tA+" rollout in /work/app", stepTime),
							writeClaudeSessionLine(sid, "assistant", "Decided to keep "+tA+" and "+tB+" aligned.", stepTime.Add(15*time.Second)),
						},
					}
					claudeSessions = append(claudeSessions, cs)
					flushClaude(cs)

				case 1: // Append new turn to existing Claude session.
					cs := claudeSessions[rng.IntN(len(claudeSessions))]
					tok := pickTopic(step + 2)
					cs.lines = append(cs.lines,
						writeClaudeSessionLine(cs.id, "user", "Check "+tok+" fallback path", stepTime),
						writeClaudeSessionLine(cs.id, "assistant", "Verified "+tok+" fallback passes tests.", stepTime.Add(15*time.Second)),
					)
					flushClaude(cs)

				case 2: // Rewrite earlier line inside existing Claude session.
					cs := claudeSessions[rng.IntN(len(claudeSessions))]
					tok := pickTopic(step + 3)
					cs.lines[0] = writeClaudeSessionLine(cs.id, "user", "Rewritten objective: stabilize "+tok+" pipeline in /work/app", stepTime)
					flushClaude(cs)

				case 3: // Insert or append an OpenCode SQLite session when sqlite3 is available.
					if !hasSQLite {
						cs := claudeSessions[rng.IntN(len(claudeSessions))]
						tok := pickTopic(step)
						cs.lines = append(cs.lines, writeClaudeSessionLine(cs.id, "assistant", "Follow-up on "+tok, stepTime))
						flushClaude(cs)
						continue
					}
					if !sqliteReady {
						runSQLite(`create table session(id text primary key, directory text, time_created integer, time_updated integer);
create table message(id text primary key, session_id text, time_created integer, time_updated integer, data text);
create table part(id text primary key, message_id text, session_id text, time_created integer, time_updated integer, data text);`)
						sqliteReady = true
					}
					opencodeCount++
					sid := fmt.Sprintf("oc_prop_%d_%d", seed, opencodeCount)
					ms := stepTime.UnixMilli()
					tok := pickTopic(step + 4)
					mid1 := fmt.Sprintf("m_%s_1", sid)
					mid2 := fmt.Sprintf("m_%s_2", sid)
					runSQLite(fmt.Sprintf(
						`insert into session values(%q,'/work/app',%d,%d);
insert into message values(%q,%q,%d,%d,'{"role":"user"}');
insert into part values(%q,%q,%q,%d,%d,'{"type":"text","text":"Audit %s state machine"}');
insert into message values(%q,%q,%d,%d,'{"role":"assistant"}');
insert into part values(%q,%q,%q,%d,%d,'{"type":"text","text":"Decided %s invariant holds"}');`,
						sid, ms, ms+2000,
						mid1, sid, ms, ms,
						"p_"+mid1, mid1, sid, ms, ms, tok,
						mid2, sid, ms+1000, ms+2000,
						"p_"+mid2, mid2, sid, ms+1000, ms+2000, tok,
					))
				}

				if err := Ensure(incDir, "", false, io.Discard); err != nil {
					t.Fatalf("step %d incremental Ensure: %v", step, err)
				}

				// Compare incremental state against a clean cold rebuild at checkpoints.
				if step == 2 || step == 5 || step == 8 {
					coldDir := filepath.Join(root, "cold-"+strconv.Itoa(step))
					if err := Ensure(coldDir, "", true, io.Discard); err != nil {
						t.Fatalf("step %d cold Ensure: %v", step, err)
					}
					if Damaged(incDir) || Damaged(coldDir) {
						t.Fatalf("step %d damaged index: inc=%v cold=%v", step, Damaged(incDir), Damaged(coldDir))
					}

					incMeta := canonicalMetaKeys(t, incDir)
					coldMeta := canonicalMetaKeys(t, coldDir)
					if !slices.Equal(incMeta, coldMeta) {
						t.Fatalf("step %d AllMeta mismatch:\ninc:  %v\ncold: %v", step, incMeta, coldMeta)
					}

					for _, q := range vocab[:3] {
						incRes, err1 := SearchDetailed(incDir, query.Options{Query: q, Limit: 20})
						coldRes, err2 := SearchDetailed(coldDir, query.Options{Query: q, Limit: 20})
						if err1 != nil || err2 != nil {
							t.Fatalf("step %d SearchDetailed(%q): err1=%v err2=%v", step, q, err1, err2)
						}
						if !slices.Equal(sortedSessionIDs(incRes.Sessions), sortedSessionIDs(coldRes.Sessions)) {
							t.Fatalf("step %d SearchDetailed(%q) mismatch: inc=%v cold=%v",
								step, q, sortedSessionIDs(incRes.Sessions), sortedSessionIDs(coldRes.Sessions))
						}
					}

					incRel, _, _, _, err1 := ProjectRelevant(incDir, []string{"/work/app"}, vocab[:3], 10)
					coldRel, _, _, _, err2 := ProjectRelevant(coldDir, []string{"/work/app"}, vocab[:3], 10)
					if err1 != nil || err2 != nil {
						t.Fatalf("step %d ProjectRelevant err: inc=%v cold=%v", step, err1, err2)
					}
					if !slices.Equal(sortedSessionIDs(incRel), sortedSessionIDs(coldRel)) {
						t.Fatalf("step %d ProjectRelevant mismatch: inc=%v cold=%v",
							step, sortedSessionIDs(incRel), sortedSessionIDs(coldRel))
					}
				}
			}

			if seed == 0 {
				exportDir := filepath.Join(root, "sync-export")
				exported, err := Export(incDir, exportDir)
				if err != nil {
					t.Fatalf("Export: %v", err)
				}
				if exported == 0 {
					t.Fatal("Export wrote 0 records from populated index")
				}
				peerDir := filepath.Join(root, "peer-index")
				t.Setenv("DEJA_CLAUDE_ROOT", filepath.Join(root, "empty-claude"))
				t.Setenv("DEJA_OPENCODE_DB", filepath.Join(root, "empty-opencode.db"))
				imported, err := Import(peerDir, exportDir)
				if err != nil {
					t.Fatalf("Import: %v", err)
				}
				if imported != exported {
					t.Fatalf("Import count = %d, want exported %d", imported, exported)
				}
				if meta, err := AllMeta(peerDir); err != nil || len(meta) == 0 {
					t.Fatalf("peer AllMeta after Import: len=%d err=%v", len(meta), err)
				}
			}
		})
	}
}

func canonicalMetaKeys(t *testing.T, dir string) []string {
	t.Helper()
	ms, err := AllMeta(dir)
	if err != nil {
		t.Fatalf("AllMeta(%s): %v", dir, err)
	}
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = fmt.Sprintf("%s|%s|%s", m.Harness, m.ID, m.Project)
	}
	sort.Strings(out)
	return out
}

func sortedSessionIDs(ss []model.Session) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = s.Harness + "|" + s.ID
	}
	sort.Strings(out)
	return out
}

func abs64(v int64) int64 {
	if v == math.MinInt64 {
		return 0
	}
	if v < 0 {
		return -v
	}
	return v
}
