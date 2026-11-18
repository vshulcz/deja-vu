package main

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/redact"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const (
	auditMaxLineChunkBytes = 4 * 1024 * 1024
	auditMaxPEMBlockBytes  = 32 * 1024
)

// auditNamedSecretAllow mirrors index.SecretKindNamed so raw audit scanning
// restricts expensive passes and reporting to concrete provider/protocol kinds.
var auditNamedSecretAllow = map[string]bool{
	"aws-access-key":     true,
	"aws-secret":         true,
	"aws-key":            true,
	"openai-key":         true,
	"anthropic-key":      true,
	"google-api-key":     true,
	"github-token":       true,
	"gitlab-token":       true,
	"groq-key":           true,
	"xai-key":            true,
	"npm-token":          true,
	"slack-token":        true,
	"huggingface-token":  true,
	"stripe-key":         true,
	"telegram-bot-token": true,
	"provider-token":     true,
	"private-key":        true,
	"jwt":                true,
	"bearer-token":       true,
	"cookie":             true,
	"url-credentials":    true,
	"webhook-url":        true,
	"command-password":   true,
	"password":           true,
}

type auditFileScanTask struct {
	storeName string
	path      string
}

type auditStoreSecretCounts struct {
	files   int
	matches int
	classes map[string]int
}

func auditShouldSkipPath(p string) bool {
	if sources.IsClaudeMemDB(p) {
		return true
	}
	lower := strings.ToLower(p)
	for _, suf := range []string{".db-wal", ".db-shm", ".sqlite-wal", ".sqlite-shm"} {
		if strings.HasSuffix(lower, suf) {
			return true
		}
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".db", ".sqlite", ".sqlite3", ".vscdb", ".zst", ".zstd", ".gz", ".zip",
		".png", ".jpg", ".jpeg", ".webp", ".pdf", ".wasm", ".bin":
		return true
	default:
		return false
	}
}

func auditScanFile(path string) map[string]int {
	if auditShouldSkipPath(path) {
		return nil
	}
	f, openErr := os.Open(path)
	if openErr != nil {
		return nil
	}
	defer f.Close()

	var header [4096]byte
	n, readErr := io.ReadFull(f, header[:])
	if readErr != nil && !errors.Is(readErr, io.EOF) && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		return nil
	}
	if n == 0 || bytes.IndexByte(header[:n], 0) >= 0 {
		return nil
	}

	reader := io.MultiReader(bytes.NewReader(header[:n]), f)
	counts := map[string]int{}
	inPEM := false
	var pemBuf strings.Builder
	prevKeyLine := ""

	record := func(text string) map[string]int {
		raw := redact.ScanKinds(text, auditNamedSecretAllow)
		out := map[string]int{}
		for kind, c := range raw {
			if c > 0 && index.SecretKindNamed(kind) {
				out[kind] += c
				counts[kind] += c
			}
		}
		return out
	}

	streamErr := auditStreamChunks(reader, func(line string) {
		trimmed := strings.TrimSpace(line)
		if !inPEM && strings.Contains(line, "-----BEGIN ") &&
			strings.Contains(line, "PRIVATE KEY-----") &&
			!strings.Contains(line, "-----END ") {
			inPEM = true
			pemBuf.Reset()
			pemBuf.WriteString(line)
			pemBuf.WriteByte('\n')
			prevKeyLine = ""
			return
		}
		if inPEM {
			if pemBuf.Len()+len(line)+1 <= auditMaxPEMBlockBytes {
				pemBuf.WriteString(line)
				pemBuf.WriteByte('\n')
			}
			if strings.Contains(line, "-----END ") || pemBuf.Len() >= auditMaxPEMBlockBytes {
				record(pemBuf.String())
				pemBuf.Reset()
				inPEM = false
			}
			prevKeyLine = ""
			return
		}

		c1 := record(line)
		if prevKeyLine != "" && trimmed != "" {
			rawJoined := redact.ScanKinds(prevKeyLine+" "+trimmed, auditNamedSecretAllow)
			for kind, c2 := range rawJoined {
				if index.SecretKindNamed(kind) && c2 > c1[kind] {
					counts[kind] += c2 - c1[kind]
				}
			}
		}
		if len(c1) == 0 && strings.HasSuffix(trimmed, ":") && len(trimmed) <= 256 {
			prevKeyLine = trimmed
		} else {
			prevKeyLine = ""
		}
	})
	if streamErr != nil {
		return nil
	}
	if inPEM && pemBuf.Len() > 0 {
		record(pemBuf.String())
	}
	return counts
}

func auditStreamChunks(r io.Reader, fn func(line string)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var buf []byte
	for {
		frag, err := br.ReadSlice('\n')
		buf = append(buf, frag...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			if len(buf) > 0 {
				line := strings.TrimRight(string(buf), "\r\n")
				if line != "" {
					fn(line)
				}
				buf = buf[:0]
			}
			if errors.Is(err, io.EOF) {
				return nil
			}
			if err != nil {
				return err
			}
			continue
		}
		if len(buf) >= auditMaxLineChunkBytes {
			cut := auditChunkSplitIndex(buf)
			fn(string(buf[:cut]))
			tail := len(buf) - cut
			copy(buf, buf[cut:])
			buf = buf[:tail]
		}
	}
}

func auditChunkSplitIndex(buf []byte) int {
	lo := max(len(buf)-16*1024, 1)
	for i := len(buf) - 1; i >= lo; i-- {
		switch buf[i] {
		case '\n', ',', ';', '}', ']', ' ', '\t':
			return i + 1
		}
	}
	return len(buf)
}

func applyAuditSecretScan(rep *auditReport, storeFiles map[string][]string) {
	rep.SecretsScanned = true
	var tasks []auditFileScanTask
	for _, row := range rep.Stores {
		for _, p := range storeFiles[row.Name] {
			tasks = append(tasks, auditFileScanTask{storeName: row.Name, path: p})
		}
	}
	if len(tasks) == 0 {
		return
	}

	type scannedItem struct {
		storeName string
		path      string
		counts    map[string]int
	}
	jobs := make(chan auditFileScanTask, len(tasks))
	for _, t := range tasks {
		jobs <- t
	}
	close(jobs)

	var (
		mu      sync.Mutex
		scanned = make([]scannedItem, 0, len(tasks))
		wg      sync.WaitGroup
	)
	workers := min(len(tasks), max(runtime.GOMAXPROCS(0), 4))
	for range workers {
		wg.Go(func() {
			for task := range jobs {
				counts := auditScanFile(task.path)
				if len(counts) == 0 {
					continue
				}
				mu.Lock()
				scanned = append(scanned, scannedItem{
					storeName: task.storeName,
					path:      task.path,
					counts:    counts,
				})
				mu.Unlock()
			}
		})
	}
	wg.Wait()

	byStore := map[string]*auditStoreSecretCounts{}
	seenPaths := map[string]bool{}
	totalClasses := map[string]int{}

	for _, item := range scanned {
		sc := byStore[item.storeName]
		if sc == nil {
			sc = &auditStoreSecretCounts{classes: map[string]int{}}
			byStore[item.storeName] = sc
		}
		sc.files++
		fileMatches := 0
		for kind, n := range item.counts {
			sc.classes[kind] += n
			fileMatches += n
		}
		sc.matches += fileMatches

		if !seenPaths[item.path] {
			seenPaths[item.path] = true
			rep.SecretFiles++
			rep.SecretMatches += fileMatches
			for kind, n := range item.counts {
				totalClasses[kind] += n
			}
		}
	}

	for i := range rep.Stores {
		sc := byStore[rep.Stores[i].Name]
		if sc == nil || sc.matches == 0 {
			continue
		}
		rep.Stores[i].SecretFiles = sc.files
		rep.Stores[i].SecretMatches = sc.matches
		rep.Stores[i].CredentialClasses = maps.Clone(sc.classes)
	}
	if len(totalClasses) > 0 {
		rep.CredentialClasses = totalClasses
	}
}

func formatAuditClassSummary(classes map[string]int) string {
	type classCount struct {
		name  string
		count int
	}
	items := make([]classCount, 0, len(classes))
	for k, v := range classes {
		if v > 0 {
			items = append(items, classCount{name: k, count: v})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].count != items[j].count {
			return items[i].count > items[j].count
		}
		return items[i].name < items[j].name
	})
	parts := make([]string, 0, len(items))
	for _, it := range items {
		parts = append(parts, it.name+" ("+grouped(it.count)+")")
	}
	return strings.Join(parts, ", ")
}
