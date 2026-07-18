package embed

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"strings"

	"github.com/vshulcz/deja-vu/internal/index"
)

const sidecarVersion uint16 = 1

var magic = [4]byte{'D', 'J', 'V', '1'}

type Vector struct {
	Offset int64
	Key    string
	Values []float32
}
type Sidecar struct {
	Model, Generation string
	Dim               int
	Vectors           []Vector
	Covered           int
}

func Path(dir string) string {
	return strings.TrimSuffix(dir, string(os.PathSeparator)) + ".vectors.bin"
}

func EmbedIndex(dir string, client *Client) (Sidecar, error) {
	recs, err := index.ReadRecords(dir)
	if err != nil {
		return Sidecar{}, fmt.Errorf("read index records: %w", err)
	}
	gen, err := index.Generation(dir)
	if err != nil {
		return Sidecar{}, err
	}
	old, _ := Read(dir)
	if old.Model != client.Model || old.Generation != gen || old.Dim == 0 {
		old = Sidecar{}
	}
	seen := make(map[int64]bool, len(old.Vectors))
	for _, v := range old.Vectors {
		seen[v.Offset] = true
	}
	var pending []index.OffsetRecord
	for _, r := range recs {
		if !seen[r.Offset] {
			pending = append(pending, r)
		}
	}
	for len(pending) > 0 {
		n := len(pending)
		if n > 32 {
			n = 32
		}
		texts := make([]string, n)
		for i := range pending[:n] {
			texts[i] = truncate(pending[i].Record.Text)
		}
		vectors, err := client.Embed(context.Background(), texts)
		if err != nil {
			return Sidecar{}, err
		}
		for i, values := range vectors {
			if old.Dim == 0 {
				old.Dim = len(values)
			}
			if len(values) != old.Dim {
				return Sidecar{}, fmt.Errorf("embedding dimension changed from %d to %d", old.Dim, len(values))
			}
			old.Vectors = append(old.Vectors, Vector{Offset: pending[i].Offset, Key: pending[i].Record.Key, Values: values})
		}
		pending = pending[n:]
	}
	old.Model, old.Generation, old.Covered = client.Model, gen, len(recs)
	if err := write(dir, old); err != nil {
		return Sidecar{}, err
	}
	return old, nil
}

func truncate(s string) string {
	const limit = 2000
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit-12]) + "\n[truncated]"
}

func Read(dir string) (Sidecar, error) {
	f, err := os.Open(Path(dir))
	if err != nil {
		return Sidecar{}, err
	}
	defer func() { _ = f.Close() }()
	var got [4]byte
	if _, err = f.Read(got[:]); err != nil || got != magic {
		return Sidecar{}, fmt.Errorf("invalid vector sidecar")
	}
	var version, dim uint16
	var count, covered uint64
	var model, gen string
	if err = binary.Read(f, binary.LittleEndian, &version); err != nil || version != sidecarVersion {
		return Sidecar{}, fmt.Errorf("unsupported vector sidecar")
	}
	if err = binary.Read(f, binary.LittleEndian, &dim); err != nil {
		return Sidecar{}, err
	}
	if model, err = readString(f); err != nil {
		return Sidecar{}, err
	}
	if gen, err = readString(f); err != nil {
		return Sidecar{}, err
	}
	if err = binary.Read(f, binary.LittleEndian, &count); err != nil {
		return Sidecar{}, err
	}
	if count > 10_000_000 || dim > 16_384 {
		return Sidecar{}, fmt.Errorf("invalid vector sidecar dimensions")
	}
	if err = binary.Read(f, binary.LittleEndian, &covered); err != nil {
		return Sidecar{}, err
	}
	out := Sidecar{Model: model, Generation: gen, Dim: int(dim), Covered: int(covered), Vectors: make([]Vector, count)}
	for i := range out.Vectors {
		if err = binary.Read(f, binary.LittleEndian, &out.Vectors[i].Offset); err != nil {
			return Sidecar{}, err
		}
		if out.Vectors[i].Key, err = readString(f); err != nil {
			return Sidecar{}, err
		}
		out.Vectors[i].Values = make([]float32, out.Dim)
		if err = binary.Read(f, binary.LittleEndian, out.Vectors[i].Values); err != nil {
			return Sidecar{}, err
		}
	}
	return out, nil
}

func write(dir string, s Sidecar) error {
	tmp := Path(dir) + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	good := false
	defer func() {
		if !good {
			_ = os.Remove(tmp)
		}
	}()
	if _, err = f.Write(magic[:]); err == nil {
		err = binary.Write(f, binary.LittleEndian, sidecarVersion)
	}
	if err == nil {
		err = binary.Write(f, binary.LittleEndian, uint16(s.Dim))
	}
	if err == nil {
		err = writeString(f, s.Model)
	}
	if err == nil {
		err = writeString(f, s.Generation)
	}
	if err == nil {
		err = binary.Write(f, binary.LittleEndian, uint64(len(s.Vectors)))
	}
	if err == nil {
		err = binary.Write(f, binary.LittleEndian, uint64(s.Covered))
	}
	for _, v := range s.Vectors {
		if err == nil {
			err = binary.Write(f, binary.LittleEndian, v.Offset)
		}
		if err == nil {
			err = writeString(f, v.Key)
		}
		if err == nil {
			err = binary.Write(f, binary.LittleEndian, v.Values)
		}
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if err = os.Rename(tmp, Path(dir)); err != nil {
		return err
	}
	good = true
	return nil
}
func writeString(w interface{ Write([]byte) (int, error) }, s string) error {
	if err := binary.Write(w, binary.LittleEndian, uint32(len(s))); err != nil {
		return err
	}
	_, err := w.Write([]byte(s))
	return err
}
func readString(r io.Reader) (string, error) {
	var n uint32
	if err := binary.Read(r, binary.LittleEndian, &n); err != nil {
		return "", err
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return string(b), nil
}
func Cosine(a, b []float32) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var dot, aa, bb float64
	for i := range a {
		x, y := float64(a[i]), float64(b[i])
		dot += x * y
		aa += x * x
		bb += y * y
	}
	if aa == 0 || bb == 0 {
		return 0
	}
	return dot / math.Sqrt(aa*bb)
}
