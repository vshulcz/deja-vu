package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vshulcz/deja-vu/internal/sources"
)

// Hermes reads mcp_servers.deja.enabled through PyYAML and its own boolish
// check, so far more spellings than `false` switch deja off, and `disabled:`
// is not a key it reads. Install turned an `enabled: no` back on and switched
// a `disabled: true` off; doctor saw neither. The spellings are the ones
// measured against `hermes mcp list`.
var (
	hermesOffSpellings = []string{
		"false", "False", "FALSE", "no", "No", "NO", "off", "Off", "OFF",
		"0", "0.0", "00", "0x0", "~", "null", "",
		`"false"`, `"no"`, `"off"`, `"0"`, `"y"`, `"t"`, `"nope"`, `'false'`,
		"y", "nO", "-0", "0b0", ".0", "0.0e+5",
	}
	hermesOnSpellings = []string{
		"true", "True", "yes", "on", "ON", "1", "0.5", "-1",
		`"true"`, `"TRUE"`, `"Yes"`, `"1"`, "tRUE", "0x1", ".inf",
	}
)

func TestHermesReadsEnabledTheWayHermesDoes(t *testing.T) {
	for _, v := range hermesOffSpellings {
		if hermesBoolish(v) {
			t.Errorf("enabled: %s read as on, hermes reads it as off", v)
		}
	}
	for _, v := range hermesOnSpellings {
		if !hermesBoolish(v) {
			t.Errorf("enabled: %s read as off, hermes reads it as on", v)
		}
	}
}

func hermesConfigWithSwitch(t *testing.T, line string) string {
	t.Helper()
	hermeticEnv(t)
	bin := filepath.Join(os.Getenv("HOME"), "bin", "deja")
	if _, err := installTarget("hermes", bin, false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sources.HermesHome(), "config.yaml")
	switchText(t, path, "    enabled: true\n", line)
	return path
}

func TestHermesInstallKeepsTheSwitchWhicheverWayItIsSpelt(t *testing.T) {
	for _, v := range hermesOffSpellings {
		t.Run("off "+v, func(t *testing.T) {
			line := strings.TrimRight("    enabled: "+v, " ") + " # travelling\n"
			path := hermesConfigWithSwitch(t, line)
			res, err := installTarget("hermes", "/new/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			text := fileText(t, path)
			if !strings.Contains(text, strings.TrimRight("    enabled: "+v, " ")+"\n") {
				t.Errorf("the switch was not kept as written:\n%s", text)
			}
			if !strings.Contains(text, "/new/deja") {
				t.Errorf("the entry was not updated:\n%s", text)
			}
			if !strings.Contains(res.Note, "switched off") {
				t.Errorf("the reader is not told the entry is off: %q", res.Note)
			}
			if note := clientMCPEntryOff("hermes"); !strings.Contains(note, "mcp_servers.deja.enabled") {
				t.Errorf("doctor does not report the switch: %q", note)
			}
		})
	}
	for _, line := range []string{
		"    enabled: True\n", "    enabled: yes\n", "    enabled: \"1\"\n",
		// goose's spelling, which hermes does not read.
		"    enabled: true\n    disabled: true\n",
		"    disabled: true\n",
	} {
		t.Run("on "+strings.TrimSpace(line), func(t *testing.T) {
			path := hermesConfigWithSwitch(t, line)
			if note := clientMCPEntryOff("hermes"); note != "" {
				t.Errorf("doctor reports an entry hermes runs as off: %q", note)
			}
			res, err := installTarget("hermes", "/new/deja", false)
			if err != nil {
				t.Fatal(err)
			}
			if text := fileText(t, path); !strings.Contains(text, "    enabled: true\n") {
				t.Errorf("an entry that was on was not written on:\n%s", text)
			}
			if strings.Contains(res.Note, "switched off") {
				t.Errorf("an entry that is on was reported as off: %q", res.Note)
			}
		})
	}
}
