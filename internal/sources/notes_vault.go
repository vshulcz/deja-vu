package sources

import (
	"bufio"
	"bytes"
	"io"

	"github.com/vshulcz/deja-vu/internal/vault"
)

func personalNotesDEK(path string) ([]byte, error) {
	if path != NotesFile() || !vault.Enabled() {
		return nil, nil
	}
	return vault.CheckUnlocked("")
}

func sealPersonalNoteLine(dek, rawJSON []byte) ([]byte, error) {
	plain := bytes.TrimRight(rawJSON, "\r\n")
	if len(dek) == 0 {
		return append(plain, '\n'), nil
	}
	enc, err := vault.SealNoteLine(dek, plain)
	if err != nil {
		return nil, err
	}
	return []byte(enc + "\n"), nil
}

func openPersonalNoteLine(dek, line []byte) ([]byte, bool) {
	if !bytes.HasPrefix(bytes.TrimSpace(line), []byte(vault.NoteLinePrefix)) {
		return line, true
	}
	if len(dek) == 0 {
		return nil, false
	}
	plain, err := vault.OpenNoteLine(dek, string(line))
	if err != nil {
		return nil, false
	}
	return plain, true
}

// vaultLineReader decrypts NoteLinePrefix lines incrementally without extra goroutines.
type vaultLineReader struct {
	br  *bufio.Reader
	dek []byte
	buf []byte
	eof bool
}

func newVaultLineReader(r io.Reader, dek []byte) io.Reader {
	if len(dek) == 0 {
		return r
	}
	return &vaultLineReader{br: bufio.NewReaderSize(r, 64*1024), dek: dek}
}

func (vr *vaultLineReader) Read(p []byte) (int, error) {
	for len(vr.buf) == 0 {
		if vr.eof {
			return 0, io.EOF
		}
		line, err := vr.br.ReadBytes('\n')
		if len(line) > 0 {
			trimmed := bytes.TrimRight(line, "\r\n")
			if bytes.HasPrefix(trimmed, []byte(vault.NoteLinePrefix)) {
				if plain, ok := openPersonalNoteLine(vr.dek, trimmed); ok {
					vr.buf = append(plain, '\n')
				} else {
					vr.buf = []byte("{malformed-sealed}\n")
				}
			} else {
				vr.buf = line
			}
		}
		if err != nil {
			if err == io.EOF {
				vr.eof = true
				if len(vr.buf) == 0 {
					return 0, io.EOF
				}
				break
			}
			return 0, err
		}
	}
	n := copy(p, vr.buf)
	vr.buf = vr.buf[n:]
	return n, nil
}
