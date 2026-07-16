package sources

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

var (
	antigravityRequestOpenRE  = regexp.MustCompile(`^\s*<USER_REQUEST>\s*`)
	antigravityRequestCloseRE = regexp.MustCompile(`\s*</USER_REQUEST>\s*$`)
)

var antigravityUserBlockREs = []*regexp.Regexp{
	regexp.MustCompile(`(?s)<ADDITIONAL_METADATA>.*?</ADDITIONAL_METADATA>`),
	regexp.MustCompile(`(?s)<USER_SETTINGS_CHANGE>.*?</USER_SETTINGS_CHANGE>`),
}

func AntigravityRoots() []string {
	if v := os.Getenv("DEJA_ANTIGRAVITY_ROOT"); v != "" {
		return []string{v}
	}
	roots, err := filepath.Glob(filepath.Join(Home(), ".gemini", "antigravity*"))
	if err != nil {
		return nil
	}
	var out []string
	for _, root := range roots {
		if fi, err := os.Stat(root); err == nil && fi.IsDir() {
			out = append(out, root)
		}
	}
	return out
}

func AntigravityTranscripts() []string {
	var out []string
	for _, root := range AntigravityRoots() {
		matches, err := filepath.Glob(filepath.Join(root, "brain", "*", ".system_generated", "logs", "transcript.jsonl"))
		if err == nil {
			out = append(out, matches...)
		}
	}
	return out
}

func LoadAntigravity() []model.Session {
	return parseFiles(AntigravityTranscripts(), ParseAntigravityFile)
}

func ParseAntigravityFile(path string) ([]model.Session, error) {
	id := antigravitySessionID(path)
	if id == "" || id == "." || id == string(filepath.Separator) {
		return nil, nil
	}
	s := model.Session{Harness: "antigravity", ID: id, Project: "-", Path: path}
	err := scanJSONLFromOffset(path, 0, func(m map[string]any) {
		role := ""
		source, _ := m["source"].(string)
		switch source {
		case "USER_EXPLICIT":
			role = "user"
		case "MODEL":
			role = "assistant"
		default:
			return
		}
		text, _ := m["content"].(string)
		if strings.TrimSpace(text) == "" {
			return
		}
		if role == "user" {
			text = cleanAntigravityUserContent(text)
		}
		if strings.TrimSpace(text) == "" {
			return
		}
		if len(text) > 64*1024 {
			text = text[:64*1024]
		}
		t, _ := time.Parse(time.RFC3339Nano, str(m["created_at"]))
		if t.IsZero() {
			t = s.Started
		}
		s.Touch(t)
		s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: t})
	})
	if len(s.Messages) == 0 {
		return nil, err
	}
	return []model.Session{s}, err
}

func antigravitySessionID(path string) string {
	return filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(path))))
}

func cleanAntigravityUserContent(text string) string {
	for _, re := range antigravityUserBlockREs {
		text = re.ReplaceAllString(text, "")
	}
	text = antigravityRequestOpenRE.ReplaceAllString(text, "")
	text = antigravityRequestCloseRE.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}
