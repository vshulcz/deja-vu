package main

// The stand the line-level blame demo is recorded against: a small repository
// with a real history, and one indexed session that performed one of its
// commits. Both halves are needed, because the answer is a join — git says
// which commit last wrote the line, and the session says what that commit
// replaced and what it was asked to do.
//
// Synthetic on purpose, like the search corpus: recording this against a real
// checkout would publish someone's code and someone's prompts.
//
//	go run ./scripts/demo -out /tmp/demo-home -blame
//	rm -rf /tmp/demo-home/.cache/deja
//	vhs scripts/demo/blame.tape

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The line the demo asks about, in the two shapes the repository holds it in.
const (
	blameOldLine = "\tcfg.MaxConnLifetime = 30 * time.Minute"
	blameNewLine = "\tcfg.MaxConnLifetime = 4 * time.Minute"
)

func poolSource(lifetime string) string {
	return `package pool

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// New builds the pool every service in this repository shares.
func New(dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = 24
` + lifetime + `
	cfg.HealthCheckPeriod = 30 * time.Second
	return pgxpool.NewWithConfig(context.Background(), cfg)
}
`
}

// writeBlameStand builds the repository and the session that changed it. The
// session is timestamped an hour before the commit, because attribution drops
// an edit recorded after the commit it would be credited with.
func writeBlameStand(root string, now time.Time) error {
	repo := filepath.Join(root, "work", "checkout")
	file := filepath.Join(repo, "internal", "pool", "pool.go")
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}

	first := now.AddDate(0, 0, -214)
	second := now.AddDate(0, 0, -152)

	if err := gitInit(repo, first); err != nil {
		return err
	}
	if err := writeFile(file, []byte(poolSource(blameOldLine))); err != nil {
		return err
	}
	if err := gitCommit(repo, first, "feat: one pgx pool for every service"); err != nil {
		return err
	}
	if err := writeFile(file, []byte(poolSource(blameNewLine))); err != nil {
		return err
	}
	// Short on purpose: the answer prints the subject after the sha and the
	// date, and a longer one wraps in the recording.
	if err := gitCommit(repo, second, "fix: retire pool connections before the proxy does"); err != nil {
		return err
	}

	return writeBlameSession(root, repo, second.Add(-time.Hour))
}

// writeBlameSession writes the Claude transcript that made the second commit.
// It lives under the encoded working directory, the way Claude Code writes it,
// so the session's project is the checkout rather than a made-up name.
func writeBlameSession(root, repo string, start time.Time) error {
	encoded := strings.ReplaceAll(strings.ReplaceAll(filepath.ToSlash(repo), ":", ""), "/", "-")
	path := filepath.Join(root, ".claude", "projects", encoded, "d41c8e2b.jsonl")
	file := filepath.ToSlash(filepath.Join(repo, "internal", "pool", "pool.go"))

	rows := []map[string]any{
		claudeText("user", "d41c8e2b", start,
			"every pod loses its database connections about five minutes after a deploy, and only behind the proxy"),
		claudeText("assistant", "d41c8e2b", start.Add(2*time.Minute),
			"The proxy closes an idle backend connection at 5m and pgx does not know it happened, so the next "+
				"checkout hands out a dead connection. A health check cannot see it either — it runs on the pool's "+
				"own schedule, not before the borrow. The pool has to retire connections before the proxy does, so "+
				"the lifetime goes under the proxy's idle timeout rather than over it."),
		{
			"type":      "assistant",
			"sessionId": "d41c8e2b",
			"timestamp": start.Add(3 * time.Minute).Format(time.RFC3339),
			"cwd":       repo,
			"message": map[string]any{"role": "assistant", "content": []map[string]any{{
				"type": "tool_use",
				"name": "Edit",
				"input": map[string]any{
					"file_path":  file,
					"old_string": blameOldLine,
					"new_string": blameNewLine,
				},
			}}},
		},
		claudeText("assistant", "d41c8e2b", start.Add(5*time.Minute),
			"Four minutes, so a connection is always retired with a minute to spare against the proxy's five. "+
				"Thirty was the pgx default nobody had a reason for. Left the health check alone: it was never the "+
				"thing that noticed."),
	}
	return writeLines(path, rows)
}

func claudeText(role, id string, at time.Time, text string) map[string]any {
	return map[string]any{
		"type":      role,
		"sessionId": id,
		"timestamp": at.Format(time.RFC3339),
		"message":   map[string]any{"role": role, "content": text},
	}
}

// gitInit makes the repository with an identity of its own, so recording the
// demo never puts the name on the recording machine into the frame.
func gitInit(repo string, at time.Time) error {
	if err := os.MkdirAll(repo, 0o755); err != nil {
		return err
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.name", "demo"},
		{"config", "user.email", "demo@example.com"},
	} {
		if err := git(repo, at, args...); err != nil {
			return err
		}
	}
	return nil
}

func gitCommit(repo string, at time.Time, subject string) error {
	if err := git(repo, at, "add", "-A"); err != nil {
		return err
	}
	return git(repo, at, "commit", "-q", "-m", subject)
}

func git(repo string, at time.Time, args ...string) error {
	cmd := exec.Command("git", append([]string{"-C", repo}, args...)...)
	stamp := at.Format(time.RFC3339)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_DATE="+stamp,
		"GIT_COMMITTER_DATE="+stamp,
		// The demo must not read the recording machine's git config: an
		// include, a commit template or a signing key would all reach in.
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CONFIG_SYSTEM=/dev/null",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// blameStandLine is the line the tape asks about, found rather than hardcoded:
// editing the file above would otherwise move the line and the tape would ask
// about the wrong one.
func blameStandLine(root string) (string, int, error) {
	rel := filepath.Join("internal", "pool", "pool.go")
	path := filepath.Join(root, "work", "checkout", rel)
	b, err := os.ReadFile(path)
	if err != nil {
		return "", 0, err
	}
	for i, ln := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(ln) == strings.TrimSpace(blameNewLine) {
			return filepath.ToSlash(rel), i + 1, nil
		}
	}
	return "", 0, fmt.Errorf("%s does not hold the line the demo asks about", path)
}

// blameManifest is written beside the stand so the tape does not repeat the
// path and the line number, which move whenever the file above is edited.
func blameManifest(root string) error {
	rel, line, err := blameStandLine(root)
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(map[string]any{"file": rel, "line": line}, "", "  ")
	if err != nil {
		return err
	}
	return writeFile(filepath.Join(root, "blame-target.json"), append(b, '\n'))
}
