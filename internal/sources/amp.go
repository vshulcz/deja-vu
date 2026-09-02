package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/vshulcz/deja-vu/internal/model"
)

// AmpRoot returns Amp's thread store root, overridable by deja without
// changing Amp's own environment.
func AmpRoot() string {
	dataHome := filepath.Join(Home(), ".local", "share", "amp")
	if runtime.GOOS == "linux" {
		if p := os.Getenv("XDG_DATA_HOME"); p != "" {
			dataHome = filepath.Join(p, "amp")
		}
	}
	return EnvPath("DEJA_AMP_ROOT", filepath.Join(dataHome, "threads"))
}

// AmpThreadFiles lists Amp's one-thread-per-JSON files.
func AmpThreadFiles() []string {
	return walkFiles(AmpRoot(), func(p string) bool {
		return strings.HasSuffix(p, ".json")
	})
}

// LoadAmp loads every readable Amp thread.
func LoadAmp() []model.Session {
	return parseFiles(AmpThreadFiles(), ParseAmpFile)
}

type ampThread struct {
	ID      string      `json:"id"`
	Title   string      `json:"title"`
	Created json.Number `json:"created"`
	Env     struct {
		Initial struct {
			Trees []struct {
				URI string `json:"uri"`
			} `json:"trees"`
		} `json:"initial"`
	} `json:"env"`
	Messages []struct {
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"messages"`
}

// ParseAmpFile parses one Amp thread. Amp does not record per-message
// timestamps, so every parsed message carries the thread's created timestamp.
func ParseAmpFile(path string) ([]model.Session, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var thread ampThread
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&thread); err != nil {
		return nil, fmt.Errorf("decode Amp thread %s: %w", path, err)
	}
	var trailing json.RawMessage
	if err := dec.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("decode Amp thread %s: trailing data", path)
		}
		return nil, fmt.Errorf("decode Amp thread %s: trailing data: %w", path, err)
	}
	if thread.ID == "" {
		return nil, fmt.Errorf("decode Amp thread %s: missing id", path)
	}
	created := parseTimeAny(thread.Created)
	if created.IsZero() {
		return nil, fmt.Errorf("decode Amp thread %s: invalid created timestamp", path)
	}

	project := thread.Title
	if len(thread.Env.Initial.Trees) > 0 {
		project = ampProject(thread.Env.Initial.Trees[0].URI, project)
	}
	session := model.Session{
		ID:      thread.ID,
		Harness: "amp",
		Project: projectName(project),
		Path:    path,
		Title:   thread.Title,
		Started: created,
		Updated: created,
	}
	for _, item := range thread.Messages {
		if item.Role != "user" && item.Role != "assistant" {
			continue
		}
		var texts []string
		for _, block := range item.Content {
			if block.Type == "text" && block.Text != "" {
				texts = append(texts, block.Text)
			}
		}
		if len(texts) == 0 {
			continue
		}
		session.Messages = append(session.Messages, model.Message{
			Role: item.Role,
			Text: strings.Join(texts, "\n"),
			Time: created,
		})
	}
	if len(session.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{session}, nil
}

func ampProject(uri, fallback string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return fallback
	}
	return u.Path
}
