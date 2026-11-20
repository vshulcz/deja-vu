package sources

// Chromium IndexedDB value decoding for the Doubao Desktop reader (doubao.go).
// Records in the store are levelled three deep:
//
//   - the record key is Chromium's IndexedDB key encoding (a bit-field prefix
//     naming database/object-store/index ids, then the user key), where the
//     session-bearing records use plain string keys;
//   - the value is Blink's SerializedScriptValue, optionally wrapped by
//     idb_value_wrapping.cc: FF 11 02 prefixes a snappy-compressed stream,
//     FF 11 01 means the value lives in an external blob file (unread here);
//   - after the SSV header (FF <version> FE <trailer>) the bytes are V8's
//     structured serialization stream, read here tag by tag.
//
// The V8 reader covers the tags IndexedDB values actually use and errors on
// anything else: guessing at an unknown tag would invent content. Objects
// keep their written key order — the reply order of one Doubao task is the
// insertion order of `receivedMessages`, which a Go map would scramble.
//
// Shape attested by Nwflower/dsh-chat-import (lib/sources/chromium-indexeddb.mjs)
// and checked against a real 2.37.8 store.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode/utf16"
)

const (
	idbObjectStoreDataIndexID = 1
	maxIDBKeyDepth            = 32
	maxV8Depth                = 128
	maxV8Allocs               = 1 << 20
	maxV8SparsePrealloc       = 1 << 16
)

// v8Obj is a JavaScript plain object with its written key order kept.
type v8Obj struct {
	keys []string
	m    map[string]any
}

func newV8Obj() *v8Obj {
	return &v8Obj{m: map[string]any{}}
}

func (o *v8Obj) set(key string, value any) {
	if _, ok := o.m[key]; !ok {
		o.keys = append(o.keys, key)
	}
	o.m[key] = value
}

func (o *v8Obj) get(key string) (any, bool) {
	v, ok := o.m[key]
	return v, ok
}

// v8Map and v8Set decode Map/Set values. The Doubao reader never needs one,
// and treating them as plain objects would invent fields, so they decode to
// opaque carriers that the object accessor rejects.
type v8Map struct{ entries []any } // alternating key, value
type v8Set struct{ items []any }

// idbKeyPrefix is the decoded KeyPrefix header of an IndexedDB record key.
type idbKeyPrefix struct {
	databaseID    uint64
	objectStoreID uint64
	indexID       uint64
	end           int
}

// decodeIDBKeyPrefix reads the bit-field header: the first byte sizes the
// three id fields that follow, each big-endian.
func decodeIDBKeyPrefix(key []byte) (idbKeyPrefix, bool) {
	if len(key) < 4 {
		return idbKeyPrefix{}, false
	}
	first := key[0]
	dbBytes := int((first>>5)&0x7) + 1
	osBytes := int((first>>2)&0x7) + 1
	indexBytes := int(first&0x3) + 1
	if 1+dbBytes+osBytes+indexBytes > len(key) {
		return idbKeyPrefix{}, false
	}
	read := func(n int, pos int) (uint64, int) {
		var v uint64
		for i := n - 1; i >= 0; i-- {
			v = v<<8 | uint64(key[pos+i])
		}
		return v, pos + n
	}
	dbID, pos := read(dbBytes, 1)
	osID, pos := read(osBytes, pos)
	indexID, pos := read(indexBytes, pos)
	return idbKeyPrefix{databaseID: dbID, objectStoreID: osID, indexID: indexID, end: pos}, true
}

// decodeIDBUserKey decodes the user key that follows a record prefix. Type 1
// strings are UTF-16BE; 2/3 dates and numbers are float64; 4 arrays nest;
// 6 is binary.
func decodeIDBUserKey(buf []byte, pos int) (any, int, error) {
	return decodeIDBUserKeyDepth(buf, pos, 0)
}

func decodeIDBUserKeyDepth(buf []byte, pos, depth int) (any, int, error) {
	if depth > maxIDBKeyDepth {
		return nil, pos, errors.New("idb key: nesting limit exceeded")
	}
	if pos >= len(buf) {
		return nil, pos, errors.New("idb key: empty")
	}
	switch buf[pos] {
	case 0, 5:
		return nil, pos + 1, nil
	case 1:
		length, p, err := readVarint(buf, pos+1)
		if err != nil {
			return nil, p, err
		}
		ln, ok := varintLen(length, (len(buf)-p)/2)
		if !ok {
			return nil, p, errors.New("idb key: string out of bounds")
		}
		units := make([]uint16, ln)
		for i := range units {
			units[i] = uint16(buf[p+2*i])<<8 | uint16(buf[p+2*i+1])
		}
		return string(utf16.Decode(units)), p + ln*2, nil
	case 2, 3:
		if pos+9 > len(buf) {
			return nil, pos, errors.New("idb key: number out of bounds")
		}
		return idbKeyDouble(leFloat64(buf[pos+1 : pos+9])), pos + 9, nil
	case 4:
		length, p, err := readVarint(buf, pos+1)
		if err != nil {
			return nil, p, err
		}
		ln, ok := varintLen(length, len(buf)-p)
		if !ok {
			return nil, p, errors.New("idb key: array out of bounds")
		}
		items := make([]any, 0, ln)
		for range ln {
			var v any
			v, p, err = decodeIDBUserKeyDepth(buf, p, depth+1)
			if err != nil {
				return nil, p, err
			}
			items = append(items, v)
		}
		return items, p, nil
	case 6:
		length, p, err := readVarint(buf, pos+1)
		if err != nil {
			return nil, p, err
		}
		ln, ok := varintLen(length, len(buf)-p)
		if !ok {
			return nil, p, errors.New("idb key: binary out of bounds")
		}
		return append([]byte(nil), buf[p:p+ln]...), p + ln, nil
	default:
		return nil, pos, fmt.Errorf("idb key: unknown type %d", buf[pos])
	}
}

// unwrapIDBValue strips the Blink IDBValue wrapper. FF 11 02 prefixes a
// snappy stream, FF 11 01 marks a value spilled to a blob file (returned as
// not-ok), and a bare SerializedScriptValue is passed through.
func unwrapIDBValue(value []byte) ([]byte, bool) {
	limit := min(len(value)-2, 8)
	for off := range limit {
		if value[off] != 0xff || value[off+1] != 0x11 {
			continue
		}
		switch value[off+2] {
		case 0x02:
			out, err := snappyUncompress(value[off+3:])
			if err != nil {
				return nil, false
			}
			return out, true
		case 0x01:
			return nil, false
		}
	}
	return value, true
}

// ssvPayloadOffset finds where the V8 stream starts after the SSV header
// (FF <varint version> FE + fixed-width trailer), or -1 when absent.
func ssvPayloadOffset(buf []byte) int {
	for i := 0; i+2 < len(buf) && i < 8; i++ {
		if buf[i] != 0xff {
			continue
		}
		_, p, err := readVarint(buf, i+1)
		if err != nil {
			return -1
		}
		if p < len(buf) && buf[p] == 0xfe {
			end := p + 1 + 8 + 4
			if end <= len(buf) {
				return end
			}
			return -1
		}
		return p
	}
	return -1
}

// v8 tags, the subset serialized into IndexedDB values.
const (
	v8TagVersion       = 0xff
	v8TagPadding       = 0x00
	v8TagVerifyCount   = 0x3f
	v8TagTheHole       = 0x2d
	v8TagUndefined     = 0x5f
	v8TagNull          = 0x30
	v8TagTrue          = 0x54
	v8TagFalse         = 0x46
	v8TagInt32         = 0x49
	v8TagUint32        = 0x55
	v8TagDouble        = 0x4e
	v8TagBigInt        = 0x5a
	v8TagUTF8String    = 0x53
	v8TagOneByteString = 0x22
	v8TagTwoByteString = 0x63
	v8TagObjectRef     = 0x5e
	v8TagBeginObject   = 0x6f
	v8TagEndObject     = 0x7b
	v8TagBeginSparse   = 0x61
	v8TagEndSparse     = 0x40
	v8TagBeginDense    = 0x41
	v8TagEndDense      = 0x24
	v8TagDate          = 0x44
	v8TagTrueObject    = 0x79
	v8TagFalseObject   = 0x78
	v8TagNumberObject  = 0x6e
	v8TagStringObject  = 0x73
	v8TagBigIntObject  = 0x7a
	v8TagRegExp        = 0x52
	v8TagBeginMap      = 0x3b
	v8TagEndMap        = 0x3a
	v8TagBeginSet      = 0x27
	v8TagEndSet        = 0x2c
	v8TagArrayBuffer   = 0x42
	v8TagImmutableAB   = 0x43
	v8TagResizableAB   = 0x7e
	v8TagError         = 0x72
	v8TagHostObject    = 0x5c
	v8TagSharedObject  = 0x70
	v8TagErrorEnd      = 0x2e
	v8TagErrorMessage  = 0x6d
	v8TagErrorStack    = 0x73
	v8TagErrorCause    = 0x63
)

// idbKeyDouble is a record key or date stored as a little-endian float64.
type idbKeyDouble float64

// v8Null is the decoded form of JavaScript null and undefined. The field
// accessors narrow by type, so it reads as "no value" wherever it lands.
type v8Null struct{}

func leFloat64(b []byte) float64 {
	return math.Float64frombits(binary.LittleEndian.Uint64(b))
}

// v8Reader walks one V8 structured serialization stream. The methods share
// the position and the object table the stream refers back into.
type v8Reader struct {
	buf    []byte
	pos    int
	table  []any
	depth  int
	allocs int
}

// varint reads one varint, advancing the reader.
func (r *v8Reader) varint() (uint64, error) {
	n, p, err := readVarint(r.buf, r.pos)
	r.pos = p
	return n, err
}

// readDouble reads a little-endian float64, advancing the reader.
func (r *v8Reader) readDouble() (float64, error) {
	if r.pos+8 > len(r.buf) {
		return 0, errors.New("v8: double out of bounds")
	}
	v := leFloat64(r.buf[r.pos : r.pos+8])
	r.pos += 8
	return v, nil
}

// readBigInt skips a BigInt's bit-field and units and yields nothing: no
// Doubao field carries one.
func (r *v8Reader) readBigInt() (any, error) {
	bitfield, err := r.varint()
	if err != nil {
		return nil, err
	}
	units := bitfield >> 2
	if units == 0 {
		units = 1
	}
	avail := len(r.buf) - r.pos
	if avail < 0 || units > uint64(avail) {
		return nil, errors.New("v8: bigint out of bounds")
	}
	r.pos += int(units&0x7fffffffffffffff)/8*8 + 8
	if r.pos > len(r.buf) {
		return nil, errors.New("v8: bigint out of bounds")
	}
	return "(bigint)", nil
}

// readStringBody reads the bytes of a string whose varint byte length has
// been decoded; n is the byte length. The encoding's unit width is the
// caller's to interpret.
func (r *v8Reader) readStringBody(n uint64) ([]byte, error) {
	ln, ok := varintLen(n, len(r.buf)-r.pos)
	if !ok {
		return nil, errors.New("v8: string out of bounds")
	}
	raw := r.buf[r.pos : r.pos+ln]
	r.pos += ln
	return raw, nil
}

// readRef resolves an object-table reference.
func (r *v8Reader) readRef() (any, error) {
	idx, err := r.varint()
	if err != nil {
		return nil, err
	}
	if idx >= uint64(len(r.table)) {
		return nil, errors.New("v8: object reference out of range")
	}
	return r.table[idx], nil
}

// keepValue records a value in the object table and returns it.
func (r *v8Reader) keepValue(v any) any {
	r.table = append(r.table, v)
	return v
}

// readObject reads {key: value, ...} pairs up to the end tag, preserving the
// written key order.
func (r *v8Reader) readObject() (any, error) {
	obj := newV8Obj()
	r.table = append(r.table, obj)
	for {
		if r.pos >= len(r.buf) {
			return nil, errors.New("v8: object not closed")
		}
		if r.buf[r.pos] == v8TagEndObject {
			r.pos++
			_, err := r.varint()
			return obj, err
		}
		key, err := r.readValue()
		if err != nil {
			return nil, err
		}
		val, err := r.readValue()
		if err != nil {
			return nil, err
		}
		obj.set(v8KeyString(key), val)
	}
}

// readDenseArray reads a dense array's elements up to the end tag.
func (r *v8Reader) readDenseArray() (any, error) {
	length, err := r.varint()
	if err != nil {
		return nil, err
	}
	ln, ok := varintLen(length, len(r.buf)-r.pos)
	if !ok || ln > maxV8Allocs-r.allocs {
		return nil, errors.New("v8: dense array out of bounds")
	}
	r.allocs += ln
	arr := make([]any, ln)
	r.table = append(r.table, arr)
	for i := range arr {
		v, err := r.readValue()
		if err != nil {
			return nil, err
		}
		arr[i] = v
	}
	return r.readArrayEnd(arr, v8TagEndDense)
}

// readSparseArray reads index:value pairs up to the end tag.
func (r *v8Reader) readSparseArray() (any, error) {
	length, err := r.varint()
	if err != nil {
		return nil, err
	}
	if length > uint64(len(r.buf)) {
		return nil, errors.New("v8: sparse array out of bounds")
	}
	ln, _ := varintLen(length, len(r.buf))
	allocLen := min(ln, maxV8SparsePrealloc)
	if allocLen > maxV8Allocs-r.allocs {
		return nil, errors.New("v8: sparse array allocation limit exceeded")
	}
	r.allocs += allocLen
	arr := make([]any, allocLen)
	r.table = append(r.table, arr)
	for {
		if r.pos >= len(r.buf) {
			return nil, errors.New("v8: sparse array not closed")
		}
		if r.buf[r.pos] == v8TagEndSparse {
			return r.readArrayEnd(arr, v8TagEndSparse)
		}
		key, err := r.readValue()
		if err != nil {
			return nil, err
		}
		val, err := r.readValue()
		if err != nil {
			return nil, err
		}
		if idx := int64(v8AsNumber(key)); idx >= 0 && idx < int64(len(arr)) {
			arr[idx] = val
		}
	}
}

// readArrayEnd consumes the end tag with its count and padding varints.
func (r *v8Reader) readArrayEnd(arr []any, endTag byte) (any, error) {
	if r.pos >= len(r.buf) || r.buf[r.pos] != endTag {
		return arr, nil
	}
	r.pos++
	if _, err := r.varint(); err != nil {
		return nil, err
	}
	if _, err := r.varint(); err != nil {
		return nil, err
	}
	return arr, nil
}

// readRegExp reads a pattern and its flag varint; only the pattern text is
// kept.
func (r *v8Reader) readRegExp() (any, error) {
	pattern, err := r.readValue()
	if err != nil {
		return nil, err
	}
	if _, err := r.varint(); err != nil {
		return nil, err
	}
	return v8KeyString(pattern), nil
}

// readMap reads key/value pairs up to the end tag.
func (r *v8Reader) readMap() (any, error) {
	m := &v8Map{}
	r.table = append(r.table, m)
	for {
		if r.pos >= len(r.buf) {
			return nil, errors.New("v8: map not closed")
		}
		if r.buf[r.pos] == v8TagEndMap {
			r.pos++
			_, err := r.varint()
			return m, err
		}
		key, err := r.readValue()
		if err != nil {
			return nil, err
		}
		val, err := r.readValue()
		if err != nil {
			return nil, err
		}
		m.entries = append(m.entries, key, val)
	}
}

// readSet reads items up to the end tag.
func (r *v8Reader) readSet() (any, error) {
	set := &v8Set{}
	r.table = append(r.table, set)
	for {
		if r.pos >= len(r.buf) {
			return nil, errors.New("v8: set not closed")
		}
		if r.buf[r.pos] == v8TagEndSet {
			r.pos++
			_, err := r.varint()
			return set, err
		}
		item, err := r.readValue()
		if err != nil {
			return nil, err
		}
		set.items = append(set.items, item)
	}
}

// readBuffer reads an ArrayBuffer's bytes, skipping the max-length varint of
// a resizable one.
func (r *v8Reader) readBuffer(resizable bool) (any, error) {
	n, err := r.varint()
	if err != nil {
		return nil, err
	}
	if resizable {
		if _, err := r.varint(); err != nil {
			return nil, err
		}
	}
	ln, ok := varintLen(n, len(r.buf)-r.pos)
	if !ok {
		return nil, errors.New("v8: array buffer out of bounds")
	}
	bytes := append([]byte(nil), r.buf[r.pos:r.pos+ln]...)
	r.pos += ln
	r.table = append(r.table, bytes)
	return bytes, nil
}

// readError reads an error object's message, stack and cause sub-tags up to
// the end sub-tag.
func (r *v8Reader) readError() (any, error) {
	errObj := map[string]any{"name": "Error", "message": "", "stack": ""}
	r.table = append(r.table, errObj)
	for {
		if r.pos >= len(r.buf) {
			return nil, errors.New("v8: error object not closed")
		}
		sub := r.buf[r.pos]
		r.pos++
		if sub == v8TagErrorEnd {
			return errObj, nil
		}
		var key string
		switch sub {
		case v8TagErrorMessage:
			key = "message"
		case v8TagErrorStack:
			key = "stack"
		case v8TagErrorCause:
			key = "cause"
		default:
			continue
		}
		v, err := r.readValue()
		if err != nil {
			return nil, err
		}
		if key == "cause" {
			errObj[key] = v
		} else {
			errObj[key] = v8KeyString(v)
		}
	}
}

// readValue reads one value from the stream. An unknown tag errors: the value
// is counted as unread rather than guessed at.
func (r *v8Reader) readValue() (any, error) {
	if r.depth >= maxV8Depth {
		return nil, errors.New("v8: nesting limit exceeded")
	}
	r.depth++
	defer func() { r.depth-- }()

	var tag byte
	for {
		if r.pos >= len(r.buf) {
			return nil, errors.New("v8: stream truncated")
		}
		tag = r.buf[r.pos]
		r.pos++
		switch tag {
		case v8TagVersion, v8TagVerifyCount:
			if _, err := r.varint(); err != nil {
				return nil, err
			}
			continue
		case v8TagPadding:
			continue
		}
		break
	}

	switch tag {
	case v8TagUndefined, v8TagTheHole, v8TagNull:
		return v8Null{}, nil
	case v8TagTrue:
		return true, nil
	case v8TagFalse:
		return false, nil
	case v8TagInt32:
		v, err := r.varint()
		if err != nil {
			return nil, err
		}
		return int64(v>>1) ^ -int64(v&1), nil
	case v8TagUint32:
		v, err := r.varint()
		if err != nil {
			return nil, err
		}
		return int64(v & 0xffffffff), nil
	case v8TagDouble:
		return r.readDouble()
	case v8TagBigInt:
		return r.readBigInt()
	case v8TagBigIntObject:
		v, err := r.readBigInt()
		if err != nil {
			return nil, err
		}
		return r.keepValue(v), nil
	case v8TagUTF8String, v8TagOneByteString, v8TagTwoByteString:
		return r.readString(tag)
	case v8TagObjectRef:
		return r.readRef()
	case v8TagBeginObject:
		return r.readObject()
	case v8TagBeginDense:
		return r.readDenseArray()
	case v8TagBeginSparse:
		return r.readSparseArray()
	case v8TagDate:
		v, err := r.readDouble()
		if err != nil {
			return nil, err
		}
		return r.keepValue(v), nil
	case v8TagTrueObject, v8TagFalseObject:
		return r.keepValue(tag == v8TagTrueObject), nil
	case v8TagNumberObject:
		v, err := r.readDouble()
		if err != nil {
			return nil, err
		}
		return r.keepValue(v), nil
	case v8TagStringObject:
		v, err := r.readValue()
		if err != nil {
			return nil, err
		}
		return r.keepValue(v), nil
	case v8TagRegExp:
		return r.readRegExp()
	case v8TagBeginMap:
		return r.readMap()
	case v8TagBeginSet:
		return r.readSet()
	case v8TagArrayBuffer, v8TagImmutableAB:
		return r.readBuffer(false)
	case v8TagResizableAB:
		return r.readBuffer(true)
	case v8TagError:
		return r.readError()
	case v8TagHostObject:
		return "[host object]", nil
	case v8TagSharedObject:
		if _, err := r.varint(); err != nil {
			return nil, err
		}
		return "[shared object]", nil
	default:
		return nil, fmt.Errorf("v8: unknown tag 0x%x at %d", tag, r.pos-1)
	}
}

// readString reads a string of the tag's encoding.
func (r *v8Reader) readString(tag byte) (any, error) {
	if tag == v8TagTwoByteString && r.pos < len(r.buf) && r.buf[r.pos] == v8TagPadding {
		r.pos++ // alignment padding before a two-byte string
	}
	n, err := r.varint()
	if err != nil {
		return nil, err
	}
	raw, err := r.readStringBody(n)
	if err != nil {
		return nil, err
	}
	switch tag {
	case v8TagUTF8String:
		return string(raw), nil
	case v8TagOneByteString:
		runes := make([]rune, len(raw))
		for i := range runes {
			runes[i] = rune(raw[i])
		}
		return string(runes), nil
	case v8TagTwoByteString:
		if len(raw)%2 != 0 {
			return nil, errors.New("v8: odd two-byte string length")
		}
		units := make([]uint16, len(raw)/2)
		for i := range units {
			units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
		}
		return string(utf16.Decode(units)), nil
	default:
		return nil, fmt.Errorf("v8: unexpected string tag 0x%x", tag)
	}
}

// deserializeV8 reads one V8 structured serialization stream starting at start.
func deserializeV8(buf []byte, start int) (any, error) {
	r := &v8Reader{buf: buf, pos: start}
	return r.readValue()
}

// decodeIDBValue turns one stored value into a Go value, trying the SSV
// payload offset first and then byte offsets 0..7: old writers omit the
// header. ok is false only when nothing decoded.
func decodeIDBValue(value []byte) (any, bool) {
	body, ok := unwrapIDBValue(value)
	if !ok {
		return nil, false
	}
	start := ssvPayloadOffset(body)
	var candidates []int
	if start >= 0 {
		candidates = append(candidates, start)
	}
	for i := range 8 {
		if i != start {
			candidates = append(candidates, i)
		}
	}
	for _, off := range candidates {
		if v, err := deserializeV8(body, off); err == nil {
			return v, true
		}
	}
	return nil, false
}

// idbRecord is one object-store data record with its full write history.
type idbRecord struct {
	databaseID    uint64
	objectStoreID uint64
	key           string
	value         any
	versions      []any
	versionCount  int
}

// readIndexedDb decodes every data record of an IndexedDB LevelDB directory,
// keeping all decoded versions of each record in write order. Doubao rewrites
// the protocol record on every streaming increment, and the answer lives in
// those superseded snapshots — the current value alone reads as an empty
// store. Records sort by key so two runs over one store see the same order.
func readIndexedDb(dir string) (records []idbRecord, undecodable int, rawCount int) {
	history := readLevelDbHistory(dir)
	keys := make([]string, 0, len(history))
	for k := range history {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		versions := history[k]
		rawCount += len(versions)
		prefix, ok := decodeIDBKeyPrefix(versions[0].key)
		if !ok || prefix.indexID != idbObjectStoreDataIndexID {
			continue
		}
		userKey, _, err := decodeIDBUserKey(versions[0].key, prefix.end)
		if err != nil {
			undecodable++
			continue
		}
		var decoded []any
		for _, v := range versions {
			if v.value == nil {
				continue
			}
			if value, ok := decodeIDBValue(v.value); ok {
				decoded = append(decoded, value)
			}
		}
		if len(decoded) == 0 {
			undecodable++
			continue
		}
		records = append(records, idbRecord{
			databaseID:    prefix.databaseID,
			objectStoreID: prefix.objectStoreID,
			key:           v8KeyString(userKey),
			value:         decoded[len(decoded)-1],
			versions:      decoded,
			versionCount:  len(versions),
		})
	}
	return records, undecodable, rawCount
}

// v8KeyString stringifies a decoded key the way JavaScript would for object
// fields. Only strings ever match in practice; the rest is diagnostics.
func v8KeyString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "null"
	case bool:
		if x {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprint(v8AsNumber(v))
	}
}

// v8AsNumber narrows a decoded value to float64 for arithmetic and indexing.
func v8AsNumber(v any) float64 {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	case idbKeyDouble:
		return float64(x)
	}
	return 0
}

// v8AsInt narrows to an exact integer where one was written.
func v8AsInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		return int64(x), true
	case idbKeyDouble:
		return int64(x), true
	}
	return 0, false
}

// v8AsString narrows a decoded string.
func v8AsString(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok
}

// v8AsObj narrows a plain object.
func v8AsObj(v any) (*v8Obj, bool) {
	o, ok := v.(*v8Obj)
	return o, ok
}

// v8AsArr narrows an array (dense or sparse decode).
func v8AsArr(v any) ([]any, bool) {
	a, ok := v.([]any)
	return a, ok
}

// v8AsObjAt fetches a field of an object as a nested object.
func v8AsObjAt(o *v8Obj, key string) (*v8Obj, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.get(key)
	if !ok {
		return nil, false
	}
	return v8AsObj(v)
}

// v8ArrAt fetches a field of an object as an array.
func v8ArrAt(o *v8Obj, key string) ([]any, bool) {
	if o == nil {
		return nil, false
	}
	v, ok := o.get(key)
	if !ok {
		return nil, false
	}
	return v8AsArr(v)
}

// v8StringAt fetches a field of an object as a string.
func v8StringAt(o *v8Obj, key string) (string, bool) {
	if o == nil {
		return "", false
	}
	v, ok := o.get(key)
	if !ok {
		return "", false
	}
	return v8AsString(v)
}

// v8TimeAt fetches a field of an object as a Doubao timestamp.
func v8TimeAt(o *v8Obj, key string) (int64, bool) {
	if o == nil {
		return 0, false
	}
	v, ok := o.get(key)
	if !ok {
		return 0, false
	}
	return doubaoTime(v)
}

// stringsHasSuffix anchors the protocol-store suffix on a colon so an
// unrelated key sharing the tail is not swept in.
func idbKeyMatchesSuffix(key, suffix string) bool {
	return key == suffix || strings.HasSuffix(key, ":"+suffix)
}
