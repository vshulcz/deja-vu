package sources

import (
	"path/filepath"
	"strings"
	"testing"
)

// Each registry fixture below carries the summary its harness writes when it
// compacts, in the shape that harness writes it. The reader has to index it
// under the summary role: dropped, it is the only record of the turns the
// compaction took away; read as speech, it is words nobody said (#4801).
var compactionSummaryFixtures = []struct {
	host, fixture, marker string
}{
	{"codex", "codex/rollout-2026-07-17T09-00-00-registry-codex.jsonl", "CODEX-SUMMARY"},
	{"copilot", "copilot/7cf44517-55ca-435c-893a-3fde1973a44e/events.jsonl", "COPILOT-SUMMARY"},
	{"copilot-chat", "copilot-chat/workspaceStorage/a1b2c3d4e5f6/chatSessions/3b7e1f1b-0000-4000-8000-000000000000.jsonl", "COPILOT-CHAT-SUMMARY"},
	{"kimi", "kimi/sessions/wd_demo_0123456789ab/session_fixture01/agents/main/wire.jsonl", "KIMI-SUMMARY"},
	{"qwen", "qwen/projects/-workspace-registry-demo/chats/registry-qwen.jsonl", "QWEN-SUMMARY"},
	{"pi", "pi/session.jsonl", "PI-SUMMARY"},
	{"pi", "pi/session.jsonl", "PI-BRANCH-SUMMARY"},
	{"omp", "omp/session.jsonl", "OMP-SUMMARY"},
	{"prime", "prime/session.jsonl", "PRIME-SUMMARY"},
	{"senpi", "senpi/-workspace-senpi-demo/session.jsonl", "SENPI-SUMMARY"},
	{"gjc", "gjc/-workspace-gjc-demo/session.jsonl", "GJC-SUMMARY"},
	{"kimchi", "kimchi/--workspace-kimchi-demo--/session.jsonl", "KIMCHI-SUMMARY"},
	{"openclaw", "openclaw/agents/main/sessions/a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d.jsonl", "OPENCLAW-SUMMARY"},
	{"openclaw", "openclaw/agent/openclaw-agent.sql", "OPENCLAW-SUMMARY"},
	{"roo", "roo/tasks/1767225700000/api_conversation_history.json", "ROO-SUMMARY"},
	{"kilocode", "kilocode/tasks/registry-kilocode/api_conversation_history.json", "KILO-SUMMARY"},
	{"zcode", "zcode/cli-db.sql", "ZCODE-SUMMARY"},
	{"codewhale", "codewhale/sessions/registry-codewhale.json", "CODEWHALE-SUMMARY"},
	{"crush", "crush/crush.sql", "CRUSH-SUMMARY"},
	{"antigravity", "antigravity/brain/registry-antigravity/.system_generated/logs/transcript.jsonl", "ANTIGRAVITY-SUMMARY"},
}

func TestRegistryFixturesIndexCompactionSummaries(t *testing.T) {
	root := filepath.Join("..", "..", "fixtures", "registry")
	for _, c := range compactionSummaryFixtures {
		t.Run(c.host+"/"+c.marker, func(t *testing.T) {
			ss := parseRegistryFixture(t, c.host, filepath.Join(root, filepath.FromSlash(c.fixture)))
			var summaries, elsewhere int
			for _, s := range ss {
				for _, m := range s.Messages {
					if !strings.Contains(m.Text, c.marker) {
						continue
					}
					if m.Role == RoleSummary {
						summaries++
					} else {
						elsewhere++
					}
				}
			}
			if summaries != 1 || elsewhere != 0 {
				t.Errorf("%s: summary indexed %d times under its role and %d times under another, want once and never", c.host, summaries, elsewhere)
			}
		})
	}
}
