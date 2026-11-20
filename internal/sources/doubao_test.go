package sources

// Synthetic Doubao stores for the reader's tests. The encoders below write
// real LevelDB, snappy, IndexedDB key and V8 structured-serialization bytes —
// the same shapes Chromium writes — so the tests exercise the decoder stack
// end to end rather than an intermediate shape. The committed conformance
// fixture (fixtures/registry/doubao/) is produced by TestDoubaoWriteFixture
// with DEJA_DOUBAO_FIXTURE_DIR pointing at the repository root; it is not
// written during ordinary runs.

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// ── byte helpers ────────────────────────────────────────────────────────────

func uvarint(n uint64) []byte {
	var buf [10]byte
	k := binary.PutUvarint(buf[:], n)
	return append([]byte(nil), buf[:k]...)
}

func leBytes64(n uint64) []byte {
	var b [8]byte
	binary.LittleEndian.PutUint64(b[:], n)
	return b[:]
}

func leBytes32(n uint32) []byte {
	var b [4]byte
	binary.LittleEndian.PutUint32(b[:], n)
	return b[:]
}

func leBytesFloat(f float64) []byte {
	return leBytes64(math.Float64bits(f))
}

// zigzag folds an int32 the way V8's serializer does. The test's ints are
// small (block types and counts), so the conversion cannot lose anything.
func zigzag(n int64) uint64 {
	return uint64(n<<1) ^ uint64(n>>63) //nolint: gosec // test values are small ints
}

// ── snappy: literal-only compression, valid for the decoder ─────────────────

func snappyLiteral(input []byte) []byte {
	out := make([]byte, 0, len(input)+len(input)/65536*3+5)
	out = append(out, uvarint(uint64(len(input)))...)
	for len(input) > 0 {
		n := min(len(input), 65536)
		// Literal tag with a 2-byte length: tag = 61<<2, stored value = n-1.
		out = append(out, byte(61<<2), byte((n-1)&0xff), byte((n-1)>>8))
		out = append(out, input[:n]...)
		input = input[n:]
	}
	return out
}

// ── LevelDB: WriteBatch + write-ahead log ───────────────────────────────────

type wbOp struct {
	del   bool
	key   []byte
	value []byte
}

func encodeWriteBatch(seq uint64, ops []wbOp) []byte {
	var buf []byte
	buf = append(buf, leBytes64(seq)...)
	buf = append(buf, leBytes32(uint32(len(ops)&0x7fffffff))...)
	for _, op := range ops {
		if op.del {
			buf = append(buf, 0)
		} else {
			buf = append(buf, 1)
		}
		buf = append(buf, uvarint(uint64(len(op.key)))...)
		buf = append(buf, op.key...)
		if !op.del {
			buf = append(buf, uvarint(uint64(len(op.value)))...)
			buf = append(buf, op.value...)
		}
	}
	return buf
}

// frameWAL frames batch payloads as write-ahead log records, fragmenting
// payloads that do not fit one 32 KiB block and skipping block tails the way
// LevelDB does.
func frameWAL(batches [][]byte) []byte {
	const headerSize = 7
	var out []byte
	room := func() int {
		if inBlock := len(out) % leveldbBlockSize; inBlock >= leveldbBlockSize-headerSize {
			out = append(out, make([]byte, leveldbBlockSize-inBlock)...)
		}
		return leveldbBlockSize - len(out)%leveldbBlockSize - headerSize
	}
	emit := func(t byte, chunk []byte) {
		framed := append([]byte{t}, chunk...)
		sum := crc32c(framed)
		masked := (sum >> 15) | (sum << 17)
		masked += 0xa282ead8
		out = append(out, leBytes32(masked)...)
		out = append(out, leBytes32(uint32(len(chunk) & 0xffff))[:2]...)
		out = append(out, t)
		out = append(out, chunk...)
	}
	for _, payload := range batches {
		first := true
		for len(payload) > 0 {
			r := room()
			chunk := payload
			if len(chunk) > r {
				chunk = chunk[:r]
			}
			var t byte
			switch {
			case first && len(chunk) == len(payload):
				t = 1 // FULL
			case first:
				t = 2 // FIRST
			case len(chunk) == len(payload):
				t = 4 // LAST
			default:
				t = 3 // MIDDLE
			}
			emit(t, chunk)
			payload = payload[len(chunk):]
			first = false
		}
	}
	return out
}

// ── LevelDB: SSTable ────────────────────────────────────────────────────────

type sstEntry struct {
	userKey []byte
	seq     uint64
	value   []byte
}

func sstInternalKey(e sstEntry) []byte {
	return append(append([]byte(nil), e.userKey...), leBytes64(e.seq<<8|1)...)
}

// sstBlockBytes encodes entries with one restart point at offset 0, which is
// where the reader stops reading entries.
func sstBlockBytes(pairs []leveldbEntry) []byte {
	var b []byte
	for _, e := range pairs {
		b = append(b, byte(0)) // shared = 0
		b = append(b, uvarint(uint64(len(e.key)))...)
		b = append(b, uvarint(uint64(len(e.value)))...)
		b = append(b, e.key...)
		b = append(b, e.value...)
	}
	b = append(b, leBytes32(0)...)
	b = append(b, leBytes32(1)...)
	return b
}

// encodeSSTable writes one data block (optionally snappy), an index block and
// the footer.
func encodeSSTable(entries []sstEntry, compress bool) []byte {
	dataPairs := make([]leveldbEntry, 0, len(entries))
	for _, e := range entries {
		dataPairs = append(dataPairs, leveldbEntry{key: sstInternalKey(e), value: e.value})
	}
	dataBlock := sstBlockBytes(dataPairs)
	dataCompressed := dataBlock
	dataFlag := byte(0)
	if compress {
		dataCompressed = snappyLiteral(dataBlock)
		dataFlag = 1
	}

	// Layout: data block, type byte, index block, type byte, footer. The data
	// block starts at 0; the index block right after its compression byte.
	// The BlockHandle's size is the on-disk span (compressed when it is).
	handle := append(uvarint(0), uvarint(uint64(len(dataCompressed)))...)
	indexOffset := len(dataCompressed) + 1
	indexBlock := sstBlockBytes([]leveldbEntry{{key: sstInternalKey(entries[len(entries)-1]), value: handle}})

	var footer []byte
	footer = append(footer, uvarint(0)...) // metaindex offset (unused)
	footer = append(footer, uvarint(0)...) // metaindex size (unused)
	footer = append(footer, uvarint(uint64(indexOffset))...)
	footer = append(footer, uvarint(uint64(len(indexBlock)))...)
	for len(footer) < 40 {
		footer = append(footer, 0)
	}
	footer = append(footer, leBytes64(0xdb4775248b80fb57)...)

	out := make([]byte, 0, len(dataCompressed)+1+len(indexBlock)+1+len(footer))
	out = append(out, dataCompressed...)
	out = append(out, dataFlag)
	out = append(out, indexBlock...)
	out = append(out, byte(0))
	out = append(out, footer...)
	return out
}

// ── Chromium IndexedDB keys and values ──────────────────────────────────────

// idbTestStringKey encodes a string record key with 1-byte db/os/index ids
// and index id 1 (object-store data).
func idbTestStringKey(key string) []byte {
	out := []byte{0x00, 0x00, 0x00, 0x01}
	units := utf16.Encode([]rune(key))
	be := make([]byte, 0, 2*len(units))
	for _, u := range units {
		be = append(be, byte(u>>8&0xff), byte(u&0xff))
	}
	out = append(out, 1)
	out = append(out, uvarint(uint64(len(units)))...)
	return append(out, be...)
}

// encodeSSV builds a SerializedScriptValue: version tag, Blink trailer, then
// the V8 stream.
func encodeSSV(v8 []byte) []byte {
	out := []byte{0xff, 0x01, 0xfe}
	out = append(out, make([]byte, 8+4)...)
	return append(out, v8...)
}

// wrapIDBValue applies the Chromium IDBValue wrapper: snappy-compressed when
// asked, bare otherwise.
func wrapIDBValue(ssv []byte, snappy bool) []byte {
	if snappy {
		return append([]byte{0xff, 0x11, 0x02}, snappyLiteral(ssv)...)
	}
	return ssv
}

// ── small V8 value builders ─────────────────────────────────────────────────

type vkv struct {
	k string
	v []byte
}

// vobj builds a plain object; keys come before values, the order the reader
// expects.
func vobj(pairs ...vkv) []byte {
	var out []byte
	out = append(out, 0x6f)
	for _, p := range pairs {
		out = append(out, encodeV8OneByteString(p.k)...)
		out = append(out, p.v...)
	}
	out = append(out, 0x7b)
	out = append(out, uvarint(uint64(len(pairs)))...)
	return out
}

// vobjOrdered builds an object with an explicit key order — the order the
// receivedMessages replies are read in.
func vobjOrdered(keys []string, vals [][]byte) []byte {
	var out []byte
	out = append(out, 0x6f)
	for i, k := range keys {
		out = append(out, encodeV8OneByteString(k)...)
		out = append(out, vals[i]...)
	}
	out = append(out, 0x7b)
	out = append(out, uvarint(uint64(len(keys)))...)
	return out
}

func encodeV8OneByteString(s string) []byte {
	out := append([]byte{0x22}, uvarint(uint64(len(s)))...)
	return append(out, s...)
}

func encodeV8TwoByteString(s string) []byte {
	units := utf16.Encode([]rune(s))
	raw := make([]byte, 0, 2*len(units))
	for _, u := range units {
		var b [2]byte
		binary.LittleEndian.PutUint16(b[:], u)
		raw = append(raw, b[:]...)
	}
	out := append([]byte{0x63}, uvarint(uint64(len(raw)))...)
	return append(out, raw...)
}

// encodeV8String picks the string form by content, like V8 does.
func encodeV8String(s string) []byte {
	for _, r := range s {
		if r > 0x7f {
			return encodeV8TwoByteString(s)
		}
	}
	return encodeV8OneByteString(s)
}

func encodeV8Double(f float64) []byte {
	out := make([]byte, 0, 9)
	out = append(out, 0x4e)
	return append(out, leBytesFloat(f)...)
}

func encodeV8Int32(n int64) []byte {
	return append([]byte{0x49}, uvarint(zigzag(n))...)
}

func encodeV8Dense(items [][]byte) []byte {
	out := append([]byte{0x41}, uvarint(uint64(len(items)))...)
	for _, it := range items {
		out = append(out, it...)
	}
	out = append(out, 0x24)
	out = append(out, uvarint(uint64(len(items)))...)
	out = append(out, uvarint(0)...)
	return out
}

// ── Doubao record builders ──────────────────────────────────────────────────

func doubaoTextBlockValue(text string) []byte {
	return vobj(
		vk("block_type", encodeV8Int32(10000)),
		vk("content", vobj(vk("text_block", vobj(vk("text", encodeV8String(text)))))),
	)
}

// doubaoTaskValue builds one mainTaskDataMap task. extraConversationID is the
// id carried on the received message's extra; when conversationID is empty
// the reader must fall back to it.
func doubaoTaskValue(conversationID, prompt, reply, extraConversationID string, promptMS, replyTime float64) []byte {
	return vobj(
		vk("requestQuery", vobj(vk("syncTask", vobj(
			vk("client_meta", vobj(vk("conversation_id", encodeV8String(conversationID)))),
			vk("messages", encodeV8Dense([][]byte{
				vobj(vk("content_block", encodeV8Dense([][]byte{doubaoTextBlockValue(prompt)}))),
			})),
			vk("option", vobj(vk("create_time_ms", encodeV8Double(promptMS)))),
		)))),
		vk("receivedMessages", vobjOrdered(
			[]string{"msg-1"},
			[][]byte{vobj(
				vk("extra", vobj(
					vk("conversation_id", encodeV8String(extraConversationID)),
					vk("create_time", encodeV8Double(replyTime)),
					vk("content_blocks_v2", encodeV8Dense([][]byte{doubaoTextBlockValue(reply)})),
				)),
			)},
		)),
	)
}

func doubaoProtocolValue(tasks ...vkv) []byte {
	return vobj(
		vk("state", vobj(vk("mainTaskDataMap", vobj(tasks...)))),
		vk("version", encodeV8Int32(2)),
	)
}

// doubaoListValue builds one conversation-list record holding a single
// conversation with the given fields — the shape every test needs. The
// reader merges several such records per conversation id.
func doubaoListValue(schema string, fields ...vkv) []byte {
	return vobj(
		vk("uid", encodeV8String("u1")),
		vk("schema", encodeV8String(schema)),
		vk("data", vobj(vk("conversations", encodeV8Dense([][]byte{vobj(fields...)})))),
	)
}

func vk(key string, val []byte) vkv { return vkv{k: key, v: val} }

// ── store builders ──────────────────────────────────────────────────────────

// writeDoubaoStore assembles a LevelDB directory: ldb entries carry the
// (userKey, seq, value) triples, logBatches the WriteBatch payloads.
func writeDoubaoStore(t *testing.T, dir string, ldbEntries []sstEntry, logBatches [][]byte, compress bool) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if len(ldbEntries) > 0 {
		if err := os.WriteFile(filepath.Join(dir, "000001.ldb"), encodeSSTable(ldbEntries, compress), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if len(logBatches) > 0 {
		if err := os.WriteFile(filepath.Join(dir, "000002.log"), frameWAL(logBatches), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "CURRENT"), []byte("MANIFEST-000001\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

const (
	doubaoConvID  = "conv-1"
	doubaoConv2ID = "conv-2"
	doubaoTitle   = "显卡降价原因"
	doubaoPrompt  = "帮我看看显卡降价的行情"
	doubaoReply   = "降了，4090 已经来到 7999"
)

func doubaoBaseStore(t *testing.T, compress bool) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "profile", "IndexedDB", "chrome_doubao-chat_0.indexeddb.leveldb")
	listVal := doubaoListValue("conversation-list-data-overall-v2",
		vk("conversation_id", encodeV8String(doubaoConvID)),
		vk("name", encodeV8String(doubaoTitle)),
		vk("create_time", encodeV8Double(1792575407000)),
		vk("update_time", encodeV8Double(1792575500000)),
	)
	taskVal := doubaoTaskValue(doubaoConvID, doubaoPrompt, doubaoReply, doubaoConvID, 1792575407000, 1792575407500)
	protoVal := doubaoProtocolValue(vk("task-1", taskVal))
	ldbEntries := []sstEntry{
		{userKey: idbTestStringKey("conversation-list-record"), seq: 5, value: encodeSSV(listVal)},
		{userKey: idbTestStringKey(doubaoProtocolKey), seq: 10, value: wrapIDBValue(encodeSSV(protoVal), compress)},
	}
	writeDoubaoStore(t, dir, ldbEntries, nil, compress)
	return root
}

// ── tests ───────────────────────────────────────────────────────────────────

func TestDoubaoParse(t *testing.T) {
	root := doubaoBaseStore(t, false)
	t.Setenv("DEJA_DOUBAO_ROOT", root)

	files := DoubaoFiles()
	if len(files) != 1 || filepath.Base(files[0]) != "000001.ldb" {
		t.Fatalf("DoubaoFiles() = %v, want the one ldb file", files)
	}
	if !IsDoubaoLevelDbFile(files[0]) {
		t.Fatal("IsDoubaoLevelDbFile(file) = false")
	}
	if IsDoubaoLevelDbFile(filepath.Join(root, "profile", "IndexedDB", "chrome_doubao-background_0.indexeddb.leveldb", "000001.ldb")) {
		t.Fatal("IsDoubaoLevelDbFile accepted a non-chat database")
	}
	if IsDoubaoLevelDbFile(filepath.Join(root, "profile", "nothing.ldb")) {
		t.Fatal("IsDoubaoLevelDbFile accepted a file outside a database directory")
	}

	ss, err := ParseDoubaoFile(files[0])
	if err != nil {
		t.Fatalf("ParseDoubaoFile: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("got %d sessions, want 1: %+v", len(ss), ss)
	}
	s := ss[0]
	if s.Harness != "doubao" || s.ID != doubaoConvID {
		t.Fatalf("harness=%q id=%q", s.Harness, s.ID)
	}
	if s.Title != doubaoTitle {
		t.Fatalf("title = %q", s.Title)
	}
	if len(s.Messages) != 2 {
		t.Fatalf("got %d messages, want 2: %+v", len(s.Messages), s.Messages)
	}
	if s.Messages[0].Role != "user" || s.Messages[0].Text != doubaoPrompt {
		t.Fatalf("message 0 = %+v", s.Messages[0])
	}
	if s.Messages[1].Role != "assistant" || s.Messages[1].Text != doubaoReply {
		t.Fatalf("message 1 = %+v", s.Messages[1])
	}
	if want := time.UnixMilli(1792575407000); !s.Started.Equal(want) {
		t.Fatalf("started = %v, want %v", s.Started, want)
	}
	if want := time.UnixMilli(1792575500000); !s.Updated.Equal(want) {
		t.Fatalf("updated = %v, want %v", s.Updated, want)
	}
}

func TestDoubaoParseSnappy(t *testing.T) {
	root := doubaoBaseStore(t, true)
	t.Setenv("DEJA_DOUBAO_ROOT", root)
	ss, err := ParseDoubaoDir(DoubaoIndexedDbDirs()[0])
	if err != nil || len(ss) != 1 {
		t.Fatalf("snappy store parse = %d sessions, err=%v", len(ss), err)
	}
	if ss[0].Messages[1].Text != doubaoReply {
		t.Fatalf("reply through snappy = %q", ss[0].Messages[1].Text)
	}
}

// 2.35+ prefixes the protocol key with the account (im-sdk:user:<uid>:) and
// 2.37+ writes client_meta.conversation_id as an empty string, carrying the
// id on the received messages' extra instead. An unrelated key sharing the
// suffix must not contribute.
func TestDoubaoDriftedKeys(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "IndexedDB", "chrome_doubao-chat_0.indexeddb.leveldb")
	prefixed := "im-sdk:user:2091696589113404:" + doubaoProtocolKey
	taskVal := doubaoTaskValue("", "二号会话的问题", "二号会话的回答", doubaoConv2ID, 1792576000000, 1792576000500)
	decoyVal := doubaoProtocolValue()
	ldbEntries := []sstEntry{
		{userKey: idbTestStringKey(prefixed), seq: 7, value: encodeSSV(doubaoProtocolValue(vk("task-1", taskVal)))},
		{userKey: idbTestStringKey("some-other:" + doubaoProtocolKey), seq: 8, value: encodeSSV(decoyVal)},
	}
	writeDoubaoStore(t, dir, ldbEntries, nil, false)
	t.Setenv("DEJA_DOUBAO_ROOT", root)

	ss, err := ParseDoubaoDir(dir)
	if err != nil {
		t.Fatalf("ParseDoubaoDir: %v", err)
	}
	if len(ss) != 1 || ss[0].ID != doubaoConv2ID {
		t.Fatalf("sessions = %+v, want one conv-2 from the prefixed key", ss)
	}
	if ss[0].Messages[0].Text != "二号会话的问题" {
		t.Fatalf("prompt = %q", ss[0].Messages[0].Text)
	}
	// No conversation-list record: the title falls back to the first prompt.
	if ss[0].Title != "二号会话的问题" {
		t.Fatalf("fallback title = %q", ss[0].Title)
	}
}

// Streaming generations rewrite the protocol record; only the longest
// snapshot holds the full reply.
func TestDoubaoStreamingHistory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "IndexedDB", "chrome_doubao-chat_0.indexeddb.leveldb")
	mk := func(reply string) []byte {
		task := doubaoTaskValue(doubaoConvID, doubaoPrompt, reply, doubaoConvID, 1792575407000, 1792575407500)
		return encodeSSV(doubaoProtocolValue(vk("task-1", task)))
	}
	ldbEntries := []sstEntry{
		{userKey: idbTestStringKey(doubaoProtocolKey), seq: 10, value: mk("答")},
	}
	logBatches := [][]byte{
		encodeWriteBatch(20, []wbOp{{key: idbTestStringKey(doubaoProtocolKey), value: mk("答，补充")}}),
		encodeWriteBatch(30, []wbOp{{key: idbTestStringKey(doubaoProtocolKey), value: mk("答，补充完毕")}}),
	}
	writeDoubaoStore(t, dir, ldbEntries, logBatches, false)
	t.Setenv("DEJA_DOUBAO_ROOT", root)

	ss, err := ParseDoubaoDir(dir)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err=%v", len(ss), err)
	}
	if got := ss[0].Messages[1].Text; got != "答，补充完毕" {
		t.Fatalf("reply = %q, want the longest snapshot", got)
	}
}

// A reply that starts over (regenerate) replaces the old one and is counted.
func TestDoubaoRegenerateCountsRestart(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "IndexedDB", "chrome_doubao-chat_0.indexeddb.leveldb")
	mk := func(reply string) []byte {
		task := doubaoTaskValue(doubaoConvID, doubaoPrompt, reply, doubaoConvID, 1792575407000, 1792575407500)
		return encodeSSV(doubaoProtocolValue(vk("task-1", task)))
	}
	logBatches := [][]byte{
		encodeWriteBatch(10, []wbOp{{key: idbTestStringKey(doubaoProtocolKey), value: mk("第一次的回答")}}),
		encodeWriteBatch(20, []wbOp{{key: idbTestStringKey(doubaoProtocolKey), value: mk("重答后的完整回答")}}),
	}
	writeDoubaoStore(t, dir, nil, logBatches, false)
	t.Setenv("DEJA_DOUBAO_ROOT", root)

	ss, err := ParseDoubaoDir(dir)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err=%v", len(ss), err)
	}
	if got := ss[0].Messages[1].Text; got != "重答后的完整回答" {
		t.Fatalf("reply = %q, want the regenerate", got)
	}
}

// The same conversation appears in several list-schema variants with
// different fields; merged per conversation id.
func TestDoubaoListSchemaVariantsMerge(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "IndexedDB", "chrome_doubao-chat_0.indexeddb.leveldb")
	v1 := doubaoListValue("conversation-list-data-non-project-v2",
		vk("conversation_id", encodeV8String(doubaoConvID)),
		vk("name", encodeV8String(doubaoTitle)),
	)
	v2 := doubaoListValue("conversation-list-data-device-group-v1",
		vk("conversation_id", encodeV8String(doubaoConvID)),
		vk("create_time", encodeV8Double(1792575000000)),
		vk("update_time", encodeV8Double(1792575600000)),
	)
	taskVal := doubaoTaskValue(doubaoConvID, doubaoPrompt, doubaoReply, doubaoConvID, 1792575407000, 1792575407500)
	ldbEntries := []sstEntry{
		{userKey: idbTestStringKey("list-1"), seq: 3, value: encodeSSV(v1)},
		{userKey: idbTestStringKey("list-2"), seq: 4, value: encodeSSV(v2)},
		{userKey: idbTestStringKey(doubaoProtocolKey), seq: 10, value: encodeSSV(doubaoProtocolValue(vk("task-1", taskVal)))},
	}
	writeDoubaoStore(t, dir, ldbEntries, nil, false)
	t.Setenv("DEJA_DOUBAO_ROOT", root)

	ss, err := ParseDoubaoDir(dir)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err=%v", len(ss), err)
	}
	if ss[0].Title != doubaoTitle {
		t.Fatalf("title = %q, want the variant that has it", ss[0].Title)
	}
	if want := time.UnixMilli(1792575000000); !ss[0].Started.Equal(want) {
		t.Fatalf("started = %v, want the earliest create_time", ss[0].Started)
	}
	if want := time.UnixMilli(1792575600000); !ss[0].Updated.Equal(want) {
		t.Fatalf("updated = %v, want the latest update_time", ss[0].Updated)
	}
}

// Non-text blocks are counted and dropped, not turned into placeholders; a
// task without any readable text yields no turn.
func TestDoubaoNonTextBlocksSkipped(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "IndexedDB", "chrome_doubao-chat_0.indexeddb.leveldb")
	promptBlocks := encodeV8Dense([][]byte{
		vobj(
			vk("block_type", encodeV8Int32(10001)),
			vk("content", vobj(vk("image_block", vobj(vk("url", encodeV8String("https://example.com/a.png")))))),
		),
		doubaoTextBlockValue(doubaoPrompt),
	})
	replyBlocks := encodeV8Dense([][]byte{
		vobj(vk("block_type", encodeV8Int32(10002)), vk("content", vobj())),
		doubaoTextBlockValue(doubaoReply),
	})
	task := vobj(
		vk("requestQuery", vobj(vk("syncTask", vobj(
			vk("client_meta", vobj(vk("conversation_id", encodeV8String(doubaoConvID)))),
			vk("messages", encodeV8Dense([][]byte{vobj(vk("content_block", promptBlocks))})),
			vk("option", vobj(vk("create_time_ms", encodeV8Double(1792575407000)))),
		)))),
		vk("receivedMessages", vobjOrdered([]string{"msg-1"}, [][]byte{vobj(
			vk("extra", vobj(
				vk("conversation_id", encodeV8String(doubaoConvID)),
				vk("create_time", encodeV8Double(1792575407500)),
				vk("content_blocks_v2", replyBlocks),
			)),
		)})),
	)
	textless := doubaoTaskValue(doubaoConvID, "", "", doubaoConvID, 1792575407000, 1792575407500)
	protoVal := doubaoProtocolValue(vk("task-1", task), vk("task-2", textless))
	ldbEntries := []sstEntry{
		{userKey: idbTestStringKey(doubaoProtocolKey), seq: 10, value: encodeSSV(protoVal)},
	}
	writeDoubaoStore(t, dir, ldbEntries, nil, false)
	t.Setenv("DEJA_DOUBAO_ROOT", root)

	ss, err := ParseDoubaoDir(dir)
	if err != nil {
		t.Fatalf("ParseDoubaoDir: %v", err)
	}
	if len(ss) != 1 {
		t.Fatalf("sessions = %d, want 1 (the text-less task yields none)", len(ss))
	}
	if len(ss[0].Messages) != 2 || ss[0].Messages[1].Text != doubaoReply {
		t.Fatalf("messages = %+v", ss[0].Messages)
	}
}

// A deletion in the log is a version like any other: the snapshot history
// still carries the content written before it.
func TestDoubaoTombstoneKeepsHistory(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "IndexedDB", "chrome_doubao-chat_0.indexeddb.leveldb")
	taskVal := doubaoTaskValue(doubaoConvID, doubaoPrompt, doubaoReply, doubaoConvID, 1792575407000, 1792575407500)
	ldbEntries := []sstEntry{
		{userKey: idbTestStringKey(doubaoProtocolKey), seq: 10, value: encodeSSV(doubaoProtocolValue(vk("task-1", taskVal)))},
	}
	logBatches := [][]byte{
		encodeWriteBatch(20, []wbOp{{del: true, key: idbTestStringKey(doubaoProtocolKey)}}),
	}
	writeDoubaoStore(t, dir, ldbEntries, logBatches, false)
	t.Setenv("DEJA_DOUBAO_ROOT", root)

	ss, err := ParseDoubaoDir(dir)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err=%v", len(ss), err)
	}
	if ss[0].Messages[1].Text != doubaoReply {
		t.Fatalf("reply after tombstone = %q", ss[0].Messages[1].Text)
	}
}

// A batch too large for one 32 KiB log block arrives as FIRST/MIDDLE/LAST
// fragments and must reassemble.
func TestDoubaoLogFragmentReassembly(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "IndexedDB", "chrome_doubao-chat_0.indexeddb.leveldb")
	var sb strings.Builder
	for range 3000 {
		sb.WriteString("流式输出片段。")
	}
	longReply := sb.String()
	taskVal := doubaoTaskValue(doubaoConvID, doubaoPrompt, longReply, doubaoConvID, 1792575407000, 1792575407500)
	payload := encodeWriteBatch(10, []wbOp{{key: idbTestStringKey(doubaoProtocolKey), value: encodeSSV(doubaoProtocolValue(vk("task-1", taskVal)))}})
	if len(payload) <= leveldbBlockSize {
		t.Fatalf("test payload %d fits one block; make it larger", len(payload))
	}
	writeDoubaoStore(t, dir, nil, [][]byte{payload}, false)
	t.Setenv("DEJA_DOUBAO_ROOT", root)

	ss, err := ParseDoubaoDir(dir)
	if err != nil || len(ss) != 1 {
		t.Fatalf("sessions = %d, err=%v", len(ss), err)
	}
	if got := ss[0].Messages[1].Text; got != longReply {
		t.Fatalf("reply = %d bytes, want the full %d", len(got), len(longReply))
	}
}

func TestDoubaoLoad(t *testing.T) {
	root := doubaoBaseStore(t, false)
	t.Setenv("DEJA_DOUBAO_ROOT", root)
	ss := LoadDoubao()
	if len(ss) != 1 || ss[0].Harness != "doubao" {
		t.Fatalf("LoadDoubao() = %+v, want one doubao session", ss)
	}
}

// TestDoubaoWriteFixture writes the committed conformance fixture when
// DEJA_WRITE_DOUBAO_FIXTURE_DIR names the repository root. The DEJA_WRITE_
// prefix is what testenv.Scrub keeps, so the variable survives the package's
// environment sweep; ordinary runs skip this.
func TestDoubaoWriteFixture(t *testing.T) {
	out := os.Getenv("DEJA_WRITE_DOUBAO_FIXTURE_DIR")
	if out == "" {
		t.Skip("DEJA_WRITE_DOUBAO_FIXTURE_DIR not set")
	}
	dir := filepath.Join(out, "fixtures", "registry", "doubao", "chrome_doubao-chat_0.indexeddb.leveldb")
	listVal := doubaoListValue("conversation-list-data-overall-v2",
		vk("conversation_id", encodeV8String(doubaoConvID)),
		vk("name", encodeV8String(doubaoTitle)),
		vk("create_time", encodeV8Double(1792575407000)),
		vk("update_time", encodeV8Double(1792575500000)),
	)
	taskShort := doubaoTaskValue(doubaoConvID, doubaoPrompt, "降了", doubaoConvID, 1792575407000, 1792575407500)
	taskFull := doubaoTaskValue(doubaoConvID, doubaoPrompt, doubaoReply, doubaoConvID, 1792575407000, 1792575407500)
	entries := []sstEntry{
		{userKey: idbTestStringKey("conversation-list-record"), seq: 5, value: encodeSSV(listVal)},
		{userKey: idbTestStringKey(doubaoProtocolKey), seq: 10, value: encodeSSV(doubaoProtocolValue(vk("task-1", taskShort)))},
	}
	// The streaming generation keeps rewriting the protocol record; the log
	// holds the snapshot with the completed reply.
	batch := encodeWriteBatch(20, []wbOp{{key: idbTestStringKey(doubaoProtocolKey), value: encodeSSV(doubaoProtocolValue(vk("task-1", taskFull)))}})
	writeDoubaoStore(t, dir, entries, [][]byte{batch}, true)
}
