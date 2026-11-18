package usage

import (
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/vault"
)

// RecordAccess appends a structured, content-free access audit entry. Query strings
// and memory payloads are never logged.
func RecordAccess(indexDir, surface, client, action, projectScope, decision, reason string) {
	recordEvent(indexDir, Event{
		Time:         time.Now().UTC(),
		Kind:         KindAccess,
		Surface:      surface,
		Client:       client,
		Action:       action,
		ProjectScope: projectScope,
		Decision:     decision,
		DenyReason:   reason,
	})
}

func sealSnapshotPayload(indexDir, digest string, terms []string) (sealedDigest string, keptTerms []string, ok bool) {
	if !vault.Enabled() {
		return digest, terms, true
	}
	dek, err := vault.CheckUnlocked(indexDir)
	if err != nil || len(dek) == 0 {
		return "", nil, false
	}
	enc, err := vault.SealSnapshot(dek, digest)
	clear(dek)
	if err != nil {
		return "", nil, false
	}
	return enc, nil, true
}

func unsealSnapshots(indexDir string, snaps []Snapshot) []Snapshot {
	hasSealed := false
	for _, s := range snaps {
		if strings.HasPrefix(s.Digest, vault.NoteLinePrefix) {
			hasSealed = true
			break
		}
	}
	if !hasSealed {
		return snaps
	}
	dek, err := vault.CheckUnlocked(indexDir)
	defer clear(dek)
	for i := range snaps {
		if !strings.HasPrefix(snaps[i].Digest, vault.NoteLinePrefix) {
			continue
		}
		if err != nil || len(dek) == 0 {
			snaps[i].Digest = "[encrypted — run deja unlock]"
			continue
		}
		plain, decErr := vault.OpenSnapshot(dek, snaps[i].Digest)
		if decErr != nil {
			snaps[i].Digest = "[encrypted — run deja unlock]"
			continue
		}
		snaps[i].Digest = plain
	}
	return snaps
}
