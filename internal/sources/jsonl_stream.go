package sources

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"unicode/utf16"
	"unicode/utf8"
)

// A JSONL line longer than the read buffer is decoded as it streams rather
// than read whole. Codex writes a command's whole output into one line, and
// lines of tens of megabytes are real: read whole, a 100 MB line was held as
// the line, a copy of it, the decoder's buffer and the decoded string, and
// took a pass from 363 MB to 782 MB (#5391). Streamed, each string keeps
// longStringCap bytes and the rest is read past: the cap the parsers already
// put on a message, so no message reads differently, and a reader that looks
// past the 64 KB the index stores (a patch's later file headers, the metadata
// after a command's output) still finds it in any string up to the cap.
//
// A string past the cap that holds JSON itself, an arguments or output field
// encoded twice, is decoded the same way and handed on as that JSON with its
// own long strings cut, so a reader that unmarshals it still can.

// longStringCap is how much of one string a streamed line keeps.
var longStringCap = maxParsedMessage

// jsonlBuffer is the line reader's buffer: a line that fits is read whole and
// decoded as before.
const jsonlBuffer = 1 << 20

// maxStreamDepth is encoding/json's nesting limit.
const maxStreamDepth = 10000

var errStreamSyntax = errors.New("invalid JSON")

// jsonLine is one line of a JSONL file as readJSONLine reads it.
type jsonLine struct {
	// raw is a line that fit the buffer, for the caller to decode. It is the
	// reader's buffer, valid until the next read: every caller decodes it at
	// once, and copying each line first was 1.8 GB of a 12k-session build.
	raw []byte
	// long is a line that did not; m and ok are what it decoded to: ok is
	// false for anything but one JSON object, and empty for a blank line.
	long, ok, empty bool
	m               map[string]any
	// ended is false for a last line with no newline.
	ended bool
}

// readJSONLine reads one line of r, to its newline, whatever it holds.
func readJSONLine(r *bufio.Reader) (jsonLine, error) {
	l, _, err := readJSONLineHinted(r, nil)
	return l, err
}

// readJSONLineHinted is readJSONLine that also reports, for a long line,
// whether hint is in the bytes the decoder read.
func readJSONLineHinted(r *bufio.Reader, hint []byte) (jsonLine, bool, error) {
	line, err := r.ReadSlice('\n')
	if !errors.Is(err, bufio.ErrBufferFull) {
		return jsonLine{raw: line, ended: err == nil}, false, err
	}
	src := &lineSrc{r: r, head: trimBOM(bytes.Clone(line)), hint: hint}
	if len(hint) > 0 {
		src.seen = bytes.Contains(src.head, hint)
		src.win = append(src.win, src.head[max(len(src.head)-len(hint)+1, 0):]...)
	}
	l := jsonLine{long: true}
	l.m, l.ok, l.empty = decodeStreamedLine(src)
	l.ended, err = src.drain()
	return l, src.seen, err
}

// readLineBytes reads one line of r for a caller that decodes the bytes
// itself. A line that fits the buffer comes back as it is, valid until the
// next read. A longer one is decoded as it streams and comes back re-encoded,
// its strings cut at longStringCap, or as bytes no decoder takes when it holds
// no object, so a caller that counts the lines it cannot decode still counts
// it. partial is
// a long last line with no newline that does not decode yet: the client is
// still writing it.
func readLineBytes(r *bufio.Reader) (line []byte, partial bool, err error) {
	line, err = r.ReadSlice('\n')
	if !errors.Is(err, bufio.ErrBufferFull) {
		return line, false, err
	}
	head := trimBOM(bytes.Clone(line))
	src := &lineSrc{r: r, head: head}
	m, ok, empty := decodeStreamedLine(src)
	ended, err := src.drain()
	if ended {
		err = nil
	}
	switch {
	case ok:
		var b bytes.Buffer
		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		if enc.Encode(m) != nil {
			return notAnObject, false, err
		}
		return bytes.TrimSuffix(b.Bytes(), []byte{'\n'}), false, err
	case empty:
		return nil, false, err
	case !ended && errors.Is(err, io.EOF):
		return nil, true, err
	}
	return notAnObject, false, err
}

// notAnObject stands for a long line that holds no object. Its head could be
// valid JSON on its own, one record followed by more, so it is not handed on.
var notAnObject = []byte("{")

// streamedLineAt is the line in f's bytes [start, end) as readLineBytes reads
// it, a copy the caller can keep; nil when it does not decode.
func streamedLineAt(f *os.File, start, end int64) []byte {
	r := jsonlReaders.Get().(*bufio.Reader)
	defer func() { r.Reset(nil); jsonlReaders.Put(r) }()
	r.Reset(io.NewSectionReader(f, start, max(end-start, 0)))
	line, _, _ := readLineBytes(r)
	return bytes.Clone(line)
}

func trimBOM(b []byte) []byte {
	return bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
}

// scanJSONLHinted hands fn each line from offset on that decodes to an
// object, and, of the lines that fit the buffer, only those holding hint: the
// rest are not decoded. A longer line is decoded as it streams, and handed
// on when the hint was among its bytes.
func scanJSONLHinted(path string, offset int64, hint []byte, fn func(map[string]any)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if offset > 0 {
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return err
		}
	}
	r := jsonlReaders.Get().(*bufio.Reader)
	defer func() { r.Reset(nil); jsonlReaders.Put(r) }()
	r.Reset(boundedFrom(path, f, offset))
	for {
		l, seen, err := readJSONLineHinted(r, hint)
		switch {
		case l.long && l.ok && seen:
			fn(l.m)
		case !l.long && bytes.Contains(l.raw, hint):
			if m, ok, _ := decodeJSONObjectLine(l.raw); ok {
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

// byteSrc is what the decoder reads: a line, or the decoded text of a string
// that holds JSON. One byte can be put back.
type byteSrc interface {
	readByte() (byte, error)
	unreadByte()
}

// lineSrc yields one line, the part already read first, and io.EOF at its
// newline.
type lineSrc struct {
	r           *bufio.Reader
	head        []byte
	done, ended bool
	err         error
	last        byte
	unread      bool
	// hint, when set, is looked for in the bytes read past head: seen says
	// whether it was, win holds the last bytes read for a match across them.
	hint []byte
	seen bool
	win  []byte
}

func (s *lineSrc) readByte() (byte, error) {
	if s.unread {
		s.unread = false
		return s.last, nil
	}
	var c byte
	switch {
	case len(s.head) > 0:
		c, s.head = s.head[0], s.head[1:]
	case s.done:
		return 0, io.EOF
	default:
		var err error
		if c, err = s.r.ReadByte(); err != nil {
			s.done, s.err = true, err
			return 0, io.EOF
		}
		if len(s.hint) > 0 && !s.seen {
			s.look(c)
		}
	}
	if c == '\n' {
		s.done, s.ended = true, true
		return 0, io.EOF
	}
	s.last = c
	return c, nil
}

func (s *lineSrc) unreadByte() { s.unread = true }

func (s *lineSrc) look(c byte) {
	if len(s.win) == len(s.hint) {
		copy(s.win, s.win[1:])
		s.win = s.win[:len(s.win)-1]
	}
	s.win = append(s.win, c)
	s.seen = bytes.Equal(s.win, s.hint)
}

// drain reads past what is left of the line.
func (s *lineSrc) drain() (ended bool, err error) {
	s.head = nil
	for !s.done {
		_, err := s.r.ReadSlice('\n')
		switch {
		case err == nil:
			s.done, s.ended = true, true
		case !errors.Is(err, bufio.ErrBufferFull):
			s.done, s.err = true, err
		}
	}
	return s.ended, s.err
}

// decodeStreamedLine decodes a line holding one JSON object the way
// json.Unmarshal decodes it into a map[string]any with numbers kept as
// json.Number, each string cut at longStringCap. Anything else on the line,
// a null included, is not an object and ok is false.
func decodeStreamedLine(src byteSrc) (m map[string]any, ok, empty bool) {
	d := &streamDecoder{src: src}
	c, err := d.skipSpace()
	if err != nil {
		return nil, false, true
	}
	if c != '{' {
		return nil, false, false
	}
	if m, err = d.object(1); err != nil {
		return nil, false, false
	}
	if _, err := d.skipSpace(); !errors.Is(err, io.EOF) {
		return nil, false, false
	}
	return m, true, false
}

type streamDecoder struct {
	src byteSrc
}

func (d *streamDecoder) skipSpace() (byte, error) {
	for {
		c, err := d.src.readByte()
		if err != nil {
			return 0, err
		}
		if c != ' ' && c != '\t' && c != '\r' && c != '\n' {
			return c, nil
		}
	}
}

// next is skipSpace for a byte the grammar requires.
func (d *streamDecoder) next() (byte, error) {
	c, err := d.skipSpace()
	if err != nil {
		return 0, errStreamSyntax
	}
	return c, nil
}

func (d *streamDecoder) literal(rest string) error {
	for i := 0; i < len(rest); i++ {
		if c, err := d.src.readByte(); err != nil || c != rest[i] {
			return errStreamSyntax
		}
	}
	return nil
}

// value decodes the value whose first byte is c.
func (d *streamDecoder) value(c byte, depth int) (any, error) {
	if (c == '{' || c == '[') && depth+1 > maxStreamDepth {
		return nil, errStreamSyntax
	}
	switch {
	case c == '{':
		return d.object(depth + 1)
	case c == '[':
		return d.array(depth + 1)
	case c == '"':
		return d.str()
	case c == 't':
		return true, d.literal("true"[1:])
	case c == 'f':
		return false, d.literal("false"[1:])
	case c == 'n':
		return nil, d.literal("null"[1:])
	case c == '-' || c >= '0' && c <= '9':
		return d.number(c)
	}
	return nil, errStreamSyntax
}

func (d *streamDecoder) object(depth int) (map[string]any, error) {
	m := map[string]any{}
	c, err := d.next()
	if err != nil || c == '}' {
		return m, err
	}
	for {
		if c != '"' {
			return nil, errStreamSyntax
		}
		k, err := d.str()
		if err != nil {
			return nil, err
		}
		if c, err = d.next(); err != nil || c != ':' {
			return nil, errStreamSyntax
		}
		if c, err = d.next(); err != nil {
			return nil, err
		}
		v, err := d.value(c, depth)
		if err != nil {
			return nil, err
		}
		m[k] = v
		if c, err = d.next(); err != nil {
			return nil, err
		}
		if c == '}' {
			return m, nil
		}
		if c != ',' {
			return nil, errStreamSyntax
		}
		if c, err = d.next(); err != nil {
			return nil, err
		}
	}
}

func (d *streamDecoder) array(depth int) ([]any, error) {
	out := []any{}
	c, err := d.next()
	if err != nil || c == ']' {
		return out, err
	}
	for {
		v, err := d.value(c, depth)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
		if c, err = d.next(); err != nil {
			return nil, err
		}
		if c == ']' {
			return out, nil
		}
		if c != ',' {
			return nil, errStreamSyntax
		}
		if c, err = d.next(); err != nil {
			return nil, err
		}
	}
}

// maxNumberLen bounds a number's text; nothing real comes near it.
const maxNumberLen = 4096

func (d *streamDecoder) number(c byte) (any, error) {
	b := []byte{c}
	for {
		c, err := d.src.readByte()
		if err != nil {
			break
		}
		if c >= '0' && c <= '9' || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E' {
			if b = append(b, c); len(b) > maxNumberLen {
				return nil, errStreamSyntax
			}
			continue
		}
		d.src.unreadByte()
		break
	}
	// encoding/json judges the grammar of the few bytes.
	var n json.Number
	if json.Unmarshal(b, &n) != nil {
		return nil, errStreamSyntax
	}
	return n, nil
}

// str decodes a string whose opening quote was read. Past longStringCap the
// rest is read and dropped, unless what was kept opens JSON: then the string
// keeps its own text with each string inside it cut, while that fits in twice
// the cap.
func (d *streamDecoder) str() (string, error) {
	s := &strSrc{src: d.src}
	limit := longStringCap + utf8.UTFMax
	var kept []byte
	for len(kept) < limit {
		c, err := s.readByte()
		if errors.Is(err, io.EOF) {
			return string(validUTF8(kept)), nil
		}
		if err != nil {
			return "", err
		}
		kept = append(kept, c)
	}
	if t := bytes.TrimLeft(kept, " \t\r\n"); len(t) > 0 && (t[0] == '{' || t[0] == '[') {
		if v, ok := boundedJSONText(kept, s); ok {
			return v, nil
		}
	}
	if err := s.drain(); err != nil {
		return "", err
	}
	kept = validUTF8(kept)
	cut := longStringCap
	for cut > 0 && !utf8.RuneStart(kept[cut]) {
		cut--
	}
	return string(kept[:cut]), nil
}

// boundedJSONText is a long string that holds JSON, an output or arguments
// field encoded twice, as it was written but with each string inside it cut
// at longStringCap: an old Codex output keeps the metadata after its text.
// Not past twice the cap, and not when the result is not JSON: the string is
// then cut like any other, so JSON of many small values costs no more than
// text does.
func boundedJSONText(head []byte, s *strSrc) (string, bool) {
	budget := 2 * longStringCap
	out := make([]byte, 0, len(head)+4096)
	inStr, esc, skipping := false, false, false
	hexLeft, n := 0, 0
	feed := func(c byte) bool {
		switch {
		case !inStr:
			if c == '"' {
				inStr, n = true, 0
			}
			out = append(out, c)
		case skipping:
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr, skipping = false, false
				out = append(out, c)
			}
		case n >= longStringCap && !esc && hexLeft == 0 && c != '\\' && c != '"' && utf8.RuneStart(c):
			skipping = true
		default:
			out = append(out, c)
			n++
			switch {
			case hexLeft > 0:
				hexLeft--
			case esc:
				esc = false
				if c == 'u' {
					hexLeft = 4
				}
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
		}
		return len(out) <= budget
	}
	for _, c := range head {
		if !feed(c) {
			return "", false
		}
	}
	for {
		c, err := s.readByte()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil || !feed(c) {
			return "", false
		}
	}
	if inStr || !json.Valid(out) {
		return "", false
	}
	return string(out), true
}

// strSrc yields the decoded bytes of a string whose opening quote was read,
// then io.EOF at its closing quote. Escapes decode as encoding/json decodes
// them, a lone surrogate to U+FFFD with what follows read on its own; a raw
// control byte is an error, as there. Invalid UTF-8 is passed through for
// validUTF8.
type strSrc struct {
	src    byteSrc
	out    [utf8.UTFMax]byte
	n, i   int
	done   bool
	last   byte
	unread bool
	// An escape read while looking for a low surrogate, to decode next.
	pendU    rune
	hasPendU bool
	pendC    byte
	hasPendC bool
	// err sticks: a string that broke stays broken for whoever reads on.
	err error
}

func (s *strSrc) unreadByte() { s.unread = true }

func (s *strSrc) readByte() (byte, error) {
	c, err := s.read()
	if err != nil && !errors.Is(err, io.EOF) {
		s.err = err
	}
	return c, err
}

func (s *strSrc) read() (byte, error) {
	if s.err != nil {
		return 0, s.err
	}
	if s.unread {
		s.unread = false
		return s.last, nil
	}
	if s.i < s.n {
		s.last = s.out[s.i]
		s.i++
		return s.last, nil
	}
	if s.done {
		return 0, io.EOF
	}
	var r rune
	var err error
	switch {
	case s.hasPendU:
		s.hasPendU = false
		r, err = s.fromU(s.pendU)
	case s.hasPendC:
		s.hasPendC = false
		r, err = simpleEscape(s.pendC)
	default:
		c, rerr := s.src.readByte()
		switch {
		case rerr != nil:
			return 0, errStreamSyntax
		case c == '"':
			s.done = true
			return 0, io.EOF
		case c < 0x20:
			return 0, errStreamSyntax
		case c != '\\':
			s.last = c
			return c, nil
		}
		r, err = s.escape()
	}
	if err != nil {
		return 0, err
	}
	s.n, s.i = utf8.EncodeRune(s.out[:], r), 1
	s.last = s.out[0]
	return s.last, nil
}

// escape decodes the escape after a backslash.
func (s *strSrc) escape() (rune, error) {
	c, err := s.src.readByte()
	if err != nil {
		return 0, errStreamSyntax
	}
	if c != 'u' {
		return simpleEscape(c)
	}
	h, err := s.hex4()
	if err != nil {
		return 0, err
	}
	return s.fromU(h)
}

// fromU is the rune a \u escape gives, reading the low half of a surrogate
// pair after it.
func (s *strSrc) fromU(h rune) (rune, error) {
	if !utf16.IsSurrogate(h) {
		return h, nil
	}
	c, err := s.src.readByte()
	if err != nil {
		return 0, errStreamSyntax
	}
	if c != '\\' {
		s.src.unreadByte()
		return utf8.RuneError, nil
	}
	c, err = s.src.readByte()
	if err != nil {
		return 0, errStreamSyntax
	}
	if c != 'u' {
		s.pendC, s.hasPendC = c, true
		return utf8.RuneError, nil
	}
	h2, err := s.hex4()
	if err != nil {
		return 0, err
	}
	if r := utf16.DecodeRune(h, h2); r != utf8.RuneError {
		return r, nil
	}
	s.pendU, s.hasPendU = h2, true
	return utf8.RuneError, nil
}

func simpleEscape(c byte) (rune, error) {
	switch c {
	case '"', '\\', '/':
		return rune(c), nil
	case 'b':
		return '\b', nil
	case 'f':
		return '\f', nil
	case 'n':
		return '\n', nil
	case 'r':
		return '\r', nil
	case 't':
		return '\t', nil
	}
	return 0, errStreamSyntax
}

func (s *strSrc) hex4() (rune, error) {
	var r rune
	for range 4 {
		c, err := s.src.readByte()
		if err != nil {
			return 0, errStreamSyntax
		}
		switch {
		case c >= '0' && c <= '9':
			r = r<<4 | rune(c-'0')
		case c >= 'a' && c <= 'f':
			r = r<<4 | rune(c-'a'+10)
		case c >= 'A' && c <= 'F':
			r = r<<4 | rune(c-'A'+10)
		default:
			return 0, errStreamSyntax
		}
	}
	return r, nil
}

// drain reads to the closing quote, checking the bytes as it goes.
func (s *strSrc) drain() error {
	for {
		if _, err := s.readByte(); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// validUTF8 replaces each byte that is not part of valid UTF-8 with U+FFFD,
// as encoding/json does.
func validUTF8(b []byte) []byte {
	if utf8.Valid(b) {
		return b
	}
	out := make([]byte, 0, len(b)+8)
	for len(b) > 0 {
		r, n := utf8.DecodeRune(b)
		if r == utf8.RuneError && n == 1 {
			out = append(out, "�"...)
		} else {
			out = append(out, b[:n]...)
		}
		b = b[n:]
	}
	return out
}
