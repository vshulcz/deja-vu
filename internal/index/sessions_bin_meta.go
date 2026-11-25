package index

import (
	"encoding/binary"
	"fmt"
)

func appendSessionMeta(dst []byte, m SessionMeta) ([]byte, error) {
	dst = appendStr(dst, m.ID)
	dst = appendStr(dst, m.Harness)
	dst = appendStr(dst, m.Project)
	dst = appendStr(dst, m.Path)
	dst = appendStr(dst, m.Title)
	var err error
	if dst, err = appendTime(dst, m.Started); err != nil {
		return nil, err
	}
	if dst, err = appendTime(dst, m.Updated); err != nil {
		return nil, err
	}
	dst = binary.AppendUvarint(dst, uint64(m.Ord))
	dst = appendU64Slice(dst, m.Asked)
	dst = appendStrSlice(dst, m.Touched)
	dst = appendStr(dst, m.Settled)
	dst = appendBool(dst, m.RanCommand)
	dst = appendBool(dst, m.Shared)
	dst = appendBool(dst, m.AgentTitle)
	if dst, err = appendTime(dst, m.SharedStarted); err != nil {
		return nil, err
	}
	if dst, err = appendTime(dst, m.SharedUpdated); err != nil {
		return nil, err
	}
	dst = appendStrSlice(dst, m.Copies)
	dst = appendStr(dst, m.OrigID)
	dst = appendStr(dst, m.RemoteID)
	dst = appendStr(dst, m.Kind)
	dst = appendStr(dst, m.Parent)
	dst = appendStr(dst, m.Agent)
	dst = appendModelPtr(dst, m.Model)
	dst = appendTokensPtr(dst, m.Tokens)
	dst = appendStrIntMap(dst, m.ToolCalls)
	dst = binary.AppendVarint(dst, int64(m.ToolErrors))
	dst = binary.AppendUvarint(dst, m.Opening)
	dst = appendStr(dst, m.From)
	dst = appendStr(dst, m.Lifecycle)
	dst = appendStr(dst, m.LifecycleNote)
	dst = appendStr(dst, m.LifecycleAt)
	dst = appendBool(dst, m.GaveUp)
	dst = binary.AppendVarint(dst, int64(m.Words))
	dst = appendBool(dst, m.NoText)
	dst = binary.AppendVarint(dst, int64(m.Counted))
	dst = binary.AppendUvarint(dst, m.LastMsg)
	dst = appendIntSlice(dst, m.TouchHits)
	dst = appendU64Slice(dst, m.Hit)
	dst = appendStrSlice(dst, m.Rules)
	dst = appendStrSlice(dst, m.Revokes)
	dst = appendStrSlice(dst, m.Planted)
	return dst, nil
}

func decodeSessionMetaExact(b []byte) (SessionMeta, error) {
	r := &binReader{b: b}
	var m SessionMeta
	var err error
	if m.ID, err = r.str(); err != nil {
		return m, err
	}
	if m.Harness, err = r.str(); err != nil {
		return m, err
	}
	if m.Project, err = r.str(); err != nil {
		return m, err
	}
	if m.Path, err = r.str(); err != nil {
		return m, err
	}
	if m.Title, err = r.str(); err != nil {
		return m, err
	}
	if m.Started, err = r.timeVal(); err != nil {
		return m, err
	}
	if m.Updated, err = r.timeVal(); err != nil {
		return m, err
	}
	if m.Ord, err = r.u32(); err != nil {
		return m, err
	}
	if m.Asked, err = r.u64Slice(); err != nil {
		return m, err
	}
	if m.Touched, err = r.strSlice(); err != nil {
		return m, err
	}
	if m.Settled, err = r.str(); err != nil {
		return m, err
	}
	if m.RanCommand, err = r.boolVal(); err != nil {
		return m, err
	}
	if m.Shared, err = r.boolVal(); err != nil {
		return m, err
	}
	if m.AgentTitle, err = r.boolVal(); err != nil {
		return m, err
	}
	if m.SharedStarted, err = r.timeVal(); err != nil {
		return m, err
	}
	if m.SharedUpdated, err = r.timeVal(); err != nil {
		return m, err
	}
	if m.Copies, err = r.strSlice(); err != nil {
		return m, err
	}
	if m.OrigID, err = r.str(); err != nil {
		return m, err
	}
	if m.RemoteID, err = r.str(); err != nil {
		return m, err
	}
	if m.Kind, err = r.str(); err != nil {
		return m, err
	}
	if m.Parent, err = r.str(); err != nil {
		return m, err
	}
	if m.Agent, err = r.str(); err != nil {
		return m, err
	}
	if m.Model, err = r.modelPtr(); err != nil {
		return m, err
	}
	if m.Tokens, err = r.tokensPtr(); err != nil {
		return m, err
	}
	if m.ToolCalls, err = r.strIntMap(); err != nil {
		return m, err
	}
	if m.ToolErrors, err = r.intVal(); err != nil {
		return m, err
	}
	if m.Opening, err = r.uvar(); err != nil {
		return m, err
	}
	if m.From, err = r.str(); err != nil {
		return m, err
	}
	if m.Lifecycle, err = r.str(); err != nil {
		return m, err
	}
	if m.LifecycleNote, err = r.str(); err != nil {
		return m, err
	}
	if m.LifecycleAt, err = r.str(); err != nil {
		return m, err
	}
	if m.GaveUp, err = r.boolVal(); err != nil {
		return m, err
	}
	if m.Words, err = r.intVal(); err != nil {
		return m, err
	}
	if m.NoText, err = r.boolVal(); err != nil {
		return m, err
	}
	if m.Counted, err = r.intVal(); err != nil {
		return m, err
	}
	if m.LastMsg, err = r.uvar(); err != nil {
		return m, err
	}
	if m.TouchHits, err = r.intSlice(); err != nil {
		return m, err
	}
	if m.Hit, err = r.u64Slice(); err != nil {
		return m, err
	}
	if m.Rules, err = r.strSlice(); err != nil {
		return m, err
	}
	if m.Revokes, err = r.strSlice(); err != nil {
		return m, err
	}
	if m.Planted, err = r.strSlice(); err != nil {
		return m, err
	}
	if len(r.b) != 0 {
		return m, fmt.Errorf("trailing %d bytes", len(r.b))
	}
	return m, nil
}
