package sources

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

func TestWriteBackRefusesWithItsReason(t *testing.T) {
	for harness, want := range map[string]string{
		"reasonix": "system prompt",
		"zcode":    "CLI database",
		"trae":     "TRAE CLI",
		"muse":     "Muse",
	} {
		if CanWriteBack(harness) {
			t.Errorf("%s: written back, want a refusal", harness)
			continue
		}
		s := model.Session{Harness: harness, ID: "s1", Path: filepath.Join(t.TempDir(), "s1.jsonl"),
			Messages: []model.Message{{Role: "user", Text: "hi"}}}
		_, err := RenderWriteBack(s)
		var r *WriteBackRefusal
		if !errors.As(err, &r) || !strings.Contains(r.Reason, want) {
			t.Errorf("%s: got %v, want a reason naming %q", harness, err, want)
		}
	}
}
