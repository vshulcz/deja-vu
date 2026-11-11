package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/index"
	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/sources"
)

const defaultClaudeCleanupDays = 30

type auditStoreRow struct {
	Name  string `json:"name"`
	Files int    `json:"files"`
	Bytes int64  `json:"bytes"`
}

type auditClaudeRetention struct {
	CleanupPeriodDays int  `json:"cleanup_period_days"`
	Explicit          bool `json:"explicit"`
	TotalFiles        int  `json:"total_files"`
	ExpiringIn7Days   int  `json:"expiring_in_7_days"`
	PastPeriod        int  `json:"past_period"`
	PreservedDeleted  int  `json:"preserved_deleted"`
}

type auditReport struct {
	SchemaVersion      int                  `json:"schema_version"`
	Stores             []auditStoreRow      `json:"stores"`
	TotalFiles         int                  `json:"total_files"`
	TotalBytes         int64                `json:"total_bytes"`
	ClaudeRetention    auditClaudeRetention `json:"claude_retention"`
	ClaudeMemDBPresent bool                 `json:"claude_mem_db_present"`
	ClaudeMemDBBytes   int64                `json:"claude_mem_db_bytes,omitempty"`
	IndexedSessions    int                  `json:"indexed_sessions"`
	RecoverableFiles   int                  `json:"recoverable_files"`
	WiredTargets       []string             `json:"wired_targets"`
	NextCommand        string               `json:"next_command,omitempty"`
}

var auditNow = time.Now

func runAudit(dir string, args []string, stdout io.Writer) error {
	jsonOut := false
	for _, a := range args {
		switch a {
		case "--json":
			jsonOut = true
		default:
			if strings.HasPrefix(a, "-") {
				return fmt.Errorf("audit: unknown flag %q", a)
			}
			return fmt.Errorf("audit takes no positional arguments — got %q", a)
		}
	}

	rep := collectAudit(dir, auditNow())
	rep.SchemaVersion = jsonout.Version
	if jsonOut {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(rep)
	}
	printAuditReport(stdout, rep)
	return nil
}

func collectAudit(dir string, now time.Time) auditReport {
	var rep auditReport
	rep.Stores = []auditStoreRow{}
	rep.WiredTargets = []string{}

	type storeSpec struct {
		name  string
		files func() []string
	}
	specs := []storeSpec{
		{"claude", sources.ClaudeFiles},
		{"codex", sources.CodexFiles},
		{"cursor", cursorReadFiles},
		{"opencode", func() []string { return presentFiles(sources.OpencodeDB()) }},
		{"gemini", sources.GeminiChatFiles},
		{"antigravity", sources.AntigravityTranscripts},
		{"openclaw", sources.OpenClawStoreFiles},
		{"hermes", sources.HermesSessionFiles},
		{"copilot", sources.CopilotSessionFiles},
		{"copilot-chat", sources.CopilotChatSessionFiles},
		{"cline", sources.ClineSessionFiles},
		{"roo", sources.RooTaskFiles},
		{"kilocode", sources.KiloSessionFiles},
		{"aider", sources.AiderFiles},
		{"goose", sources.GooseSessionFiles},
		{"qwen", sources.QwenSessionFiles},
		{"kimi", sources.KimiSessionFiles},
		{"zed", func() []string { return presentFiles(sources.ZedDB()) }},
		{"windsurf", sources.WindsurfSessionFiles},
		{"kiro", sources.KiroSessionFiles},
		{"continue", sources.ContinueSessionFiles},
		{"amp", sources.AmpThreadFiles},
		{"pi", sources.PiSessionFiles},
		{"omp", sources.OmpSessionFiles},
		{"crush", sources.CrushDBs},
		{"deepseek", sources.DeepSeekSessionFiles},
		{"grok", grokReadFiles},
		{"trae", sources.TraeFiles},
		{"codebuddy", sources.CodeBuddySessionFiles},
		{"cherrystudio", sources.CherryStudioSessionFiles},
		{"junie", sources.JunieSessionFiles},
		{"jetbrains", sources.JetBrainsSessionFiles},
		{"devin", sources.DevinFiles},
		{"senpi", sources.SenpiSessionFiles},
		{"kimchi", sources.KimchiSessionFiles},
		{"commandcode", sources.CommandCodeSessionFiles},
		{"zcode", sources.ZCodeSessionFiles},
		{"gjc", sources.GjcSessionFiles},
		{"prime", sources.PrimeSessionFiles},
		{"codewhale", sources.CodeWhaleSessionFiles},
		{"reasonix", sources.ReasonixSessionFiles},
		{"muse", sources.MuseSessionFiles},
		{"mcode", sources.McodeSessionFiles},
	}

	skipStore := sources.ExcludedHarnesses()
	for _, sp := range specs {
		if sources.StoreSilenced(sp.name) || skipStore[sp.name] {
			continue
		}
		files := presentFiles(sp.files()...)
		if len(files) == 0 {
			continue
		}
		sz := filesSize(files)
		if sp.name == "opencode" {
			if s, _, err := sources.OpencodeCounts(); err == nil && s == 0 {
				continue
			}
		}
		rep.Stores = append(rep.Stores, auditStoreRow{
			Name:  sp.name,
			Files: len(files),
			Bytes: sz,
		})
		rep.TotalFiles += len(files)
		rep.TotalBytes += sz
	}
	sort.Slice(rep.Stores, func(i, j int) bool {
		if rep.Stores[i].Bytes != rep.Stores[j].Bytes {
			return rep.Stores[i].Bytes > rep.Stores[j].Bytes
		}
		return rep.Stores[i].Name < rep.Stores[j].Name
	})

	rep.ClaudeRetention = collectClaudeRetention(dir, now, skipStore["claude"] || sources.StoreSilenced("claude"))

	if db := sources.ClaudeMemDBPath(); db != "" {
		if fi, err := os.Stat(db); err == nil && !fi.IsDir() && fi.Size() > 0 {
			rep.ClaudeMemDBPresent = true
			rep.ClaudeMemDBBytes = fi.Size()
		}
	}

	if n, err := index.SessionCount(dir); err == nil && n > 0 {
		rep.IndexedSessions = n
		seenPaths := map[string]bool{}
		_ = index.EachRecordOfRole(dir, index.RoleEdit, func(_ index.SessionMeta, r index.Record) {
			if recorded, _, ok := strings.Cut(r.Text, "\n"); ok && recorded != "" {
				seenPaths[recorded] = true
			}
		})
		rep.RecoverableFiles = len(seenPaths)
	}

	st := readWiringState()
	if len(st.Targets) > 0 {
		rep.WiredTargets = append(rep.WiredTargets, st.Targets...)
		sort.Strings(rep.WiredTargets)
	} else {
		rep.NextCommand = "npx @vshulcz/deja-vu install --auto"
	}

	return rep
}

func collectClaudeRetention(dir string, now time.Time, skipped bool) auditClaudeRetention {
	days, explicit := readClaudeCleanupPeriodDays()
	ret := auditClaudeRetention{
		CleanupPeriodDays: days,
		Explicit:          explicit,
	}
	if skipped {
		return ret
	}
	files := presentFiles(sources.ClaudeFiles()...)
	warnAge := time.Duration(max(days-7, 0)) * 24 * time.Hour
	pastAge := time.Duration(days) * 24 * time.Hour
	for _, p := range files {
		// Skip claude-mem SQLite DB when counting JSONL transcript ages.
		if sources.IsClaudeMemDB(p) {
			continue
		}
		ret.TotalFiles++
		if days <= 0 {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		age := now.Sub(fi.ModTime())
		if age >= pastAge {
			ret.PastPeriod++
		} else if age >= warnAge {
			ret.ExpiringIn7Days++
		}
	}
	if kept, err := index.KeptSince(dir, time.Unix(1, 0), now.Add(time.Second)); err == nil {
		for _, k := range kept {
			if k.Harness == "claude" {
				ret.PreservedDeleted++
			}
		}
	}
	return ret
}

func readClaudeCleanupPeriodDays() (int, bool) {
	settingsPath := filepath.Join(sources.ClaudeConfigDir(), "settings.json")
	b, err := os.ReadFile(settingsPath)
	if err != nil {
		return defaultClaudeCleanupDays, false
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return defaultClaudeCleanupDays, false
	}
	val, ok := raw["cleanupPeriodDays"]
	if !ok {
		return defaultClaudeCleanupDays, false
	}
	var n int
	if err := json.Unmarshal(val, &n); err != nil || n < 0 {
		return defaultClaudeCleanupDays, false
	}
	return n, true
}

func printAuditReport(w io.Writer, rep auditReport) {
	if len(rep.Stores) == 0 {
		fmt.Fprintln(w, "stores:   none detected on this machine")
	} else {
		names := make([]string, 0, min(len(rep.Stores), 6))
		for i, s := range rep.Stores {
			if i >= 6 {
				break
			}
			names = append(names, fmt.Sprintf("%s (%d)", s.Name, s.Files))
		}
		extra := ""
		if len(rep.Stores) > 6 {
			extra = fmt.Sprintf(" +%d more", len(rep.Stores)-6)
		}
		fmt.Fprintf(w, "stores:   %d agent store%s, %s transcript file%s (%s) — %s%s\n",
			len(rep.Stores), pluralS(len(rep.Stores)),
			grouped(rep.TotalFiles), pluralS(rep.TotalFiles),
			humanBytes(rep.TotalBytes),
			strings.Join(names, ", "), extra)
	}

	cr := rep.ClaudeRetention
	if cr.TotalFiles > 0 || cr.PreservedDeleted > 0 {
		settingNote := fmt.Sprintf("default %dd", cr.CleanupPeriodDays)
		if cr.Explicit {
			if cr.CleanupPeriodDays == 0 {
				settingNote = "cleanupPeriodDays=0 (disabled)"
			} else {
				settingNote = fmt.Sprintf("cleanupPeriodDays=%d", cr.CleanupPeriodDays)
			}
		}
		atRisk := cr.PastPeriod + cr.ExpiringIn7Days
		switch {
		case cr.CleanupPeriodDays == 0:
			fmt.Fprintf(w, "claude:   %s transcript%s on disk (%s)\n",
				grouped(cr.TotalFiles), pluralS(cr.TotalFiles), settingNote)
		case atRisk > 0:
			fmt.Fprintf(w, "claude:   %d of %s transcript%s at deletion risk (%d past %dd, %d within 7d; %s)\n",
				atRisk, grouped(cr.TotalFiles), pluralS(cr.TotalFiles),
				cr.PastPeriod, cr.CleanupPeriodDays, cr.ExpiringIn7Days, settingNote)
		default:
			fmt.Fprintf(w, "claude:   %s transcript%s on disk, 0 expiring within 7d (%s)\n",
				grouped(cr.TotalFiles), pluralS(cr.TotalFiles), settingNote)
		}
		if cr.PreservedDeleted > 0 {
			fmt.Fprintf(w, "kept:     %s deleted Claude session%s still searchable in deja\n",
				grouped(cr.PreservedDeleted), pluralS(cr.PreservedDeleted))
		}
	}

	if rep.ClaudeMemDBPresent {
		fmt.Fprintf(w, "migrate:  claude-mem.db detected (%s) — deja reads it directly without sign-in\n",
			humanBytes(rep.ClaudeMemDBBytes))
	}

	if rep.IndexedSessions > 0 {
		if rep.RecoverableFiles > 0 {
			fmt.Fprintf(w, "index:    %s session%s indexed; %s file path%s recoverable via `deja restore <path>`\n",
				grouped(rep.IndexedSessions), pluralS(rep.IndexedSessions),
				grouped(rep.RecoverableFiles), pluralS(rep.RecoverableFiles))
		} else {
			fmt.Fprintf(w, "index:    %s session%s indexed\n",
				grouped(rep.IndexedSessions), pluralS(rep.IndexedSessions))
		}
	} else if rep.TotalFiles > 0 {
		fmt.Fprintln(w, "index:    not built yet (history on disk is unindexed)")
	}

	if len(rep.WiredTargets) > 0 {
		fmt.Fprintf(w, "wiring:   %d target%s wired (%s)\n",
			len(rep.WiredTargets), pluralS(len(rep.WiredTargets)),
			strings.Join(rep.WiredTargets, ", "))
	} else {
		fmt.Fprintln(w, "wiring:   not wired")
		if rep.NextCommand != "" {
			fmt.Fprintf(w, "next:     %s\n", rep.NextCommand)
		}
	}
}
