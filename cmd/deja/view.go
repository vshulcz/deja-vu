package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/digest"
	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/model"
	"github.com/vshulcz/deja-vu/internal/query"
	"github.com/vshulcz/deja-vu/internal/sources"
	"github.com/vshulcz/deja-vu/internal/stats"
	"github.com/vshulcz/deja-vu/internal/usage"
)

// viewTranscripts caps how many recent sessions embed message previews; the
// rest stay browsable by metadata. viewPreviewBytes caps each preview so the
// page stays a single fast file even over a multi-gigabyte store.
const (
	viewTranscripts  = 200
	viewPreviewBytes = 6 << 10
	viewRecalls      = 100
)

type viewSession struct {
	ID      string `json:"id"`
	Harness string `json:"harness"`
	Project string `json:"project"`
	Title   string `json:"title"`
	Updated string `json:"updated"`
	Preview string `json:"preview,omitempty"`
}

type viewRecall struct {
	Time     string   `json:"time"`
	Kind     string   `json:"kind"`
	Sessions int      `json:"sessions"`
	Bytes    int      `json:"bytes"`
	Policy   string   `json:"policy,omitempty"`
	Terms    []string `json:"terms,omitempty"`
	Digest   string   `json:"digest"`
}

type viewNote struct {
	Project string   `json:"project"`
	State   string   `json:"state"`
	Title   string   `json:"title"`
	Text    string   `json:"text"`
	Tags    []string `json:"tags,omitempty"`
	At      string   `json:"at"`
}

type viewPage struct {
	GeneratedAt   string
	TotalSessions int
	Harnesses     int
	DateStart     string
	DateEnd       string
	SessionsJSON  template.JS
	RecallsJSON   template.JS
	NotesJSON     template.JS
	PreviewCount  int
}

func runView(dir string, args []string) error {
	out := ""
	openBrowser := true
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--out":
			if i+1 >= len(args) {
				return fmt.Errorf("view: --out needs a path")
			}
			i++
			out = args[i]
		case "--no-open":
			openBrowser = false
		default:
			return fmt.Errorf("view: unknown flag %q", args[i])
		}
	}
	if err := index.EnsureForSearch(dir, query.Options{All: true}, false, os.Stderr); err != nil {
		return err
	}
	if dir == "" {
		dir = index.DefaultDir()
	}
	if out == "" {
		out = dir + ".view.html"
	}
	path, err := writeViewHTML(dir, out)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "deja: view written to %s\n", path)
	if openBrowser {
		openInBrowser(path)
	}
	return nil
}

func writeViewHTML(dir, out string) (string, error) {
	abs, err := filepath.Abs(out)
	if err != nil {
		return "", err
	}
	metas, err := index.Recent(dir, 0)
	if err != nil {
		return "", err
	}
	report := stats.Build(metas, time.Now())
	page := viewPage{
		GeneratedAt:   time.Now().Format("2006-01-02 15:04"),
		TotalSessions: report.TotalSessions,
		Harnesses:     len(report.Harnesses),
	}
	if len(metas) > 0 {
		page.DateEnd = metas[0].Updated.Format("2006-01-02")
		page.DateStart = metas[len(metas)-1].Updated.Format("2006-01-02")
	}
	sessions := make([]viewSession, 0, len(metas))
	for i, s := range metas {
		v := viewSession{
			ID: s.ID, Harness: s.Harness, Project: s.Project,
			Title:   strings.TrimSpace(s.Title),
			Updated: s.Updated.Format("2006-01-02 15:04"),
		}
		if i < viewTranscripts {
			if full, ok, err := index.FindByPrefix(dir, s.ID); err == nil && ok {
				v.Preview = sessionPreview(full.Messages)
				page.PreviewCount++
			}
		}
		sessions = append(sessions, v)
	}
	recalls := make([]viewRecall, 0, viewRecalls)
	for _, sn := range usage.Snapshots(dir, viewRecalls) {
		recalls = append(recalls, viewRecall{
			Time: sn.Time.Local().Format("2006-01-02 15:04"), Kind: sn.Kind,
			Sessions: sn.Sessions, Bytes: sn.Bytes, Policy: sn.Policy,
			Terms: sn.Terms, Digest: sn.Digest,
		})
	}
	notes := make([]viewNote, 0, 16)
	for _, n := range sources.LoadPromotedNotes() {
		notes = append(notes, viewNote{
			Project: n.Project, State: n.State, Title: n.Title, Text: n.Text,
			Tags: n.Tags, At: n.At.Format("2006-01-02"),
		})
	}
	sj, err := json.Marshal(sessions)
	if err != nil {
		return "", err
	}
	rj, err := json.Marshal(recalls)
	if err != nil {
		return "", err
	}
	nj, err := json.Marshal(notes)
	if err != nil {
		return "", err
	}
	page.SessionsJSON = jsonForScript(sj)
	page.RecallsJSON = jsonForScript(rj)
	page.NotesJSON = jsonForScript(nj)
	var b strings.Builder
	if err := viewTemplate.Execute(&b, page); err != nil {
		return "", fmt.Errorf("render view: %w", err)
	}
	if err := os.WriteFile(abs, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	return abs, nil
}

// jsonForScript makes embedded JSON safe inside a <script> block.
func jsonForScript(b []byte) template.JS {
	s := string(b)
	s = strings.ReplaceAll(s, "</", "<\\/")
	return template.JS(s) // #nosec G203 -- JSON-marshalled, script-closer escaped
}

// sessionPreview flattens the first messages of a transcript into a capped
// plain-text preview. Text comes from the index, so it is already redacted.
func sessionPreview(msgs []model.Message) string {
	var b strings.Builder
	for _, m := range msgs {
		t := strings.TrimSpace(m.Text)
		if t == "" || digest.IsAgentArtifact(t) || strings.HasPrefix(t, "<local-command") ||
			strings.HasPrefix(t, "<command-") || strings.HasPrefix(t, "Caveat:") {
			continue
		}
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(t)
		b.WriteString("\n")
		if b.Len() >= viewPreviewBytes {
			break
		}
	}
	out := b.String()
	if len(out) > viewPreviewBytes {
		out = out[:viewPreviewBytes] + "…"
	}
	return out
}

func openInBrowser(path string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", path)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}
	_ = cmd.Start()
}
