package hookengine

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
	"time"
)

// DefaultStdinWait bounds how long any hook waits for its stdin payload.
const DefaultStdinWait = 300 * time.Millisecond

var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ReadPayload reads at most 1 MiB from r and gives up after wait, returning as
// soon as the bytes buffered so far form a complete JSON object or array.
func ReadPayload(r io.Reader, wait time.Duration) []byte {
	return ReadBounded(r, wait, true)
}

// ReadBounded reads at most 1 MiB from r up to wait. When stopAtValue is
// false, reading continues until EOF or deadline rather than stopping on the
// first valid JSON value.
func ReadBounded(r io.Reader, wait time.Duration, stopAtValue bool) []byte {
	var mu sync.Mutex
	var buf []byte
	done := make(chan struct{})
	go func() {
		defer close(done)
		lr := io.LimitReader(r, 1<<20)
		chunk := make([]byte, 32<<10)
		for {
			n, err := lr.Read(chunk)
			if n > 0 {
				mu.Lock()
				buf = append(buf, chunk[:n]...)
				if len(buf) >= len(utf8BOM) {
					buf = bytes.TrimPrefix(buf, utf8BOM)
				}
				whole := stopAtValue && endsAValue(buf) && json.Valid(buf)
				mu.Unlock()
				if whole {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	select {
	case <-done:
	case <-time.After(wait):
	}
	mu.Lock()
	defer mu.Unlock()
	return append([]byte(nil), buf...)
}

func endsAValue(b []byte) bool {
	b = bytes.TrimRight(b, " \t\r\n")
	if len(b) == 0 {
		return false
	}
	return b[len(b)-1] == '}' || b[len(b)-1] == ']'
}
