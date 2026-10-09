package search

import (
	"bytes"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/model"
)

// A "wrote" record is a path and line hashes; show printed the hashes bare,
// which read as an unlabelled commit sha.
func TestShowLabelsTheWrittenLineHashes(t *testing.T) {
	var b bytes.Buffer
	PrintSession(&b, model.Session{Harness: "claude", Project: "payments", ID: "bg0000", Messages: []model.Message{
		{Role: "wrote", Text: "/work/payments/internal/pool/pool.go\n77eb9cb669a7491c 1a2b3c4d5e6f7081"},
	}})
	got := b.String()
	if strings.Contains(got, "77eb9cb669a7491c") {
		t.Errorf("the hashes are printed bare:\n%s", got)
	}
	if !strings.Contains(got, "/work/payments/internal/pool/pool.go\n(2 written lines, kept as hashes for `deja blame`)") {
		t.Errorf("the record is not labelled:\n%s", got)
	}
}
