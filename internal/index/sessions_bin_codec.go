package index

import (
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"math"
	"slices"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

func u64ToInt(v uint64) (int, bool) {
	if v > uint64(math.MaxInt) {
		return 0, false
	}
	return int(v), true
}

func u64ToInt64(v uint64) (int64, bool) {
	if v > uint64(math.MaxInt64) {
		return 0, false
	}
	return int64(v), true
}

func u32ToInt(v uint32) (int, bool) {
	if uint64(v) > uint64(math.MaxInt) {
		return 0, false
	}
	return int(v), true
}

func intToU32(v int) (uint32, bool) {
	if v < 0 || uint64(v) > math.MaxUint32 {
		return 0, false
	}
	return uint32(v), true
}

func intToU64(v int) (uint64, bool) {
	if v < 0 {
		return 0, false
	}
	return uint64(v), true
}

func i64ToInt(v int64) (int, bool) {
	if v < math.MinInt || v > math.MaxInt {
		return 0, false
	}
	return int(v), true
}

func appendBool(dst []byte, v bool) []byte {
	if v {
		return append(dst, 1)
	}
	return append(dst, 0)
}

func appendStr(dst []byte, s string) []byte {
	dst = binary.AppendUvarint(dst, uint64(len(s)))
	return append(dst, s...)
}

func appendTime(dst []byte, t time.Time) ([]byte, error) {
	_, off := t.Zone()
	if t.IsZero() && off == 0 {
		return append(dst, 0), nil
	}
	raw, err := t.MarshalBinary()
	if err != nil {
		return nil, err
	}
	dst = append(dst, 1)
	dst = binary.AppendUvarint(dst, uint64(len(raw)))
	return append(dst, raw...), nil
}

func appendStrSlice(dst []byte, ss []string) []byte {
	if ss == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = binary.AppendUvarint(dst, uint64(len(ss)))
	for _, s := range ss {
		dst = appendStr(dst, s)
	}
	return dst
}

func appendU64Slice(dst []byte, xs []uint64) []byte {
	if xs == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = binary.AppendUvarint(dst, uint64(len(xs)))
	for _, x := range xs {
		dst = binary.AppendUvarint(dst, x)
	}
	return dst
}

func appendIntSlice(dst []byte, xs []int) []byte {
	if xs == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = binary.AppendUvarint(dst, uint64(len(xs)))
	for _, x := range xs {
		dst = binary.AppendVarint(dst, int64(x))
	}
	return dst
}

func appendStrIntMap(dst []byte, m map[string]int) []byte {
	if m == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = binary.AppendUvarint(dst, uint64(len(m)))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		dst = appendStr(dst, k)
		dst = binary.AppendVarint(dst, int64(m[k]))
	}
	return dst
}

func appendModelPtr(dst []byte, m *model.Model) []byte {
	if m == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = appendStr(dst, m.Provider)
	return appendStr(dst, m.ID)
}

func appendTokensPtr(dst []byte, t *model.Tokens) []byte {
	if t == nil {
		return append(dst, 0)
	}
	dst = append(dst, 1)
	dst = binary.AppendVarint(dst, t.Input)
	dst = binary.AppendVarint(dst, t.Output)
	dst = binary.AppendVarint(dst, t.Reasoning)
	return binary.AppendVarint(dst, t.CacheRead)
}

type binReader struct {
	b []byte
}

func (r *binReader) u8() (byte, error) {
	if len(r.b) == 0 {
		return 0, errors.New("unexpected EOF")
	}
	v := r.b[0]
	r.b = r.b[1:]
	return v, nil
}

func (r *binReader) boolVal() (bool, error) {
	v, err := r.u8()
	if err != nil {
		return false, err
	}
	switch v {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, fmt.Errorf("invalid bool tag %d", v)
	}
}

func (r *binReader) uvar() (uint64, error) {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		return 0, errors.New("invalid uvarint")
	}
	r.b = r.b[n:]
	return v, nil
}

func (r *binReader) ivar() (int64, error) {
	v, n := binary.Varint(r.b)
	if n <= 0 {
		return 0, errors.New("invalid varint")
	}
	r.b = r.b[n:]
	return v, nil
}

func (r *binReader) intVal() (int, error) {
	v, err := r.ivar()
	if err != nil {
		return 0, err
	}
	out, ok := i64ToInt(v)
	if !ok {
		return 0, errors.New("int overflow")
	}
	return out, nil
}

func (r *binReader) u32() (uint32, error) {
	v, err := r.uvar()
	if err != nil {
		return 0, err
	}
	if v > math.MaxUint32 {
		return 0, errors.New("uint32 overflow")
	}
	return uint32(v), nil
}

func (r *binReader) str() (string, error) {
	rawLen, err := r.uvar()
	if err != nil {
		return "", err
	}
	n, ok := u64ToInt(rawLen)
	if !ok || n > len(r.b) {
		return "", errors.New("string exceeds remaining bytes")
	}
	s := string(r.b[:n])
	r.b = r.b[n:]
	return s, nil
}

func (r *binReader) timeVal() (time.Time, error) {
	present, err := r.boolVal()
	if err != nil || !present {
		return time.Time{}, err
	}
	rawLen, err := r.uvar()
	if err != nil {
		return time.Time{}, err
	}
	n, ok := u64ToInt(rawLen)
	if !ok || n > len(r.b) || n > 64 {
		return time.Time{}, errors.New("invalid encoded time length")
	}
	var t time.Time
	if uErr := t.UnmarshalBinary(r.b[:n]); uErr != nil {
		return time.Time{}, uErr
	}
	r.b = r.b[n:]
	return t, nil
}

func (r *binReader) sliceLen() (present bool, n int, err error) {
	present, err = r.boolVal()
	if err != nil || !present {
		return false, 0, err
	}
	raw, err := r.uvar()
	if err != nil {
		return false, 0, err
	}
	n, ok := u64ToInt(raw)
	if !ok || n > len(r.b) || n > sessionsBinMaxSliceLen {
		return false, 0, errors.New("slice length out of bounds")
	}
	return true, n, nil
}

func (r *binReader) strSlice() ([]string, error) {
	ok, n, err := r.sliceLen()
	if err != nil || !ok {
		return nil, err
	}
	out := make([]string, n)
	for i := range n {
		if out[i], err = r.str(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *binReader) u64Slice() ([]uint64, error) {
	ok, n, err := r.sliceLen()
	if err != nil || !ok {
		return nil, err
	}
	out := make([]uint64, n)
	for i := range n {
		if out[i], err = r.uvar(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *binReader) intSlice() ([]int, error) {
	ok, n, err := r.sliceLen()
	if err != nil || !ok {
		return nil, err
	}
	out := make([]int, n)
	for i := range n {
		if out[i], err = r.intVal(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (r *binReader) strIntMap() (map[string]int, error) {
	ok, n, err := r.sliceLen()
	if err != nil || !ok {
		return nil, err
	}
	out := make(map[string]int, n)
	for range n {
		k, kErr := r.str()
		if kErr != nil {
			return nil, kErr
		}
		v, vErr := r.intVal()
		if vErr != nil {
			return nil, vErr
		}
		out[k] = v
	}
	return out, nil
}

func (r *binReader) modelPtr() (*model.Model, error) {
	present, err := r.boolVal()
	if err != nil || !present {
		return nil, err
	}
	var out model.Model
	if out.Provider, err = r.str(); err != nil {
		return nil, err
	}
	if out.ID, err = r.str(); err != nil {
		return nil, err
	}
	return &out, nil
}

func (r *binReader) tokensPtr() (*model.Tokens, error) {
	present, err := r.boolVal()
	if err != nil || !present {
		return nil, err
	}
	var out model.Tokens
	if out.Input, err = r.ivar(); err != nil {
		return nil, err
	}
	if out.Output, err = r.ivar(); err != nil {
		return nil, err
	}
	if out.Reasoning, err = r.ivar(); err != nil {
		return nil, err
	}
	if out.CacheRead, err = r.ivar(); err != nil {
		return nil, err
	}
	return &out, nil
}
