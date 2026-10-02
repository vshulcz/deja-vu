package sources

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// zstdDecodeFile decompresses a file's frames through the zstd CLI, keeping
// what it can when one frame is bad (#4294).
//
// A log still being appended, or one whose writer died mid-frame, ends in a
// torn frame; a damaged one has a frame that does not decode, with good frames
// possibly after it. zstd stops at either, so on any failure the file is split
// at its frame headers. The whole frames are decoded in one run, and only when
// that fails is each decoded on its own. zstd's stderr is not trusted to tell
// the two apart: a corrupt block size reads as "premature end" in the middle of
// a file. Every frame lost counts as one unusable line in the ingest
// diagnostics, which names the file instead of the store. Only a file nothing
// decodes from is an error.
func zstdDecodeFile(path, harness string, raw []byte) ([]byte, error) {
	out, stderr, err := zstdRun(raw)
	if err == nil {
		return out, nil
	}
	frames, rest := zstdSplitFrames(raw)
	var kept []byte
	if len(frames) > 0 {
		var ferr error
		if kept, _, ferr = zstdRun(bytes.Join(frames, nil)); ferr != nil {
			kept = nil
			for _, f := range frames {
				dec, _, ferr := zstdRun(f)
				if ferr != nil {
					diagMalformedLine(path)
					continue
				}
				kept = append(kept, dec...)
			}
		}
	}
	if len(rest) > 0 {
		// A torn last frame: what zstd decodes of it before the cut is kept.
		dec, _, _ := zstdRun(rest)
		kept = append(kept, dec...)
		diagMalformedLine(path)
	}
	if len(kept) == 0 {
		return nil, fmt.Errorf("%s: zstd -d %s: %w: %s", harness, filepath.Base(path), err, stderr)
	}
	return kept, nil
}

func zstdRun(in []byte) (out []byte, stderr string, err error) {
	cmd := exec.Command("zstd", "-d", "-c", "-q")
	cmd.Stdin = bytes.NewReader(in)
	var o, e bytes.Buffer
	cmd.Stdout = &o
	cmd.Stderr = &e
	err = cmd.Run()
	return o.Bytes(), strings.TrimSpace(e.String()), err
}

// zstdSplitFrames cuts a stream at its frame boundaries by reading the frame
// and block headers (RFC 8878 §3.1), without decoding anything. Where the
// headers do not add up to a whole frame, the cut moves to the next frame
// magic, so a corrupt header loses its own frame, not the rest of the file.
// rest is what follows when no frame magic does: a torn frame, or bytes that
// are not a frame at all.
func zstdSplitFrames(raw []byte) (frames [][]byte, rest []byte) {
	magic := []byte{0x28, 0xB5, 0x2F, 0xFD}
	for len(raw) > 0 {
		n := zstdFrameLen(raw)
		if n <= 0 {
			next := bytes.Index(raw[1:], magic)
			if next < 0 {
				return frames, raw
			}
			n = next + 1
		}
		frames = append(frames, raw[:n])
		raw = raw[n:]
	}
	return frames, nil
}

// zstdFrameLen is the length of the frame raw starts with, or 0 when raw does
// not hold a whole one.
func zstdFrameLen(raw []byte) int {
	if len(raw) < 8 {
		return 0
	}
	magic := binary.LittleEndian.Uint32(raw)
	if magic&0xFFFFFFF0 == 0x184D2A50 {
		// A skippable frame: magic, a 4-byte size, then that many bytes.
		n := 8 + int(binary.LittleEndian.Uint32(raw[4:]))
		if n > len(raw) {
			return 0
		}
		return n
	}
	if magic != 0xFD2FB528 {
		return 0
	}
	fhd := raw[4]
	single := fhd&0x20 != 0
	pos := 5
	if !single {
		pos++ // window descriptor
	}
	pos += []int{0, 1, 2, 4}[fhd&0x03] // dictionary id
	switch fhd >> 6 {                  // frame content size
	case 0:
		if single {
			pos++
		}
	case 1:
		pos += 2
	case 2:
		pos += 4
	case 3:
		pos += 8
	}
	for {
		if pos+3 > len(raw) {
			return 0
		}
		h := uint32(raw[pos]) | uint32(raw[pos+1])<<8 | uint32(raw[pos+2])<<16
		pos += 3
		last := h&1 != 0
		size := int(h >> 3)
		switch (h >> 1) & 3 {
		case 1: // RLE: one byte, repeated
			size = 1
		case 3: // reserved: not a frame
			return 0
		}
		pos += size
		if last {
			break
		}
	}
	if fhd&0x04 != 0 {
		pos += 4 // content checksum
	}
	if pos > len(raw) {
		return 0
	}
	return pos
}
