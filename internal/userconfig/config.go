// Package userconfig reads the user-owned configuration file that installer
// upgrades and uninstalls leave untouched.
package userconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// ConfigEnvVar overrides the default config.toml path when set to a non-empty path.
const ConfigEnvVar = "DEJA_CONFIG_FILE"

// EmbedEveryEnvVar overrides [embed].every / top-level embed_every when set in
// the process environment.
const EmbedEveryEnvVar = "DEJA_EMBED_EVERY"

// Config holds the user-owned settings read from ~/.config/deja/config.toml.
type Config struct {
	Embed EmbedConfig
	Hooks HooksConfig
	Index IndexConfig
}

// EmbedConfig controls embedding endpoint settings and periodic background embedding.
type EmbedConfig struct {
	Every         string
	URL           string
	Model         string
	APIKey        string
	Batch         *int
	QueryPrefix   string
	PassagePrefix string
	Off           *bool
}

// HooksConfig controls runtime hook behavior.
type HooksConfig struct {
	Recall              string
	RecallGlobal        *bool
	AutoRecallLocalOnly *bool
	NoSourceHints       *bool
	NoAutoIndex         *bool
}

// IndexConfig controls storage locations, source selection, and indexed fields.
type IndexConfig struct {
	Dir              string
	PolicyFile       string
	NotesFile        string
	IncludeSubagents string
	ToolOutput       *bool
	Paths            *bool
	Edits            *bool
	Writes           *bool
	Commands         *bool
	ExcludeProjects  []string
	ExcludeHarnesses []string
	Stores           []string
	SearchOnly       *bool
}

// EnvBinding pairs a runtime environment variable with its configured value.
type EnvBinding struct {
	Key   string
	Value string
}

// Diagnostic captures the inspection result for a user config file without
// failing open silently or exposing secrets.
type Diagnostic struct {
	Path     string
	Exists   bool
	Config   Config
	Unknown  []string
	Warnings []string
	Err      error
}

// Path returns the configured user config path (DEJA_CONFIG_FILE if set,
// otherwise <ConfigDir>/config.toml).
func Path() string {
	if p := strings.TrimSpace(os.Getenv(ConfigEnvVar)); p != "" {
		return filepath.Clean(expandHome(p))
	}
	return filepath.Join(sources.ConfigDir(), "config.toml")
}

// Load reads Path(), returning zero Config if the file is absent or invalid.
func Load() Config {
	cfg, err := LoadPath(Path())
	if err != nil {
		return Config{}
	}
	return cfg
}

// LoadPath reads and validates a config.toml file at path. A missing file
// returns zero Config and nil error.
func LoadPath(path string) (Config, error) {
	d := DiagnosePath(path)
	if !d.Exists {
		return Config{}, nil
	}
	return d.Config, d.Err
}

// Diagnose inspects the default user config file at Path().
func Diagnose() Diagnostic {
	return DiagnosePath(Path())
}

// DiagnosePath reads, parses, validates, and inspects permissions of path.
func DiagnosePath(path string) Diagnostic {
	diag := Diagnostic{Path: path}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return diag
		}
		diag.Exists = true
		diag.Err = err
		return diag
	}
	diag.Exists = true
	raw, err := os.ReadFile(path)
	if err != nil {
		diag.Err = err
		return diag
	}

	entries, err := parseTOML(path, string(raw))
	if err != nil {
		diag.Err = err
		return diag
	}

	cfg, unknown, err := populateConfig(path, entries)
	if err != nil {
		diag.Err = err
		diag.Unknown = unknown
		return diag
	}
	diag.Config = cfg
	diag.Unknown = unknown

	if runtime.GOOS != "windows" && cfg.Embed.APIKey != "" && info.Mode().Perm()&0o077 != 0 {
		diag.Warnings = append(diag.Warnings, fmt.Sprintf(
			"config permissions %04o allow group or other access while api_key is set — run `chmod 600 %s`",
			info.Mode().Perm(), path,
		))
	}
	return diag
}

// EmbedEveryDuration returns the parsed positive duration configured by
// embed.every (or top-level embed_every), or 0 when disabled or unset.
func (c Config) EmbedEveryDuration() time.Duration {
	d, err := ParseEmbedEvery(c.Embed.Every)
	if err != nil {
		return 0
	}
	return d
}

// EffectiveEmbedEvery returns the active periodic embed interval respecting
// DEJA_EMBED_EVERY in the process environment over config.toml.
func EffectiveEmbedEvery() time.Duration {
	if v, ok := os.LookupEnv(EmbedEveryEnvVar); ok {
		d, err := ParseEmbedEvery(v)
		if err != nil {
			return 0
		}
		return d
	}
	return Load().EmbedEveryDuration()
}

// ParseEmbedEvery parses an embed interval string. Empty, "off", and "0"
// disable periodic runs (0, nil). Negative or invalid values return an error.
func ParseEmbedEvery(s string) (time.Duration, error) {
	v := strings.TrimSpace(s)
	if v == "" || v == "0" || strings.EqualFold(v, "off") {
		return 0, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", v, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration %q must be positive (or \"off\")", v)
	}
	return d, nil
}

// EnvBindings returns the deterministic list of environment variables encoded
// by c, without mutating process state.
func (c Config) EnvBindings() []EnvBinding {
	var out []EnvBinding
	addStr := func(k, v string) {
		if v != "" {
			out = append(out, EnvBinding{Key: k, Value: v})
		}
	}
	addBool := func(k string, p *bool) {
		if p == nil {
			return
		}
		if *p {
			out = append(out, EnvBinding{Key: k, Value: "1"})
		} else {
			out = append(out, EnvBinding{Key: k, Value: "0"})
		}
	}
	addList := func(k string, items []string) {
		if len(items) > 0 {
			out = append(out, EnvBinding{Key: k, Value: strings.Join(items, ",")})
		}
	}

	addStr(EmbedEveryEnvVar, c.Embed.Every)
	addStr("DEJA_EMBED_URL", c.Embed.URL)
	addStr("DEJA_EMBED_MODEL", c.Embed.Model)
	addStr("DEJA_EMBED_KEY", c.Embed.APIKey)
	if c.Embed.Batch != nil {
		addStr("DEJA_EMBED_BATCH", strconv.Itoa(*c.Embed.Batch))
	}
	addStr("DEJA_EMBED_QUERY_PREFIX", c.Embed.QueryPrefix)
	addStr("DEJA_EMBED_PASSAGE_PREFIX", c.Embed.PassagePrefix)
	addBool("DEJA_EMBED_OFF", c.Embed.Off)

	addStr("DEJA_RECALL", c.Hooks.Recall)
	addBool("DEJA_RECALL_GLOBAL", c.Hooks.RecallGlobal)
	addBool("DEJA_AUTORECALL_LOCAL_ONLY", c.Hooks.AutoRecallLocalOnly)
	addBool("DEJA_NO_SOURCE_HINTS", c.Hooks.NoSourceHints)
	addBool("DEJA_NO_AUTO_INDEX", c.Hooks.NoAutoIndex)

	addStr("DEJA_INDEX_DIR", c.Index.Dir)
	addStr("DEJA_POLICY_FILE", c.Index.PolicyFile)
	addStr("DEJA_NOTES_FILE", c.Index.NotesFile)
	addStr("DEJA_INCLUDE_SUBAGENTS", c.Index.IncludeSubagents)
	addBool("DEJA_INDEX_TOOL_OUTPUT", c.Index.ToolOutput)
	addBool("DEJA_INDEX_PATHS", c.Index.Paths)
	addBool("DEJA_INDEX_EDITS", c.Index.Edits)
	addBool("DEJA_INDEX_WRITES", c.Index.Writes)
	addBool("DEJA_INDEX_COMMANDS", c.Index.Commands)
	addList("DEJA_EXCLUDE_PROJECTS", c.Index.ExcludeProjects)
	addList("DEJA_EXCLUDE_HARNESSES", c.Index.ExcludeHarnesses)
	addList("DEJA_STORES", c.Index.Stores)
	addBool("DEJA_SEARCH_ONLY", c.Index.SearchOnly)

	return out
}

var (
	appliedMu     sync.Mutex
	appliedCounts = map[string]int{}
)

// Apply loads the user config at Path() and sets any unset environment variables.
// Existing process environment variables always win (even when set empty). The
// returned restore function reverts only the variables set by this call.
func Apply() func() {
	return ApplyPath(Path())
}

// ApplyPath loads the config at path and applies unset environment bindings.
// Errors fail open so a broken config file never blocks CLI commands.
func ApplyPath(path string) func() {
	d := DiagnosePath(path)
	if !d.Exists || d.Err != nil {
		return func() {}
	}
	bindings := d.Config.EnvBindings()
	var setKeys []string

	appliedMu.Lock()
	for _, b := range bindings {
		if _, exists := os.LookupEnv(b.Key); exists {
			continue
		}
		if err := os.Setenv(b.Key, b.Value); err == nil {
			appliedCounts[b.Key]++
			setKeys = append(setKeys, b.Key)
		}
	}
	appliedMu.Unlock()

	return func() {
		if len(setKeys) == 0 {
			return
		}
		appliedMu.Lock()
		defer appliedMu.Unlock()
		for _, k := range setKeys {
			_ = os.Unsetenv(k)
			if appliedCounts[k] <= 1 {
				delete(appliedCounts, k)
			} else {
				appliedCounts[k]--
			}
		}
	}
}

// WasApplied reports whether key is currently populated from config.toml rather
// than the outer process environment.
func WasApplied(key string) bool {
	appliedMu.Lock()
	defer appliedMu.Unlock()
	return appliedCounts[key] > 0
}

func populateConfig(path string, entries []tomlEntry) (Config, []string, error) {
	var cfg Config
	var unknown []string
	var sawTopEmbedEvery, sawSectionEmbedEvery bool

	for _, e := range entries {
		full := e.key
		if e.table != "" {
			full = e.table + "." + e.key
		}
		handled, err := applyEmbedEntry(path, full, e.val, &cfg.Embed, &sawTopEmbedEvery, &sawSectionEmbedEvery)
		if err != nil {
			return Config{}, unknown, err
		}
		if handled {
			continue
		}
		handled, err = applyHooksEntry(path, full, e.val, &cfg.Hooks)
		if err != nil {
			return Config{}, unknown, err
		}
		if handled {
			continue
		}
		handled, err = applyIndexEntry(path, full, e.val, &cfg.Index)
		if err != nil {
			return Config{}, unknown, err
		}
		if !handled {
			unknown = append(unknown, full)
		}
	}
	sort.Strings(unknown)
	return cfg, unknown, nil
}

func applyEmbedEntry(path, full string, val tomlValue, cfg *EmbedConfig, sawTop, sawSec *bool) (bool, error) {
	switch full {
	case "embed_every", "embed.every":
		if full == "embed_every" {
			*sawTop = true
		} else {
			*sawSec = true
		}
		if *sawTop && *sawSec {
			return true, fmt.Errorf("%s:%d: duplicate setting %q and %q", path, val.line, "embed_every", "embed.every")
		}
		s, err := expectString(path, "embed.every", val)
		if err != nil {
			return true, err
		}
		if _, err := ParseEmbedEvery(s); err != nil {
			return true, fmt.Errorf("%s:%d: embed.every: %w", path, val.line, err)
		}
		cfg.Every = strings.TrimSpace(s)
	case "embed.url":
		s, err := expectString(path, full, val)
		if err != nil {
			return true, err
		}
		cfg.URL = strings.TrimSpace(s)
	case "embed.model":
		s, err := expectString(path, full, val)
		if err != nil {
			return true, err
		}
		cfg.Model = strings.TrimSpace(s)
	case "embed.api_key":
		s, err := expectString(path, full, val)
		if err != nil {
			return true, err
		}
		cfg.APIKey = s
	case "embed.batch":
		if val.kind != kindInt || val.num <= 0 || val.num > 1<<20 {
			return true, fmt.Errorf("%s:%d: %s must be a positive integer", path, val.line, full)
		}
		n := int(val.num)
		cfg.Batch = &n
	case "embed.query_prefix":
		s, err := expectString(path, full, val)
		if err != nil {
			return true, err
		}
		cfg.QueryPrefix = s
	case "embed.passage_prefix":
		s, err := expectString(path, full, val)
		if err != nil {
			return true, err
		}
		cfg.PassagePrefix = s
	case "embed.off":
		b, err := expectBool(path, full, val)
		if err != nil {
			return true, err
		}
		cfg.Off = &b
	default:
		return false, nil
	}
	return true, nil
}

func applyHooksEntry(path, full string, val tomlValue, cfg *HooksConfig) (bool, error) {
	switch full {
	case "hooks.recall":
		s, err := expectString(path, full, val)
		if err != nil {
			return true, err
		}
		norm := strings.ToLower(strings.TrimSpace(s))
		switch norm {
		case "safe", "aggressive", "off":
			cfg.Recall = norm
		default:
			return true, fmt.Errorf("%s:%d: %s must be \"safe\", \"aggressive\", or \"off\" (got %q)", path, val.line, full, s)
		}
	case "hooks.recall_global":
		return applyBoolField(path, full, val, &cfg.RecallGlobal)
	case "hooks.autorecall_local_only":
		return applyBoolField(path, full, val, &cfg.AutoRecallLocalOnly)
	case "hooks.no_source_hints":
		return applyBoolField(path, full, val, &cfg.NoSourceHints)
	case "hooks.no_auto_index":
		return applyBoolField(path, full, val, &cfg.NoAutoIndex)
	default:
		return false, nil
	}
	return true, nil
}

func applyIndexEntry(path, full string, val tomlValue, cfg *IndexConfig) (bool, error) {
	switch full {
	case "index.dir":
		return applyHomePathField(path, full, val, &cfg.Dir)
	case "index.policy_file":
		return applyHomePathField(path, full, val, &cfg.PolicyFile)
	case "index.notes_file":
		return applyHomePathField(path, full, val, &cfg.NotesFile)
	case "index.include_subagents":
		norm, err := parseIncludeSubagents(path, full, val)
		if err != nil {
			return true, err
		}
		cfg.IncludeSubagents = norm
	case "index.tool_output":
		return applyBoolField(path, full, val, &cfg.ToolOutput)
	case "index.paths":
		return applyBoolField(path, full, val, &cfg.Paths)
	case "index.edits":
		return applyBoolField(path, full, val, &cfg.Edits)
	case "index.writes":
		return applyBoolField(path, full, val, &cfg.Writes)
	case "index.commands":
		return applyBoolField(path, full, val, &cfg.Commands)
	case "index.exclude_projects":
		return applyStringListField(path, full, val, &cfg.ExcludeProjects)
	case "index.exclude_harnesses":
		return applyStringListField(path, full, val, &cfg.ExcludeHarnesses)
	case "index.stores":
		return applyStringListField(path, full, val, &cfg.Stores)
	case "index.search_only":
		return applyBoolField(path, full, val, &cfg.SearchOnly)
	default:
		return false, nil
	}
	return true, nil
}

func applyBoolField(path, full string, val tomlValue, dst **bool) (bool, error) {
	b, err := expectBool(path, full, val)
	if err != nil {
		return true, err
	}
	*dst = &b
	return true, nil
}

func applyHomePathField(path, full string, val tomlValue, dst *string) (bool, error) {
	s, err := expectString(path, full, val)
	if err != nil {
		return true, err
	}
	*dst = expandHome(strings.TrimSpace(s))
	return true, nil
}

func applyStringListField(path, full string, val tomlValue, dst *[]string) (bool, error) {
	items, err := expectStringList(path, full, val)
	if err != nil {
		return true, err
	}
	*dst = items
	return true, nil
}

func expectString(path, field string, v tomlValue) (string, error) {
	if v.kind != kindString {
		return "", fmt.Errorf("%s:%d: %s must be a string", path, v.line, field)
	}
	return v.str, nil
}

func expectBool(path, field string, v tomlValue) (bool, error) {
	if v.kind != kindBool {
		return false, fmt.Errorf("%s:%d: %s must be a boolean", path, v.line, field)
	}
	return v.b, nil
}

func expectStringList(path, field string, v tomlValue) ([]string, error) {
	switch v.kind {
	case kindString:
		var out []string
		for item := range strings.SplitSeq(v.str, ",") {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	case kindStringArray:
		var out []string
		for _, item := range v.arr {
			if s := strings.TrimSpace(item); s != "" {
				out = append(out, s)
			}
		}
		return out, nil
	case kindBool, kindInt:
		return nil, fmt.Errorf("%s:%d: %s must be an array of strings", path, v.line, field)
	}
	return nil, fmt.Errorf("%s:%d: %s must be an array of strings", path, v.line, field)
}

func parseIncludeSubagents(path, field string, v tomlValue) (string, error) {
	switch v.kind {
	case kindBool:
		if v.b {
			return "1", nil
		}
		return "0", nil
	case kindString:
		switch strings.ToLower(strings.TrimSpace(v.str)) {
		case "0", "off", "none", "false":
			return "0", nil
		case "1", "whole", "full", "true":
			return "1", nil
		case "summary", "default":
			return "summary", nil
		default:
			return "", fmt.Errorf("%s:%d: %s must be \"off\", \"summary\", or \"whole\" (got %q)", path, v.line, field, v.str)
		}
	case kindInt, kindStringArray:
		return "", fmt.Errorf("%s:%d: %s must be a string or boolean", path, v.line, field)
	}
	return "", fmt.Errorf("%s:%d: %s must be a string or boolean", path, v.line, field)
}

func expandHome(p string) string {
	home := sources.Home()
	if home == "" || p == "" {
		return p
	}
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(home, p[2:])
	}
	return p
}
