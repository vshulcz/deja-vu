package sources

import "strings"

// piEditKeys are the names omp's and gjc's replace mode give the two sides of
// an edit, mapped onto pi's own.
var piEditKeys = map[string]string{
	"old_string": "oldText", "new_string": "newText",
	"old_text": "oldText", "new_text": "newText",
}

// piEditModes puts an edit made in omp's or gjc's other modes into pi's own
// shape, {path, edits:[{oldText, newText}]}, which the dialect reads (#4524).
// omp's replace takes {path, old_string, new_string} or an edits list of
// those, gjc's {path, edits:[{old_text, new_text}]}; both patch modes take
// {path, edits:[{op, diff}]}, where an update's diff is hunks of " ", "-" and
// "+" lines under "@@" and a create's is the whole file. The call is copied,
// not changed.
func piEditModes(args map[string]any) map[string]any {
	out := make(map[string]any, len(args))
	for k, v := range args {
		if pk, ok := piEditKeys[k]; ok {
			k = pk
		}
		out[k] = v
	}
	edits, ok := args["edits"].([]any)
	if !ok {
		return out
	}
	var folded []any
	for _, e := range edits {
		em, ok := e.(map[string]any)
		if !ok {
			continue
		}
		if diff, ok := em["diff"].(string); ok {
			folded = append(folded, piPatchEdits(str(em["op"]), diff)...)
			continue
		}
		pe := make(map[string]any, len(em))
		for k, v := range em {
			if pk, ok := piEditKeys[k]; ok {
				k = pk
			}
			pe[k] = v
		}
		folded = append(folded, pe)
	}
	out["edits"] = folded
	return out
}

// piPatchEdits is one patch-mode entry as pi edits: a hunk's removed lines
// the replaced side, its added lines the written one, and a created file's
// diff written whole. A delete writes nothing.
func piPatchEdits(op, diff string) []any {
	if op == "create" {
		return []any{map[string]any{"newText": diff}}
	}
	if op != "update" {
		return nil
	}
	var out []any
	var removed, added []string
	flush := func() {
		if len(removed)+len(added) > 0 {
			out = append(out, map[string]any{"oldText": strings.Join(removed, "\n"), "newText": strings.Join(added, "\n")})
		}
		removed, added = nil, nil
	}
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "@@"):
			flush()
		case strings.HasPrefix(l, "---"), strings.HasPrefix(l, "+++"):
		case strings.HasPrefix(l, "-"):
			removed = append(removed, l[1:])
		case strings.HasPrefix(l, "+"):
			added = append(added, l[1:])
		}
	}
	flush()
	return out
}
