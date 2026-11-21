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

func TestDoubaoSSTBlockCorruptSharedLength(t *testing.T) {
	// shared=10 fits pos (>= 10 after restart/prefix) while key is still empty.
	block := []byte{0, 1, 1, 'k', 'v', 10, 1, 1, 'x', 'y', 0, 0, 0}
	entries := sstBlockEntries(block)
	if len(entries) != 1 || string(entries[0].key) != "k" {
		t.Fatalf("entries = %+v, want only the first valid entry", entries)
	}
}

func TestDoubaoV8UTF8String(t *testing.T) {
	const want = "你好，豆包"
	stream := append([]byte{v8TagUTF8String}, uvarint(uint64(len(want)))...)
	stream = append(stream, want...)
	got, err := deserializeV8(stream, 0)
	if err != nil {
		t.Fatalf("deserializeV8 UTF8 string: %v", err)
	}
	if s, _ := v8AsString(got); s != want {
		t.Fatalf("decoded = %q, want %q", s, want)
	}
}

func TestDoubaoV8StringObjectBackref(t *testing.T) {
	strObj := append([]byte{v8TagStringObject}, encodeV8OneByteString("doubao")...)
	bigObj := []byte{v8TagBigIntObject, 0x02, 1, 0, 0, 0, 0, 0, 0, 0}
	refStr := append([]byte{v8TagObjectRef}, uvarint(1)...)
	refBig := append([]byte{v8TagObjectRef}, uvarint(2)...)
	stream := vobj(
		vk("wrap", strObj),
		vk("big", bigObj),
		vk("refWrap", refStr),
		vk("refBig", refBig),
	)
	got, err := deserializeV8(stream, 0)
	if err != nil {
		t.Fatalf("deserializeV8 object refs: %v", err)
	}
	obj, ok := v8AsObj(got)
	if !ok {
		t.Fatalf("decoded = %T, want *v8Obj", got)
	}
	if s, _ := v8StringAt(obj, "refWrap"); s != "doubao" {
		t.Fatalf("refWrap = %q, want %q", s, "doubao")
	}
	if b, _ := v8StringAt(obj, "refBig"); b != "(bigint)" {
		t.Fatalf("refBig = %q, want %q", b, "(bigint)")
	}
}

func TestDoubaoV8PaddingNoStackOverflow(t *testing.T) {
	suffix := encodeV8OneByteString("ok")
	stream := make([]byte, 10000+len(suffix))
	copy(stream[10000:], suffix)
	got, err := deserializeV8(stream, 0)
	if err != nil {
		t.Fatalf("deserializeV8 padded stream: %v", err)
	}
	if s, _ := v8AsString(got); s != "ok" {
		t.Fatalf("decoded = %q, want %q", s, "ok")
	}
}

func TestDoubaoV8TypesAndContainers(t *testing.T) {
	errStream := make([]byte, 0, 64)
	errStream = append(errStream, v8TagError, v8TagErrorMessage)
	errStream = append(errStream, encodeV8OneByteString("boom")...)
	errStream = append(errStream, v8TagErrorStack)
	errStream = append(errStream, encodeV8OneByteString("trace")...)
	errStream = append(errStream, v8TagErrorCause)
	errStream = append(errStream, encodeV8OneByteString("root")...)
	errStream = append(errStream, 0x01, v8TagErrorEnd)

	sparse := make([]byte, 0, 16)
	sparse = append(sparse, v8TagBeginSparse, 0x04, v8TagUint32, 0x02)
	sparse = append(sparse, encodeV8OneByteString("two")...)
	sparse = append(sparse, v8TagEndSparse, 0x01, 0x04)

	v8Map := make([]byte, 0, 16)
	v8Map = append(v8Map, v8TagBeginMap)
	v8Map = append(v8Map, encodeV8OneByteString("mk")...)
	v8Map = append(v8Map, encodeV8OneByteString("mv")...)
	v8Map = append(v8Map, v8TagEndMap, 0x02)

	v8Set := make([]byte, 0, 16)
	v8Set = append(v8Set, v8TagBeginSet)
	v8Set = append(v8Set, encodeV8OneByteString("item")...)
	v8Set = append(v8Set, v8TagEndSet, 0x01)

	paddedTwoByte := []byte{v8TagTwoByteString, v8TagPadding, 0x04, 0x46, 0x8c, 0x05, 0x53}

	reStream := make([]byte, 0, 16)
	reStream = append(reStream, v8TagRegExp)
	reStream = append(reStream, encodeV8OneByteString("foo.*")...)
	reStream = append(reStream, uvarint(1)...)

	// Object table indices inside the outer vobj:
	// 0: outer vobj, 1: regexp kept via keepValue, 2: target vobj, ...
	stream := make([]byte, 0, 512)
	stream = append(stream, v8TagVersion, 15, v8TagVerifyCount, 1)
	stream = append(stream, vobj(
		vk("re", reStream),
		vk("target", vobj(vk("k", encodeV8OneByteString("v")))),
		vk("refTarget", append([]byte{v8TagObjectRef}, uvarint(2)...)),
		vk("undef", []byte{v8TagUndefined}),
		vk("hole", []byte{v8TagTheHole}),
		vk("nul", []byte{v8TagNull}),
		vk("tr", []byte{v8TagTrue}),
		vk("fa", []byte{v8TagFalse}),
		vk("trObj", []byte{v8TagTrueObject}),
		vk("faObj", []byte{v8TagFalseObject}),
		vk("i32", encodeV8Int32(-42)),
		vk("u32", []byte{v8TagUint32, 99}),
		vk("numObj", append([]byte{v8TagNumberObject}, leBytesFloat(3.5)...)),
		vk("dt", append([]byte{v8TagDate}, leBytesFloat(1792575407000)...)),
		vk("bi", []byte{v8TagBigInt, 0x00, 0, 0, 0, 0, 0, 0, 0, 0}),
		vk("tb", paddedTwoByte),
		vk("sp", sparse),
		vk("mp", v8Map),
		vk("st", v8Set),
		vk("ab", []byte{v8TagArrayBuffer, 0x02, 0xaa, 0xbb}),
		vk("iab", []byte{v8TagImmutableAB, 0x02, 0xcc, 0xdd}),
		vk("rab", []byte{v8TagResizableAB, 0x02, 0x04, 0xee, 0xff}),
		vk("err", errStream),
		vk("host", []byte{v8TagHostObject}),
		vk("shared", []byte{v8TagSharedObject, 0x00}),
	)...)

	got, err := deserializeV8(stream, 0)
	if err != nil {
		t.Fatalf("deserializeV8 containers: %v", err)
	}
	obj, ok := v8AsObj(got)
	if !ok {
		t.Fatalf("decoded = %T, want *v8Obj", got)
	}
	if re, _ := v8StringAt(obj, "re"); re != "foo.*" {
		t.Fatalf("re = %q, want %q", re, "foo.*")
	}
	refObj, ok := v8AsObjAt(obj, "refTarget")
	if !ok {
		t.Fatalf("refTarget missing or wrong type")
	}
	if s, _ := v8StringAt(refObj, "k"); s != "v" {
		t.Fatalf("refTarget.k = %q, want %q", s, "v")
	}
	if n, ok := func() (int64, bool) { v, _ := obj.get("i32"); return v8AsInt(v) }(); !ok || n != -42 {
		t.Fatalf("i32 = (%d, %v), want (-42, true)", n, ok)
	}
	if n, ok := func() (int64, bool) { v, _ := obj.get("u32"); return v8AsInt(v) }(); !ok || n != 99 {
		t.Fatalf("u32 = (%d, %v), want (99, true)", n, ok)
	}
	if tb, _ := v8StringAt(obj, "tb"); tb != "豆包" {
		t.Fatalf("tb = %q, want %q", tb, "豆包")
	}
	if sp, ok := v8ArrAt(obj, "sp"); !ok || len(sp) != 4 || sp[2] != "two" {
		t.Fatalf("sp = (%+v, %v), want len 4 with sp[2]=two", sp, ok)
	}
	if ev, _ := obj.get("err"); ev == nil {
		t.Fatalf("expected error object")
	}

	// Malformed V8 streams must return errors cleanly.
	badStreams := [][]byte{
		{v8TagTwoByteString, 0x03, 'a', 'b', 'c'},
		{v8TagBeginDense, 0x10, v8TagNull},
		{v8TagBeginSparse, 0x01},
		{v8TagBeginMap},
		{v8TagBeginSet},
		{v8TagArrayBuffer, 0x10, 0x01},
		{v8TagError, v8TagErrorMessage},
		{v8TagObjectRef, 0x05},
		{v8TagBigInt, 0x00, 0x01, 0x02},
		{0xee},
		{},
	}
	for i, bad := range badStreams {
		if _, err := deserializeV8(bad, 0); err == nil {
			t.Fatalf("badStreams[%d] = %x: expected error", i, bad)
		}
	}

	// Direct value-helper checks.
	if s := v8KeyString(nil); s != "null" {
		t.Fatalf("v8KeyString(nil) = %q", s)
	}
	if s := v8KeyString(true); s != "true" {
		t.Fatalf("v8KeyString(true) = %q", s)
	}
	if s := v8KeyString(false); s != "false" {
		t.Fatalf("v8KeyString(false) = %q", s)
	}
	if s := v8KeyString(int64(7)); s != "7" {
		t.Fatalf("v8KeyString(7) = %q", s)
	}
	if got := v8AsNumber(idbKeyDouble(2.5)); got != 2.5 {
		t.Fatalf("v8AsNumber(idbKeyDouble) = %v", got)
	}
	if got := v8AsNumber("bad"); got != 0 {
		t.Fatalf("v8AsNumber(string) = %v, want 0", got)
	}
	if n, ok := v8AsInt(float64(8)); !ok || n != 8 {
		t.Fatalf("v8AsInt(float64) = (%d, %v)", n, ok)
	}
	if n, ok := v8AsInt(idbKeyDouble(9)); !ok || n != 9 {
		t.Fatalf("v8AsInt(idbKeyDouble) = (%d, %v)", n, ok)
	}
	if _, ok := v8AsInt("bad"); ok {
		t.Fatalf("v8AsInt(string) = true, want false")
	}
	if _, ok := v8AsObjAt(nil, "a"); ok {
		t.Fatalf("v8AsObjAt(nil) = true")
	}
	if _, ok := v8ArrAt(nil, "a"); ok {
		t.Fatalf("v8ArrAt(nil) = true")
	}
	if _, ok := v8StringAt(nil, "a"); ok {
		t.Fatalf("v8StringAt(nil) = true")
	}
	if _, ok := v8TimeAt(nil, "a"); ok {
		t.Fatalf("v8TimeAt(nil) = true")
	}
	emptyObj := newV8Obj()
	if _, ok := v8AsObjAt(emptyObj, "missing"); ok {
		t.Fatalf("v8AsObjAt(missing) = true")
	}
	if _, ok := v8ArrAt(emptyObj, "missing"); ok {
		t.Fatalf("v8ArrAt(missing) = true")
	}
	if _, ok := v8StringAt(emptyObj, "missing"); ok {
		t.Fatalf("v8StringAt(missing) = true")
	}
	if _, ok := v8TimeAt(emptyObj, "missing"); ok {
		t.Fatalf("v8TimeAt(missing) = true")
	}
}

func TestDoubaoIDBKeyAndSSVVariants(t *testing.T) {
	if _, _, err := decodeIDBUserKey(nil, 0); err == nil {
		t.Fatalf("empty key should error")
	}
	for _, tag := range []byte{0, 5} {
		v, p, err := decodeIDBUserKey([]byte{tag}, 0)
		if err != nil || v != nil || p != 1 {
			t.Fatalf("tag %d => (%v, %d, %v)", tag, v, p, err)
		}
	}
	for _, tag := range []byte{2, 3} {
		k := append([]byte{tag}, leBytesFloat(42.5)...)
		v, p, err := decodeIDBUserKey(k, 0)
		if err != nil || p != 9 || v != idbKeyDouble(42.5) {
			t.Fatalf("number tag %d => (%v, %d, %v)", tag, v, p, err)
		}
		if _, _, err := decodeIDBUserKey([]byte{tag, 1, 2}, 0); err == nil {
			t.Fatalf("truncated number tag %d should error", tag)
		}
	}

	// Nested array: [string, number, binary].
	strElem := []byte{1, 2, 0, 'h', 0, 'i'}
	numElem := append([]byte{2}, leBytesFloat(7)...)
	binElem := []byte{6, 2, 0xca, 0xfe}
	arrKey := make([]byte, 0, 2+len(strElem)+len(numElem)+len(binElem))
	arrKey = append(arrKey, 4, 3)
	arrKey = append(arrKey, strElem...)
	arrKey = append(arrKey, numElem...)
	arrKey = append(arrKey, binElem...)
	v, _, err := decodeIDBUserKey(arrKey, 0)
	if err != nil {
		t.Fatalf("array key: %v", err)
	}
	items, ok := v.([]any)
	if !ok || len(items) != 3 || items[0] != "hi" || items[1] != idbKeyDouble(7) {
		t.Fatalf("items = %+v", items)
	}
	if b, ok := items[2].([]byte); !ok || string(b) != "\xca\xfe" {
		t.Fatalf("binary item = %x", b)
	}

	// Nesting limit and invalid keys.
	deep := make([]byte, 0, 71)
	for range 35 {
		deep = append(deep, 4, 1)
	}
	deep = append(deep, 0)
	for _, bad := range [][]byte{
		deep,
		{1, 10, 0, 'a'},
		{4, 10, 0},
		{6, 10, 0x01},
		{99},
	} {
		if _, _, err := decodeIDBUserKey(bad, 0); err == nil {
			t.Fatalf("expected key error for %x", bad)
		}
	}

	// unwrapIDBValue cases.
	if _, ok := unwrapIDBValue([]byte{0xff, 0x11, 0x01}); ok {
		t.Fatalf("spilled blob wrapper should return false")
	}
	if _, ok := unwrapIDBValue([]byte{0xff, 0x11, 0x02, 0xff}); ok {
		t.Fatalf("corrupt snappy wrapper should return false")
	}
	if got, ok := unwrapIDBValue([]byte("plain")); !ok || string(got) != "plain" {
		t.Fatalf("plain unwrap = (%q, %v)", got, ok)
	}

	// ssvPayloadOffset cases.
	withTrailer := append([]byte{0xff, 0x01, 0xfe}, make([]byte, 12)...)
	if off := ssvPayloadOffset(withTrailer); off != 15 {
		t.Fatalf("ssvPayloadOffset(withTrailer) = %d, want 15", off)
	}
	if off := ssvPayloadOffset([]byte{0xff, 0x01, 0xfe, 1, 2}); off != -1 {
		t.Fatalf("ssvPayloadOffset(short trailer) = %d, want -1", off)
	}
	if off := ssvPayloadOffset([]byte{0xff, 0x01, 0x6f}); off != 2 {
		t.Fatalf("ssvPayloadOffset(bare version) = %d, want 2", off)
	}
	if off := ssvPayloadOffset([]byte("no-header")); off != -1 {
		t.Fatalf("ssvPayloadOffset(none) = %d, want -1", off)
	}
}

func TestDoubaoSnappyAndLevelDBEdges(t *testing.T) {
	// Valid stream exercising tag 0 (1-byte extra literal), copy1, copy2, copy4.
	validSnappy := []byte{
		0x10,                    // outLen = 16
		60 << 2, 0x01, 'a', 'b', // literal len 2 (extra=1, byte=1 -> 2)
		1 << 2, 'c', 'd', // literal len 2
		0x01, 0x04, // copy1 len=4 off=4 -> "abcd"
		0x0e, 0x04, 0x00, // copy2 len=4 off=4 -> "abcd"
		0x0f, 0x04, 0x00, 0x00, 0x00, // copy4 len=4 off=4 -> "abcd"
	}
	gotSnappy, err := snappyUncompress(validSnappy)
	if err != nil || string(gotSnappy) != "abcdabcdabcdabcd" {
		t.Fatalf("snappyUncompress = (%q, %v)", gotSnappy, err)
	}

	badSnappy := [][]byte{
		{},
		{0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01},
		{0x80, 0x80, 0x80, 0x80, 0x01}, // > 64MiB max output
		{0x04, 60 << 2},
		{0x02, 0x08, 'a', 'b', 'c'},
		{0x04, 0x01},
		{0x05, 0x01, 0x08}, // copy1 OOB
		{0x05, 0x02, 0x00}, // truncated copy2
		{0x05, 0x0e, 0x08, 0x00},
		{0x05, 0x03, 0x00, 0x00}, // truncated copy4
		{0x05, 0x0f, 0x08, 0x00, 0x00, 0x00},
		{0x08, 0x00, 'a'}, // length mismatch (expected 8, decoded 1)
	}
	for i, bad := range badSnappy {
		if _, err := snappyUncompress(bad); err == nil {
			t.Fatalf("badSnappy[%d] = %x: expected error", i, bad)
		}
	}

	// leveldbBlock cases.
	if b := leveldbBlock([]byte{'x', 'y', 0}, 0, 2); string(b) != "xy" {
		t.Fatalf("plain block = %q", b)
	}
	compressed := append(snappyLiteral([]byte("hi")), 1)
	if b := leveldbBlock(compressed, 0, len(compressed)-1); string(b) != "hi" {
		t.Fatalf("snappy block = %q", b)
	}
	if b := leveldbBlock([]byte{0xff, 0x01}, 0, 1); b != nil {
		t.Fatalf("corrupt snappy block = %x, want nil", b)
	}
	if b := leveldbBlock([]byte{'x', 'y', 2}, 0, 2); b != nil {
		t.Fatalf("unknown block type = %x, want nil", b)
	}
	if b := leveldbBlock([]byte{'x'}, -1, 1); b != nil {
		t.Fatalf("negative offset block = %x, want nil", b)
	}

	// sstEntries invalid shapes.
	if e := sstEntries([]byte("short")); len(e) != 0 {
		t.Fatalf("short sst = %+v", e)
	}
	badFooter := make([]byte, 48)
	badFooter[2] = 0xff // out-of-bounds index offset
	if e := sstEntries(badFooter); len(e) != 0 {
		t.Fatalf("badFooter sst = %+v", e)
	}

	// leveldbLogRecords: fragmentation (2/3/4), block tail skip, zero frame, bad CRC, nil stats.
	fillBatch := make([]byte, leveldbBlockSize-10) // 7+32758=32765 leaves 3-byte block tail (<7)
	bigBatch := make([]byte, leveldbBlockSize*2)
	for i := range bigBatch {
		bigBatch[i] = byte('a' + i%26)
	}
	wal := frameWAL([][]byte{fillBatch, bigBatch})
	var st leveldbFrameStats
	recs := leveldbLogRecords(wal, &st)
	if len(recs) != 2 || st.goodFrames < 4 || st.badFrames != 0 {
		t.Fatalf("recs=%d stats=%+v", len(recs), st)
	}
	// frameType 0 is skipped, and a corrupted CRC increments badFrames.
	badWAL := []byte{0, 0, 0, 0, 2, 0, 0, 'x', 'y', 0, 0, 0, 0, 1, 0, 1, 'z'}
	var badSt leveldbFrameStats
	if badRecs := leveldbLogRecords(badWAL, &badSt); len(badRecs) != 1 || badSt.badFrames != 1 {
		t.Fatalf("badWAL recs=%d stats=%+v", len(badRecs), badSt)
	}
	// Nil stats on valid framed batch must not panic.
	if nilRecs := leveldbLogRecords(frameWAL([][]byte{[]byte("ok")}), nil); len(nilRecs) != 1 {
		t.Fatalf("nil stats recs = %d, want 1", len(nilRecs))
	}

	// writeBatchEntries: short, delete+put mix, truncated varint.
	if got := writeBatchEntries([]byte("short")); len(got) != 0 {
		t.Fatalf("short writeBatch = %+v", got)
	}
	wb := encodeWriteBatch(1, []wbOp{
		{del: true, key: []byte("gone")},
		{key: []byte("live"), value: []byte("val")},
	})
	entries := writeBatchEntries(wb)
	if len(entries) != 2 || entries[0].value != nil || string(entries[1].value) != "val" {
		t.Fatalf("writeBatch entries = %+v", entries)
	}
	truncWB := make([]byte, 0, 14)
	truncWB = append(truncWB, wb[:12]...)
	truncWB = append(truncWB, 1, 0x80)
	if got := writeBatchEntries(truncWB); len(got) != 0 {
		t.Fatalf("truncated writeBatch = %+v", got)
	}
}

func TestDoubaoTimeAndIncrementalHelpers(t *testing.T) {
	if ms, ok := doubaoTime(int64(1700000000)); !ok || ms != 1700000000000 {
		t.Fatalf("int64 seconds = (%d, %v)", ms, ok)
	}
	if ms, ok := doubaoTime(float64(1700000000123)); !ok || ms != 1700000000123 {
		t.Fatalf("float64 ms = (%d, %v)", ms, ok)
	}
	if ms, ok := doubaoTime(idbKeyDouble(1700000000)); !ok || ms != 1700000000000 {
		t.Fatalf("idbKeyDouble = (%d, %v)", ms, ok)
	}
	for _, in := range []string{"1700000000", "2026-11-20T12:00:00Z", "2026-11-20 12:00:00", "2026-11-20"} {
		if _, ok := doubaoTime(in); !ok {
			t.Fatalf("doubaoTime(%q) = false", in)
		}
	}
	for _, bad := range []any{"", "   ", "not-a-date", math.NaN(), math.Inf(1), 1e18, struct{}{}} {
		if _, ok := doubaoTime(bad); ok {
			t.Fatalf("doubaoTime(%v) = true, want false", bad)
		}
	}

	if k := doubaoTurnKey(doubaoTask{promptTimeOK: true, promptTime: 10}); k != 10 {
		t.Fatalf("turnKey prompt = %d", k)
	}
	if k := doubaoTurnKey(doubaoTask{replyTimeOK: true, replyTime: 20}); k != 20 {
		t.Fatalf("turnKey reply = %d", k)
	}
	if k := doubaoTurnKey(doubaoTask{}); k != math.MaxInt64 {
		t.Fatalf("turnKey empty = %d", k)
	}

	// DoubaoRoot, DoubaoFiles, IsDoubaoLevelDbFile, ParseDoubaoFileSince.
	t.Setenv("DEJA_DOUBAO_ROOT", "")
	if r := DoubaoRoot(); !strings.Contains(r, "Doubao") {
		t.Fatalf("default DoubaoRoot = %q", r)
	}

	root := doubaoBaseStore(t, false)
	t.Setenv("DEJA_DOUBAO_ROOT", root)
	files := DoubaoFiles()
	if len(files) != 1 {
		t.Fatalf("DoubaoFiles() = %+v, want 1 file", files)
	}
	if !IsDoubaoLevelDbFile(files[0]) || IsDoubaoLevelDbFile(files[0]+".bak") || IsDoubaoLevelDbFile(filepath.Join(root, "other", "000001.ldb")) {
		t.Fatalf("IsDoubaoLevelDbFile unexpected result on %s", files[0])
	}

	all, err := ParseDoubaoFileSince(files[0], time.Time{})
	if err != nil || len(all) != 1 {
		t.Fatalf("ParseDoubaoFileSince(zero) = (%d, %v)", len(all), err)
	}
	recent, err := ParseDoubaoFileSince(files[0], time.UnixMilli(0))
	if err != nil || len(recent) != 1 {
		t.Fatalf("ParseDoubaoFileSince(epoch) = (%d, %v)", len(recent), err)
	}
	none, err := ParseDoubaoFileSince(files[0], time.Now().Add(3650*24*time.Hour))
	if err != nil || len(none) != 0 {
		t.Fatalf("ParseDoubaoFileSince(future) = (%d, %v)", len(none), err)
	}
}
