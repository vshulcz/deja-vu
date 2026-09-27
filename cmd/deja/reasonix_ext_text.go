package main

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
)

// Reading the text Reasonix hands the extension: what the person typed in a
// composed turn, deja's own blocks in a folded one, and the output Reasonix
// will cut down after tool.after.

// rxLooksLikeCILog mirrors Reasonix's looksLikeCILog, the test for the output
// it summarizes.
func rxLooksLikeCILog(body string) bool {
	if rxTeamcityLine.MatchString(body) || strings.Contains(body, "TeamCity") || strings.Contains(body, "BUILD FAILED") {
		return true
	}
	return strings.Count(body, "FAIL") >= 3 && len(body) > 8<<10
}

var rxTeamcityLine = regexp.MustCompile(`(?m)^##teamcity\[.*\]$`)

// rxOneLine folds a recall block onto one line, tags included.
func rxOneLine(block string) string {
	var kept []string
	for _, ln := range strings.Split(block, "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			kept = append(kept, ln)
		}
	}
	return strings.Join(kept, " ")
}

// withoutOwnRecall is a folded message with the blocks deja appended to it
// taken out. The digest reads a message holding a recall block as deja's own
// output and drops all of it, and in these turns the rest is what the person
// typed. Only closed pairs go: a turn that quotes an opening tag keeps it.
func withoutOwnRecall(raw json.RawMessage) json.RawMessage {
	// The tag may arrive escaped (\u003c), so the cheap test is the name.
	if !bytes.Contains(raw, []byte("deja-recall")) {
		return raw
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return raw
	}
	var content string
	if json.Unmarshal(m["content"], &content) != nil {
		return raw
	}
	for {
		start := strings.Index(content, "<deja-recall>")
		if start < 0 {
			break
		}
		end := strings.Index(content[start:], "</deja-recall>")
		if end < 0 {
			break
		}
		content = content[:start] + content[start+end+len("</deja-recall>"):]
	}
	b, _ := json.Marshal(strings.TrimSpace(content))
	m["content"] = b
	out, err := json.Marshal(m)
	if err != nil {
		return raw
	}
	return out
}

// rxTypedText is what the person typed in the text input.receive carries, or
// "" for a turn the host wrote itself. The host composes a turn
// before the intercept: its own blocks around the text (the tags agent
// preview.go lists, plus its memory recall), the plan-mode marker, and ahead
// of it "Referenced context:" with the bodies of @-referenced files. A goal
// round and the message after a plan approval are the host's own turns, and
// get no recall and no digest.
func rxTypedText(text string) string {
	if strings.Contains(text, "<goal-round>") || strings.HasPrefix(strings.TrimSpace(text), rxPlanApproved) {
		return ""
	}
	for _, tag := range rxHostBlockTags {
		text = rxStripBlocks(text, "<"+tag, "</"+tag+">")
	}
	text = strings.TrimSpace(text)
	for strings.HasPrefix(text, "[Plan mode") {
		end := strings.Index(text, "]")
		if end < 0 {
			break
		}
		text = strings.TrimSpace(text[end+1:])
	}
	return rxWithoutReferencedContext(text)
}

// rxPlanApproved opens the turn Reasonix sends itself after a plan is
// approved (internal/control/controller.go).
const rxPlanApproved = "Plan approved — plan mode is off."

var rxHostBlockTags = []string{
	"response-language", "reasoning-language", "memory-update", "background-jobs",
	"active-goal", "autoresearch-runtime", "hook-context", "capability-route",
	"interrupted-turn-recovery", "execution-policy", "memory-recall", "goal-recovery",
}

// rxStripBlocks drops each closed block that opens with open (a tag name
// that may carry attributes) and ends with close.
func rxStripBlocks(text, open, close string) string {
	for from := 0; ; {
		i := strings.Index(text[from:], open)
		if i < 0 {
			return text
		}
		i += from
		if next := i + len(open); next < len(text) && text[next] != '>' && text[next] != ' ' && text[next] != '\n' {
			from = next
			continue
		}
		j := strings.Index(text[i:], close)
		if j < 0 {
			return text
		}
		text = text[:i] + text[i+j+len(close):]
		from = i
	}
}

// rxWithoutReferencedContext is Reasonix's StripReferencedContextPrefix
// (internal/control/input.go): past the preamble and the <file>, <dir>,
// <resource> and <image> blocks is what the person typed.
func rxWithoutReferencedContext(text string) string {
	const preamble = "Referenced context:"
	s := strings.TrimSpace(text)
	if !strings.HasPrefix(s, preamble) {
		return s
	}
	s = strings.TrimSpace(s[len(preamble):])
	for {
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "<file ") && !strings.HasPrefix(s, "<dir ") &&
			!strings.HasPrefix(s, "<resource ") && !strings.HasPrefix(s, "<image ") {
			return s
		}
		name := s[1:strings.IndexByte(s, ' ')]
		end := strings.Index(s, "</"+name+">")
		if end < 0 {
			return s
		}
		s = s[end+len("</"+name+">"):]
	}
}
