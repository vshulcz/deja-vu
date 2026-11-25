package index

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestSessionsBinCodecAndMetaEdges(t *testing.T) {
	if v, ok := u64ToInt(7); !ok || v != 7 {
		t.Fatalf("u64ToInt(7) = (%d, %v)", v, ok)
	}
	if _, ok := u64ToInt(math.MaxUint64); ok {
		t.Fatal("u64ToInt(MaxUint64) succeeded unexpectedly")
	}
	if v, ok := u64ToInt64(7); !ok || v != 7 {
		t.Fatalf("u64ToInt64(7) = (%d, %v)", v, ok)
	}
	if _, ok := u64ToInt64(math.MaxUint64); ok {
		t.Fatal("u64ToInt64(MaxUint64) succeeded unexpectedly")
	}
	if v, ok := u32ToInt(7); !ok || v != 7 {
		t.Fatalf("u32ToInt(7) = (%d, %v)", v, ok)
	}
	if v, ok := intToU32(7); !ok || v != 7 {
		t.Fatalf("intToU32(7) = (%d, %v)", v, ok)
	}
	if _, ok := intToU32(-1); ok {
		t.Fatal("intToU32(-1) succeeded unexpectedly")
	}
	if v, ok := intToU64(7); !ok || v != 7 {
		t.Fatalf("intToU64(7) = (%d, %v)", v, ok)
	}
	if _, ok := intToU64(-1); ok {
		t.Fatal("intToU64(-1) succeeded unexpectedly")
	}
	if v, ok := i64ToInt(7); !ok || v != 7 {
		t.Fatalf("i64ToInt(7) = (%d, %v)", v, ok)
	}

	tz := time.FixedZone("UTC+1", 3600)
	full := SessionMeta{
		ID: "id1", Harness: "claude", Project: "proj", Path: "/Users/alex/s.jsonl", Title: "title",
		Started:       time.Date(2026, 6, 15, 10, 0, 0, 123, tz),
		Updated:       time.Date(2026, 6, 15, 11, 0, 0, 456, tz),
		Ord:           400,
		Asked:         []uint64{400},
		Touched:       []string{"a"},
		Settled:       "s",
		RanCommand:    true,
		Shared:        true,
		AgentTitle:    true,
		SharedStarted: time.Date(2026, 6, 15, 10, 1, 0, 1, tz),
		SharedUpdated: time.Date(2026, 6, 15, 10, 2, 0, 2, tz),
		Copies:        []string{"c"},
		OrigID:        "o",
		RemoteID:      "r",
		Kind:          "k",
		Parent:        "p",
		Agent:         "ag",
		Model:         &model.Model{Provider: "p", ID: "m"},
		Tokens:        &model.Tokens{Input: 400, Output: 401, Reasoning: 402, CacheRead: 403},
		ToolCalls:     map[string]int{"b": 400},
		ToolErrors:    400,
		Opening:       400,
		From:          "peer",
		Lifecycle:     "active",
		LifecycleNote: "note",
		LifecycleAt:   "now",
		GaveUp:        true,
		Words:         400,
		NoText:        true,
		Counted:       400,
		LastMsg:       400,
		TouchHits:     []int{400},
		Hit:           []uint64{400},
		Rules:         []string{"ru"},
		Revokes:       []string{"rv"},
		Planted:       []string{"pl"},
	}
	raw, err := appendSessionMeta(nil, full)
	if err != nil {
		t.Fatalf("appendSessionMeta: %v", err)
	}
	for cut := range raw {
		if _, dErr := decodeSessionMetaExact(raw[:cut]); dErr == nil {
			t.Fatalf("decodeSessionMetaExact(raw[:%d]) succeeded unexpectedly", cut)
		}
	}
	extra := bytes.Clone(raw)
	extra = append(extra, 0x99)
	if _, eErr := decodeSessionMetaExact(extra); eErr == nil {
		t.Fatal("decodeSessionMetaExact accepted trailing bytes")
	}

	if _, bErr := (&binReader{b: []byte{2}}).boolVal(); bErr == nil {
		t.Fatal("boolVal accepted tag 2")
	}
	badUvar := bytes.Repeat([]byte{0x80}, 11)
	if _, uErr := (&binReader{b: badUvar}).uvar(); uErr == nil {
		t.Fatal("uvar accepted incomplete varint")
	}
	if _, iErr := (&binReader{b: badUvar}).ivar(); iErr == nil {
		t.Fatal("ivar accepted incomplete varint")
	}
	if _, iErr := (&binReader{b: badUvar}).intVal(); iErr == nil {
		t.Fatal("intVal accepted invalid varint")
	}
	overU32 := binary.AppendUvarint(nil, math.MaxUint32+1)
	if _, uErr := (&binReader{b: overU32}).u32(); uErr == nil {
		t.Fatal("u32 accepted overflow")
	}
	if _, uErr := (&binReader{b: badUvar}).u32(); uErr == nil {
		t.Fatal("u32 accepted invalid uvarint")
	}
	if _, sErr := (&binReader{b: binary.AppendUvarint(nil, 5)}).str(); sErr == nil {
		t.Fatal("str accepted truncated length")
	}
	if _, tErr := (&binReader{b: []byte{1}}).timeVal(); tErr == nil {
		t.Fatal("timeVal accepted missing length")
	}
	tooLongTime := make([]byte, 2+65)
	tooLongTime[0] = 1
	tooLongTime[1] = 65
	if _, tErr := (&binReader{b: tooLongTime}).timeVal(); tErr == nil {
		t.Fatal("timeVal accepted length > 64")
	}
	if _, tErr := (&binReader{b: []byte{1, 3, 0xff, 0xff, 0xff}}).timeVal(); tErr == nil {
		t.Fatal("timeVal accepted invalid binary time")
	}
	if _, _, sErr := (&binReader{b: []byte{1}}).sliceLen(); sErr == nil {
		t.Fatal("sliceLen accepted missing length")
	}
	if _, _, sErr := (&binReader{b: []byte{1, 10}}).sliceLen(); sErr == nil {
		t.Fatal("sliceLen accepted length exceeding buffer")
	}
	if _, mErr := (&binReader{b: []byte{1, 1, 'k', 0x80}}).strIntMap(); mErr == nil {
		t.Fatal("strIntMap accepted truncated value")
	}

	badTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("bad", 32768*60))
	if _, mErr := badTime.MarshalBinary(); mErr == nil {
		t.Fatal("expected out-of-range zone offset to fail MarshalBinary")
	}
	if _, aErr := appendTime(nil, badTime); aErr == nil {
		t.Fatal("appendTime accepted invalid timezone offset")
	}
	for _, field := range []string{"Started", "Updated", "SharedStarted", "SharedUpdated"} {
		badMeta := SessionMeta{ID: "x", Harness: "claude"}
		switch field {
		case "Started":
			badMeta.Started = badTime
		case "Updated":
			badMeta.Updated = badTime
		case "SharedStarted":
			badMeta.SharedStarted = badTime
		case "SharedUpdated":
			badMeta.SharedUpdated = badTime
		}
		if _, aErr := appendSessionMeta(nil, badMeta); aErr == nil {
			t.Fatalf("appendSessionMeta accepted invalid %s", field)
		}
	}
	if _, encErr := encodeSessionsBin(map[string]SessionMeta{
		"claude:bad": {ID: "bad", Harness: "claude", Started: badTime},
	}); encErr == nil {
		t.Fatal("encodeSessionsBin accepted invalid time")
	}
	if _, wErr := writeSessionsBinAtomicStat(filepath.Join(t.TempDir(), "sessions.bin"), map[string]SessionMeta{
		"claude:bad": {ID: "bad", Harness: "claude", Started: badTime},
	}); wErr == nil {
		t.Fatal("writeSessionsBinAtomicStat accepted invalid time")
	}
}

func TestSessionsBinBoundsAndStructuralCorruption(t *testing.T) {
	if _, _, hErr := parseSessionsBinHeader([]byte("short"), 100); !errors.Is(hErr, errCorruptIndex) {
		t.Fatalf("parseSessionsBinHeader(short) = %v, want errCorruptIndex", hErr)
	}
	for _, bErr := range []error{
		func() error { _, _, _, _, e := parseDirEntryBounds(make([]byte, 10), 64); return e }(),
		func() error { _, _, _, _, e := parseDirEntryBounds(make([]byte, 24), 16); return e }(),
		func() error {
			var ent [24]byte
			binary.LittleEndian.PutUint64(ent[0:8], math.MaxUint64)
			_, _, _, _, e := parseDirEntryBounds(ent[:], 64)
			return e
		}(),
		func() error {
			var ent [24]byte
			binary.LittleEndian.PutUint32(ent[16:20], sessionsBinMaxEntry+1)
			_, _, _, _, e := parseDirEntryBounds(ent[:], 64)
			return e
		}(),
		func() error {
			var ent [24]byte
			binary.LittleEndian.PutUint64(ent[0:8], 16)
			_, _, _, _, e := parseDirEntryBounds(ent[:], 64)
			return e
		}(),
		func() error {
			var ent [24]byte
			binary.LittleEndian.PutUint64(ent[0:8], 32)
			binary.LittleEndian.PutUint32(ent[16:20], 20)
			_, _, _, _, e := parseDirEntryBounds(ent[:], 48)
			return e
		}(),
		func() error {
			var ent [24]byte
			binary.LittleEndian.PutUint64(ent[0:8], 32)
			binary.LittleEndian.PutUint64(ent[8:16], 16)
			_, _, _, _, e := parseDirEntryBounds(ent[:], 48)
			return e
		}(),
	} {
		if !errors.Is(bErr, errCorruptIndex) {
			t.Fatalf("parseDirEntryBounds = %v, want errCorruptIndex", bErr)
		}
	}

	sessions := map[string]SessionMeta{
		"k:0": {ID: "0", Harness: "claude"},
		"k:1": {ID: "1", Harness: "claude"},
		"k:2": {ID: "2", Harness: "claude"},
		"k:3": {ID: "3", Harness: "claude"},
		"k:4": {ID: "4", Harness: "claude"},
	}
	valid, err := encodeSessionsBin(sessions)
	if err != nil {
		t.Fatal(err)
	}
	dirOff, dirOK := u64ToInt(binary.LittleEndian.Uint64(valid[16:24]))
	if !dirOK {
		t.Fatal("dirOff overflow")
	}
	resign := func(b []byte) []byte {
		out := bytes.Clone(b)
		body := len(out) - sessionsBinHashSize
		sum := sha256.Sum256(out[:body])
		copy(out[body:], sum[:])
		return out
	}
	writeResigned := func(mut []byte) string {
		dir := t.TempDir()
		if wErr := os.WriteFile(filepath.Join(dir, "sessions.bin"), resign(mut), 0o600); wErr != nil {
			t.Fatal(wErr)
		}
		return dir
	}

	goodDir := t.TempDir()
	if _, wErr := writeSessionsBinAtomicStat(filepath.Join(goodDir, "sessions.bin"), sessions); wErr != nil {
		t.Fatal(wErr)
	}
	for _, hitKey := range []string{"k:0", "k:2", "k:4"} {
		if _, ok, kErr := readSessionsBinKey(goodDir, hitKey); kErr != nil || !ok {
			t.Fatalf("readSessionsBinKey(%q) = (ok=%v, err=%v)", hitKey, ok, kErr)
		}
	}
	for _, missKey := range []string{"a:0", "k:15", "z:9"} {
		if _, ok, kErr := readSessionsBinKey(goodDir, missKey); kErr != nil || ok {
			t.Fatalf("readSessionsBinKey(%q) = (ok=%v, err=%v), want (false, nil)", missKey, ok, kErr)
		}
	}

	unsorted := bytes.Clone(valid)
	var first [sessionsBinDirEntrySize]byte
	copy(first[:], unsorted[dirOff:dirOff+sessionsBinDirEntrySize])
	copy(unsorted[dirOff:dirOff+sessionsBinDirEntrySize], unsorted[dirOff+sessionsBinDirEntrySize:dirOff+2*sessionsBinDirEntrySize])
	copy(unsorted[dirOff+sessionsBinDirEntrySize:dirOff+2*sessionsBinDirEntrySize], first[:])
	if _, rErr := readSessionsBin(writeResigned(unsorted)); !errors.Is(rErr, errCorruptIndex) {
		t.Fatalf("readSessionsBin(unsorted) = %v, want errCorruptIndex", rErr)
	}

	badBounds := bytes.Clone(valid)
	binary.LittleEndian.PutUint64(badBounds[dirOff+2*sessionsBinDirEntrySize:], 4)
	badBoundsDir := writeResigned(badBounds)
	if _, rErr := readSessionsBin(badBoundsDir); !errors.Is(rErr, errCorruptIndex) {
		t.Fatalf("readSessionsBin(badBounds) = %v, want errCorruptIndex", rErr)
	}
	if _, _, kErr := readSessionsBinKey(badBoundsDir, "k:2"); !errors.Is(kErr, errCorruptIndex) {
		t.Fatalf("readSessionsBinKey(badBounds) = %v, want errCorruptIndex", kErr)
	}

	badRow := bytes.Clone(valid)
	midEnt := dirOff + 2*sessionsBinDirEntrySize
	vOff, valOK := u64ToInt(binary.LittleEndian.Uint64(badRow[midEnt+8 : midEnt+16]))
	if !valOK {
		t.Fatal("vOff overflow")
	}
	binary.LittleEndian.PutUint32(badRow[midEnt+20:midEnt+24], 1)
	badRow[vOff] = 0xff
	badRowDir := writeResigned(badRow)
	if _, rErr := readSessionsBin(badRowDir); !errors.Is(rErr, errCorruptIndex) {
		t.Fatalf("readSessionsBin(badRow) = %v, want errCorruptIndex", rErr)
	}
	if _, _, kErr := readSessionsBinKey(badRowDir, "k:2"); !errors.Is(kErr, errCorruptIndex) {
		t.Fatalf("readSessionsBinKey(badRow) = %v, want errCorruptIndex", kErr)
	}

	truncDir := t.TempDir()
	core := manifestCore{Version: version, Format: onDiskFormat}
	if _, mErr := writeGobAtomicStat(filepath.Join(truncDir, "manifest.gob"), core); mErr != nil {
		t.Fatal(mErr)
	}
	if wErr := os.WriteFile(filepath.Join(truncDir, "sessions.bin"), valid[:16], 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	invalidateManifestCache(truncDir)
	if _, _, kErr := readSessionsBinKey(truncDir, "k:0"); !errors.Is(kErr, errCorruptIndex) {
		t.Fatalf("readSessionsBinKey(truncated) = %v, want errCorruptIndex", kErr)
	}
	if _, ok := MetaForKey(truncDir, "k:0"); ok {
		t.Fatal("MetaForKey returned ok=true on corrupt sessions.bin")
	}
}

func TestSessionsCatalogFallbacksAndWriteErrors(t *testing.T) {
	if _, err := readSessionsCatalog(t.TempDir()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("readSessionsCatalog(empty) = %v, want fs.ErrNotExist", err)
	}

	nilDir := t.TempDir()
	var nilMap map[string]SessionMeta
	if _, err := writeGobAtomicStat(filepath.Join(nilDir, "sessions.gob"), nilMap); err != nil {
		t.Fatal(err)
	}
	got, catErr := readSessionsCatalog(nilDir)
	if catErr != nil || got == nil || len(got) != 0 {
		t.Fatalf("readSessionsCatalog(nil gob map) = (%v, %v)", got, catErr)
	}

	corruptGobDir := t.TempDir()
	core := manifestCore{Version: version, Format: onDiskFormat}
	if _, mErr := writeGobAtomicStat(filepath.Join(corruptGobDir, "manifest.gob"), core); mErr != nil {
		t.Fatal(mErr)
	}
	if wErr := os.WriteFile(filepath.Join(corruptGobDir, "sessions.gob"), []byte("bad-gob"), 0o600); wErr != nil {
		t.Fatal(wErr)
	}
	invalidateManifestCache(corruptGobDir)
	if _, cErr := readSessionsCatalog(corruptGobDir); cErr == nil {
		t.Fatal("readSessionsCatalog(corrupt gob) succeeded unexpectedly")
	}
	if _, sErr := SessionCount(corruptGobDir); sErr == nil {
		t.Fatal("SessionCount(corrupt gob) succeeded unexpectedly")
	}
	if _, ok := MetaForKey(corruptGobDir, "claude:s1"); ok {
		t.Fatal("MetaForKey(corrupt gob) returned ok=true")
	}

	missingParent := filepath.Join(t.TempDir(), "missing", "sessions.bin")
	if _, wErr := writeSessionsBinAtomicStat(missingParent, map[string]SessionMeta{}); wErr == nil {
		t.Fatal("writeSessionsBinAtomicStat accepted missing parent directory")
	}
	targetDir := filepath.Join(t.TempDir(), "occupied")
	if mkErr := os.MkdirAll(filepath.Join(targetDir, "keep"), 0o750); mkErr != nil {
		t.Fatal(mkErr)
	}
	if _, wErr := writeSessionsBinAtomicStat(targetDir, map[string]SessionMeta{}); wErr == nil {
		t.Fatal("writeSessionsBinAtomicStat overwrote non-empty directory")
	}
	if _, stErr := os.Stat(targetDir + ".tmp"); !errors.Is(stErr, fs.ErrNotExist) {
		t.Fatalf("expected temp file removal after failed rename, stat=%v", stErr)
	}

	hermeticIndexEnv(t)
	def := DefaultDir()
	if mkErr := os.MkdirAll(def, 0o750); mkErr != nil {
		t.Fatal(mkErr)
	}
	if wErr := writeManifest(def, Manifest{
		Version:  version,
		Format:   onDiskFormat,
		Sessions: map[string]SessionMeta{"claude:s1": {ID: "s1", Harness: "claude"}},
		Files:    map[string]FileState{},
	}); wErr != nil {
		t.Fatal(wErr)
	}
	if !HasManifest("") {
		t.Fatal("HasManifest(\"\") = false")
	}
	if n, sErr := SessionCount(""); sErr != nil || n != 1 {
		t.Fatalf("SessionCount(\"\") = (%d, %v), want (1, nil)", n, sErr)
	}
	if meta, ok := MetaForKey("", "claude:s1"); !ok || meta.ID != "s1" {
		t.Fatalf("MetaForKey(\"\") = (%+v, %v)", meta, ok)
	}
}
