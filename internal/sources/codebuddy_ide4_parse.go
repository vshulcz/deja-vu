package sources

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

// The reading of a 4.x message follows the 5.x IDE's own converter, which
// turns these conversations into CodeBuddy Code transcripts
// (convertCraftConversation in the genie extension): a conversation read here
// before the upgrade and the transcript the upgrade writes from it index the
// same turns.

// codeBuddyIDE4Index is a conversation's index.json.
type codeBuddyIDE4Index struct {
	Messages []struct {
		ID string `json:"id"`
	} `json:"messages"`
	Requests []struct {
		ID        string   `json:"id"`
		Messages  []string `json:"messages"`
		StartedAt any      `json:"startedAt"`
		Usage     *struct {
			InputTokens  int64 `json:"inputTokens"`
			OutputTokens int64 `json:"outputTokens"`
			CacheTokens  int64 `json:"cacheTokens"`
		} `json:"usage"`
	} `json:"requests"`
}

// order is the conversation's messages in the order the converter takes them
// (orderedCraftMessageIds): each request's messages first, then any the
// message list holds that no request does, each id once.
func (x codeBuddyIDE4Index) order() []string {
	seen := map[string]bool{}
	var out []string
	push := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, r := range x.Requests {
		for _, id := range r.Messages {
			push(id)
		}
	}
	for _, m := range x.Messages {
		push(m.ID)
	}
	return out
}

// codeBuddyIDE4Message is one messages/<id>.json. message and extra are JSON
// strings in every store seen; an object is read the same.
type codeBuddyIDE4Message struct {
	Role      string          `json:"role"`
	Message   json.RawMessage `json:"message"`
	Extra     json.RawMessage `json:"extra"`
	CreatedAt any             `json:"createdAt"`
}

// codeBuddyIDE4Object decodes a field that is either a JSON object or a string
// holding one.
func codeBuddyIDE4Object(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		raw = json.RawMessage(strings.TrimPrefix(s, "\ufeff"))
	}
	var m map[string]any
	if json.Unmarshal(raw, &m) != nil {
		return nil
	}
	return m
}

// ParseCodeBuddyIDE4File reads one 4.x conversation from its index.json,
// unless the 5.x IDE converted it into a transcript, which then stands in for
// it.
func ParseCodeBuddyIDE4File(path string) ([]model.Session, error) {
	// The converter keeps the conversation's id, so the transcript is read
	// under the same session key and the conversation is listed once.
	if CodeBuddyIDE4ConvertedTranscript(path) != "" {
		return nil, nil
	}
	return parseCodeBuddyIDE4(path)
}

func parseCodeBuddyIDE4(path string) ([]model.Session, error) {
	id, ok := codeBuddyIDE4Parts(path)
	if !ok {
		return nil, nil
	}
	b, err := codeBuddyIDE4ReadFile(path)
	if err != nil {
		return nil, err
	}
	var idx codeBuddyIDE4Index
	if err := json.Unmarshal(b, &idx); err != nil {
		return nil, err
	}
	s := model.Session{Harness: "codebuddy", ID: id, Path: path}
	entry, listed := codeBuddyIDE4WorkspaceEntry(path)
	if listed && codeBuddyIDE4TeamMember(entry) {
		// A team member's own conversation: the lead's conversation holds
		// what it was sent and what it answered, and the IDE does not list
		// it as a conversation of its own.
		s.Kind = model.KindSubagent
	}
	// A message with no time of its own takes its request's start: 373 of the
	// conversations on one store wrote no createdAt at all.
	started := map[string]time.Time{}
	for _, r := range idx.Requests {
		t := parseTimeAny(r.StartedAt)
		for _, m := range r.Messages {
			if _, ok := started[m]; !ok {
				started[m] = t
			}
		}
		if r.Usage != nil {
			s.AddTokens(max(0, r.Usage.InputTokens-r.Usage.CacheTokens), r.Usage.OutputTokens, 0, r.Usage.CacheTokens)
		}
	}
	r := &codeBuddyIDE4Reader{s: &s, exits: commandExits{}, path: path}
	msgs := filepath.Join(filepath.Dir(path), "messages")
	// A message file the index lists and the store no longer holds is
	// reported once the whole conversation is read: the IDE deletes the
	// files of the turns a compaction summary replaced and keeps their ids
	// in the index, and that is not a record deja failed to read.
	var missing []string
	lastSummary := -1
	for i, mid := range idx.order() {
		if !codeBuddyIDE4SafeID(mid) {
			diagUnusableRecord(path, mid, "message id is not a file name")
			continue
		}
		mb, err := codeBuddyIDE4ReadFile(filepath.Join(msgs, mid+".json"))
		if err != nil {
			// Listed and not written yet, or written by a crash: the rest of
			// the conversation is still the conversation.
			if os.IsNotExist(err) {
				missing = append(missing, mid)
				continue
			}
			diagUnusableRecord(path, mid, "message file missing or unreadable")
			continue
		}
		var m codeBuddyIDE4Message
		if json.Unmarshal(mb, &m) != nil {
			diagUnusableRecord(path, mid, "message file is not JSON")
			continue
		}
		if codeBuddyIDE4Compacts(m) {
			lastSummary = i
		}
		t := parseTimeAny(m.CreatedAt)
		if t.IsZero() {
			t = started[mid]
		}
		r.message(mid, m, t)
	}
	if len(missing) > 0 {
		at := map[string]int{}
		for i, mid := range idx.order() {
			at[mid] = i
		}
		for _, mid := range missing {
			if at[mid] < lastSummary {
				continue
			}
			diagUnusableRecord(path, mid, "message file missing or unreadable")
		}
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	if listed {
		s.Touch(parseTimeAny(entry.CreatedAt))
		s.Touch(parseTimeAny(entry.LastMessageAt))
		s.Title = codeBuddyIDE4Title(entry.Name)
	}
	// A conversation with no time anywhere — no createdAt on its messages,
	// no startedAt on its requests, no entry in the workspace index — took
	// the zero time, so it sorted as "-" and fell outside every --since
	// window. Its index file's modification time is the nearest the store
	// keeps, as Cline's task history falls back to its file (cline.go).
	if s.Started.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			s.Touch(fi.ModTime())
		}
	}
	if r.cwd != "" {
		s.Project = projectName(r.cwd)
	} else {
		s.Project = "-"
	}
	resolveHomeOrEmptyProject(&s)
	return []model.Session{s}, nil
}

// codeBuddyIDE4Compacts reports a message that stands in for the turns before
// it: the summary the IDE writes when a conversation runs out of context
// (isMaxTokenLimitSummary, the converter's compaction boundary; isSummary),
// or the prompt that carries one to the model (isCompacted, its text a
// <cb_summary> of the turns so far). The IDE deletes the files of the turns
// such a message replaced and keeps their ids in the index.
func codeBuddyIDE4Compacts(m codeBuddyIDE4Message) bool {
	extra := codeBuddyIDE4Object(m.Extra)
	return extra["isMaxTokenLimitSummary"] == true || extra["isSummary"] == true || extra["isCompacted"] == true
}

// codeBuddyIDE4TeamMember is isCraftTeamMemberListing: a listing of type
// team-member, or a title "[Team] <member>@<team>" from before the type.
func codeBuddyIDE4TeamMember(e codeBuddyIDE4Entry) bool {
	if e.Type != "" {
		return e.Type == "team-member"
	}
	return codeBuddyIDE4TeamTitle.MatchString(strings.TrimSpace(e.Name))
}

var codeBuddyIDE4TeamTitle = regexp.MustCompile(`^\[Team\]\s+(\S.*?)@(\S+)$`)

// codeBuddyIDE4Title is the listing's name as the converter keeps it: control
// characters to spaces, runs of space to one, the first line.
func codeBuddyIDE4Title(name string) string {
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, name)
	return firstLineTrim(strings.Join(strings.Fields(name), " "))
}

// codeBuddyIDE4Reader accumulates one conversation.
type codeBuddyIDE4Reader struct {
	s     *model.Session
	path  string
	cwd   string
	exits commandExits
}

func (r *codeBuddyIDE4Reader) message(id string, m codeBuddyIDE4Message, t time.Time) {
	inner := codeBuddyIDE4Object(m.Message)
	if inner == nil {
		diagUnusableRecord(r.path, id, "message body is not a JSON object")
		return
	}
	extra := codeBuddyIDE4Object(m.Extra)
	parts := codeBuddyIDE4Content(inner["content"])
	role := codeBuddyIDE4Role(m.Role, inner["role"], parts, extra)
	if model, _ := extra["modelId"].(string); model != "" && role == "assistant" {
		r.s.SetModel("", model)
	}
	switch role {
	case "user":
		r.user(parts, extra, t)
	case "assistant":
		r.assistant(parts, t)
	case "tool":
		r.tool(parts, t)
	}
}

// codeBuddyIDE4Content is a message's content as a list of parts: a string
// is one text part.
func codeBuddyIDE4Content(v any) []map[string]any {
	switch c := v.(type) {
	case string:
		if c == "" {
			return nil
		}
		return []map[string]any{{"type": "text", "text": c}}
	case []any:
		out := make([]map[string]any, 0, len(c))
		for _, it := range c {
			if m, ok := it.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

// codeBuddyIDE4Role is resolveCraftMessageRole: the file's role, else the
// message's, else what its parts and extra say it is.
func codeBuddyIDE4Role(outer string, inner any, parts []map[string]any, extra map[string]any) string {
	for _, r := range []string{outer, str(inner)} {
		if r == "user" || r == "assistant" || r == "tool" {
			return r
		}
	}
	has := map[string]bool{}
	for _, p := range parts {
		has[str(p["type"])] = true
	}
	switch {
	case has["tool-call"] || has["reasoning"]:
		return "assistant"
	case has["tool-result"] && !has["text"] && !has["image"]:
		return "tool"
	}
	if ip, _ := extra["inputPhrase"].([]any); len(ip) > 0 || extra["isHelperMessage"] == true {
		return "user"
	}
	if _, ok := extra["agentMessageCount"]; ok || extra["toolStatus"] != nil || extra["tasks"] != nil {
		if has["text"] || has["image"] {
			return "assistant"
		}
	}
	return ""
}

// codeBuddyIDE4WorkspaceRE is the working directory the IDE writes into the
// first prompt of a conversation, inside <user_info>.
var codeBuddyIDE4WorkspaceRE = regexp.MustCompile(`(?m)^Workspace Folder:[ \t]*(.+?)[ \t]*$`)

func (r *codeBuddyIDE4Reader) user(parts []map[string]any, extra map[string]any, t time.Time) {
	var texts []string
	for _, p := range parts {
		if str(p["type"]) == "text" {
			if s := str(p["text"]); s != "" {
				texts = append(texts, s)
			}
		}
	}
	raw := strings.Join(texts, "\n")
	if r.cwd == "" {
		if m := codeBuddyIDE4WorkspaceRE.FindStringSubmatch(raw); m != nil {
			r.cwd = strings.TrimSpace(m[1])
		}
	}
	// A compaction summary in place of the turns it replaced, written as the
	// user's: the index keeps the turns.
	if extra["isMaxTokenLimitSummary"] == true {
		return
	}
	visible := codeBuddyIDE4VisibleText(raw, extra)
	// A team member's message to this conversation rides in the prompt as
	// <teammate-message>. The converter writes it as "**member**" and the
	// body into a team member's own conversation, and leaves it out of the
	// lead's, whose members hold it; a shutdown handshake is dropped either
	// way. None of it is the person's.
	visible, teammates := codeBuddyIDE4Teammates(visible)
	if r.s.Kind == model.KindSubagent {
		for _, tm := range teammates {
			r.s.Touch(t)
			r.s.Messages = append(r.s.Messages, model.Message{Role: "user", Text: capParsedMessage(tm), Time: t})
		}
	}
	text := codeBuddyIDE4QuestionAnswer(visible, extra)
	// A turn the client sent on its own — the reminder after a crash, a
	// tool's echo — has nothing the person said in it once the envelope is
	// off, and goes out with it.
	if strings.TrimSpace(text) == "" {
		return
	}
	r.s.Touch(t)
	r.s.Messages = append(r.s.Messages, model.Message{Role: "user", Text: capParsedMessage(text), Time: t})
	// An answer picked from the agent's options is the person's choice, not
	// a rule they typed; only what is left of the prompt beside it is.
	if typed := strings.TrimSpace(codeBuddyIDE4QuestionBlock.ReplaceAllString(visible, "")); typed != "" && extra["isHelperMessage"] != true {
		r.s.Typed = append(r.s.Typed, typed)
	}
}

var (
	codeBuddyIDE4Teammate        = regexp.MustCompile(`<teammate-message(\s[^>]*)?>([\s\S]*?)</teammate-message>`)
	codeBuddyIDE4TeammateID      = regexp.MustCompile(`teammate_id="([^"]+)"`)
	codeBuddyIDE4TeammateFrom    = regexp.MustCompile(`(?s)<from>(.*?)</from>`)
	codeBuddyIDE4TeammateContent = regexp.MustCompile(`(?s)<content>(.*?)</content>`)
)

// codeBuddyIDE4Teammates takes the teammate messages out of a prompt
// (parseCraftTeammateUserText): what is left, and each message as the
// converter shows it.
func codeBuddyIDE4Teammates(text string) (rest string, out []string) {
	if !strings.Contains(text, "<teammate-message") {
		return text, nil
	}
	for _, m := range codeBuddyIDE4Teammate.FindAllStringSubmatch(text, -1) {
		id, body := "", strings.TrimSpace(m[2])
		if a := codeBuddyIDE4TeammateID.FindStringSubmatch(m[1]); a != nil {
			id = strings.TrimSpace(a[1])
		} else if f := codeBuddyIDE4TeammateFrom.FindStringSubmatch(m[2]); f != nil {
			id = strings.TrimSpace(f[1])
			body = ""
			if c := codeBuddyIDE4TeammateContent.FindStringSubmatch(m[2]); c != nil {
				body = strings.TrimSpace(c[1])
			}
		}
		if id == "" || codeBuddyIDE4Shutdown(body) {
			continue
		}
		if body == "" {
			out = append(out, "**"+id+"**")
		} else {
			out = append(out, "**"+id+"**\n\n"+body)
		}
	}
	rest = codeBuddyIDE4Teammate.ReplaceAllString(text, "\n")
	for strings.Contains(rest, "\n\n\n") {
		rest = strings.ReplaceAll(rest, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(rest), out
}

// codeBuddyIDE4Shutdown is a teammate's reply to the lead's shutdown request.
func codeBuddyIDE4Shutdown(body string) bool {
	if !strings.HasPrefix(body, "{") {
		return false
	}
	var m map[string]any
	return json.Unmarshal([]byte(body), &m) == nil && m["type"] == "shutdown_response"
}

// codeBuddyIDE4Envelope names the blocks the IDE wraps a prompt in. Text
// inside them is the client's, not the person's (resolveVisibleUserText).
const codeBuddyIDE4Envelope = `automation_system_reminder|user_info|rules|always_applied_workspace_rules|agent_requestable_workspace_rules|git_status|project_context|additional_data|open_and_recently_viewed_files|system_reminder|system-reminder`

var (
	codeBuddyIDE4EnvelopeTag   = regexp.MustCompile(`</?(` + codeBuddyIDE4Envelope + `|user_query)\b`)
	codeBuddyIDE4UserQuery     = regexp.MustCompile(`(?s)<user_query>(.*?)</user_query>`)
	codeBuddyIDE4OpenUserQuery = regexp.MustCompile(`(?s)<user_query>(.*)$`)
	codeBuddyIDE4URIBadge      = regexp.MustCompile(`@(knowledge|skill|rules|git)://`)
)

// codeBuddyIDE4Blocks strips each envelope block, whole. Go's regexp has no
// backreference, so each tag is its own expression.
var codeBuddyIDE4Blocks = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, strings.Count(codeBuddyIDE4Envelope, "|")+1)
	for tag := range strings.SplitSeq(codeBuddyIDE4Envelope, "|") {
		out = append(out, regexp.MustCompile(`(?s)<`+regexp.QuoteMeta(tag)+`\b[^>]*>.*?</`+regexp.QuoteMeta(tag)+`>\s*`))
	}
	return out
}()

var codeBuddyIDE4LeadingEnvelope = regexp.MustCompile(`(?s)^.*</(` + codeBuddyIDE4Envelope + `)>\s*`)

// codeBuddyIDE4VisibleText is what the person typed in a prompt: the text of
// the blocks they put in the input box when the extra records them, else the
// <user_query> inside the envelope, else the input as they phrased it, else
// the prompt with the envelope taken off.
func codeBuddyIDE4VisibleText(raw string, extra map[string]any) string {
	finish := func(s string) string {
		return strings.TrimSpace(codeBuddyIDE4URIBadge.ReplaceAllString(s, "@$1:"))
	}
	if s := codeBuddyIDE4Phrase(extra["sourceContentBlocks"]); s != "" {
		return finish(s)
	}
	if !codeBuddyIDE4EnvelopeTag.MatchString(raw) {
		if strings.TrimSpace(raw) != "" {
			return finish(raw)
		}
		return finish(codeBuddyIDE4FromExtra(extra))
	}
	if q := codeBuddyIDE4Query(raw); q != "" {
		return finish(q)
	}
	if s := codeBuddyIDE4FromExtra(extra); s != "" {
		return finish(s)
	}
	stripped := raw
	for _, re := range codeBuddyIDE4Blocks {
		stripped = re.ReplaceAllString(stripped, "")
	}
	if q := codeBuddyIDE4Query(stripped); q != "" {
		return finish(q)
	}
	// An empty <user_query> holds nothing the person typed; left in, its
	// tags would read as the prompt.
	stripped = codeBuddyIDE4UserQuery.ReplaceAllString(stripped, "")
	return finish(codeBuddyIDE4LeadingEnvelope.ReplaceAllString(stripped, ""))
}

// codeBuddyIDE4Query is the text of the prompt's <user_query> blocks, or of
// the last one when the IDE wrote it without its closing tag. An empty block
// that is closed is no query: reading from its opening tag to the end took
// its closing tag and whatever followed for the prompt.
func codeBuddyIDE4Query(s string) string {
	var qs []string
	for _, m := range codeBuddyIDE4UserQuery.FindAllStringSubmatch(s, -1) {
		if q := strings.TrimSpace(m[1]); q != "" {
			qs = append(qs, q)
		}
	}
	if len(qs) > 0 {
		return strings.Join(qs, "\n")
	}
	if i := strings.LastIndex(s, "<user_query>"); i >= 0 && !strings.Contains(s[i:], "</user_query>") {
		if m := codeBuddyIDE4OpenUserQuery.FindStringSubmatch(s[i:]); m != nil {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
}

// codeBuddyIDE4Phrase renders the input box's blocks when one of them is a
// mention or an image, the case the converter takes them for the prompt
// (encodeContentBlocksToInlineText): text as it is, a file or folder as its
// @path, an image as @image#N:name, a command as /name. A mention is
// separated from text that touches it by a space only when both sides are
// word characters.
func codeBuddyIDE4Phrase(v any) string {
	blocks, _ := v.([]any)
	visible := false
	for _, b := range blocks {
		m, _ := b.(map[string]any)
		if t := str(m["type"]); t == "resource_link" || t == "image" {
			visible = true
		}
	}
	if !visible {
		return ""
	}
	out := ""
	lastMention := false
	add := func(s string, mention bool) {
		if s == "" {
			return
		}
		if (mention || lastMention) && out != "" && codeBuddyIDE4NeedsSpace(out, s) {
			out += " "
		}
		out += s
		lastMention = mention
	}
	images := 0
	for _, b := range blocks {
		m, _ := b.(map[string]any)
		meta, _ := m["_meta"].(map[string]any)
		appshot := str(m["type"]) == "image" && str(meta["resourceType"]) == "appshot"
		if meta["displayAsPhrase"] == false && !appshot {
			continue
		}
		switch str(m["type"]) {
		case "text":
			add(str(m["text"]), false)
		case "image":
			images++
			// The uri's last part as getLastPathSegment takes it: split at /
			// only, which reads the same on every platform.
			label := firstNonEmpty(str(meta["filename"]), str(meta["originalFilename"]), path.Base(strings.TrimRight(str(m["uri"]), "/")))
			if label == "" || label == "." || label == "/" {
				label = fmt.Sprintf("image-%d", images)
			}
			add(fmt.Sprintf("@image#%d:%s", images, codeBuddyIDE4Badge(label)), true)
		case "resource_link":
			if s, ok := codeBuddyIDE4Mention(m, meta); ok {
				add(s, true)
			}
		}
	}
	return strings.TrimSpace(out)
}

// codeBuddyIDE4Mention is a resource link as the converter writes it into the
// prompt (encodeAcpResourceLinkToInputText), for the kinds a 4.x prompt
// carries: a file or folder by its absolute path, or by its name when the IDE
// recorded a relative one, a command by its name, and a link of a known
// scheme by its name. A code selection is dropped, as the converter drops
// it: its text went to the model as context, not as what the person typed.
func codeBuddyIDE4Mention(m, meta map[string]any) (string, bool) {
	uri := str(m["uri"])
	name := str(m["name"])
	mention := str(meta["mentionType"])
	display := name
	if display == "" {
		display = path.Base(strings.TrimRight(uri, "/"))
	}
	switch {
	case uri == "":
		return "", false
	case strings.HasPrefix(uri, "command://"):
		if display == "" {
			display = strings.TrimPrefix(uri, "command://")
		}
		if !strings.HasPrefix(display, "/") {
			display = "/" + display
		}
		return display, true
	case strings.HasPrefix(uri, "file://"):
		p := strings.TrimPrefix(uri, "file://")
		if u, err := url.PathUnescape(p); err == nil {
			p = u
		}
		if !codeBuddyIDE4Absolute(p) {
			p = firstNonEmpty(str(meta["relativePath"]), name, p)
		}
		return "@" + codeBuddyIDE4FileBadge(p), true
	case mention == "file" || mention == "folder":
		if codeBuddyIDE4Absolute(uri) {
			return "@" + codeBuddyIDE4FileBadge(uri), true
		}
		// A relative path as the IDE recorded it: the converter keeps it
		// as the link's name.
		return display, true
	case strings.HasPrefix(uri, "skill://"):
		return "@skill:" + codeBuddyIDE4Badge(firstNonEmpty(name, strings.TrimPrefix(uri, "skill://"))), true
	case strings.HasPrefix(uri, "knowledge://"):
		return "@knowledge:" + codeBuddyIDE4Badge(firstNonEmpty(str(meta["name"]), display)), true
	case strings.HasPrefix(uri, "rule://"):
		return "@rules:" + codeBuddyIDE4Badge(display), true
	case strings.HasPrefix(uri, "http://"), strings.HasPrefix(uri, "https://"):
		return "@share-html:" + uri, true
	case strings.Contains(uri, "://"):
		return display, true
	}
	return "", false
}

func codeBuddyIDE4Absolute(p string) bool {
	return strings.HasPrefix(p, "/") || len(p) > 2 && p[1] == ':' && (p[2] == '\\' || p[2] == '/') &&
		(p[0] >= 'a' && p[0] <= 'z' || p[0] >= 'A' && p[0] <= 'Z')
}

// codeBuddyIDE4FileBadge is a path as a badge value: bare when the converter
// leaves it bare (isBareFileBadgePath), else quoted.
func codeBuddyIDE4FileBadge(p string) string {
	bare := p != "" && !strings.ContainsAny(p, " \t\n\r@\"\\<>")
	for _, r := range p {
		if r >= 0x3000 && r <= 0x303f || r >= 0xff01 && r <= 0xff65 {
			bare = false
		}
	}
	if bare {
		if codeBuddyIDE4NonASCII(p) {
			bare = strings.Contains(p, "/") || codeBuddyIDE4Ext.MatchString(p)
		} else {
			bare = codeBuddyIDE4BareValue(p)
		}
	}
	if bare {
		return p
	}
	return codeBuddyIDE4Quote(p)
}

var codeBuddyIDE4Ext = regexp.MustCompile(`\.[A-Za-z0-9]{1,16}$`)

func codeBuddyIDE4NonASCII(s string) bool {
	for _, r := range s {
		if r >= 0x80 {
			return true
		}
	}
	return false
}

// codeBuddyIDE4BareValue is isBareInlineBadgeValue: letters, digits and _./-.
func codeBuddyIDE4BareValue(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && !strings.ContainsRune("_./-", r) {
			return false
		}
	}
	return true
}

func codeBuddyIDE4Badge(s string) string {
	if codeBuddyIDE4BareValue(s) {
		return s
	}
	return codeBuddyIDE4Quote(s)
}

func codeBuddyIDE4Quote(s string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), `"`, `\"`) + `"`
}

// codeBuddyIDE4NeedsSpace is needsInlineSeparator: a space goes between a
// mention and the text beside it only when neither side is already space and
// both are word characters, @, / or a quote.
func codeBuddyIDE4NeedsSpace(before, after string) bool {
	b, _ := utf8.DecodeLastRuneInString(before)
	a, _ := utf8.DecodeRuneInString(after)
	if unicode.IsSpace(b) || unicode.IsSpace(a) {
		return false
	}
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_' || r == '-' }
	return (word(b) || b == '@' || b == '/' || b == '"') && (word(a) || a == '@' || a == '/')
}

// codeBuddyIDE4FromExtra is the prompt as the input box recorded it: the
// phrase, else the plain text blocks when they hold no envelope.
func codeBuddyIDE4FromExtra(extra map[string]any) string {
	if phrase, _ := extra["inputPhrase"].([]any); len(phrase) > 0 {
		var b strings.Builder
		for _, p := range phrase {
			m, _ := p.(map[string]any)
			content, expand := str(m["content"]), str(m["expandContent"])
			switch str(m["type"]) {
			case "normal":
				b.WriteString(content)
			case "file", "folder", "editor":
				b.WriteString("@" + firstNonEmpty(expand, content))
			case "command":
				b.WriteString("/" + strings.TrimPrefix(content, "/"))
			case "img":
				b.WriteString("@image:" + codeBuddyIDE4BaseName(firstNonEmpty(expand, content)))
			case "knowledge", "skill":
				b.WriteString("@" + str(m["type"]) + ":" + strings.TrimPrefix(content, "@"))
			default:
				if strings.HasPrefix(content, "@") || content == "" {
					b.WriteString(content)
				} else {
					b.WriteString("@" + content)
				}
			}
		}
		if s := strings.TrimSpace(b.String()); s != "" {
			return s
		}
	}
	blocks, _ := extra["sourceContentBlocks"].([]any)
	var b strings.Builder
	for _, it := range blocks {
		m, _ := it.(map[string]any)
		if str(m["type"]) == "text" {
			b.WriteString(str(m["text"]))
		}
	}
	if s := strings.TrimSpace(b.String()); s != "" && !codeBuddyIDE4EnvelopeTag.MatchString(s) {
		return s
	}
	return ""
}

// codeBuddyIDE4BaseName is the last part of a path as the converter takes it
// (fileName): split at / and at \ alike, whichever platform wrote it, so a
// store copied between machines reads the same on each.
func codeBuddyIDE4BaseName(p string) string {
	return p[strings.LastIndexAny(p, `/\`)+1:]
}

// codeBuddyIDE4QuestionBlock is the answer the IDE sends back for an
// ask_followup_question, as XML in the prompt.
var (
	codeBuddyIDE4QuestionBlock = regexp.MustCompile(`(?s)<question_answer\b[^>]*>.*?</question_answer>`)
	codeBuddyIDE4QuestionItem  = regexp.MustCompile(`(?s)<question_item\b[^>]*>(.*?)</question_item>`)
	codeBuddyIDE4Question      = regexp.MustCompile(`(?s)<question\b[^>]*>(.*?)</question>`)
	codeBuddyIDE4Answers       = regexp.MustCompile(`(?s)<answers\b[^>]*>(.*?)</answers>`)
)

// codeBuddyIDE4QuestionAnswer puts the person's answers to the agent's
// question in place of the XML that carries them: "question → answer", one a
// line, as the converter shows them. The answers are the person's words, and
// the only ones the turn holds.
func codeBuddyIDE4QuestionAnswer(text string, extra map[string]any) string {
	type qa struct {
		q string
		a []string
	}
	var items []qa
	from := func(v any) {
		m, _ := v.(map[string]any)
		qs, _ := m["questions"].([]any)
		for _, it := range qs {
			q, _ := it.(map[string]any)
			item := qa{q: strings.TrimSpace(str(q["question"]))}
			as, _ := q["answers"].([]any)
			for _, a := range as {
				if s := strings.TrimSpace(str(a)); s != "" {
					item.a = append(item.a, s)
				}
			}
			if item.q != "" {
				items = append(items, item)
			}
		}
	}
	from(extra["questionAnswer"])
	if len(items) == 0 {
		blocks, _ := extra["sourceContentBlocks"].([]any)
		for _, b := range blocks {
			m, _ := b.(map[string]any)
			meta, _ := m["_meta"].(map[string]any)
			if meta["questionAnswer"] != nil {
				from(meta["questionAnswer"])
				break
			}
		}
	}
	if len(items) == 0 {
		for _, block := range codeBuddyIDE4QuestionBlock.FindAllString(text, -1) {
			for _, it := range codeBuddyIDE4QuestionItem.FindAllStringSubmatch(block, -1) {
				item := qa{}
				if m := codeBuddyIDE4Question.FindStringSubmatch(it[1]); m != nil {
					item.q = strings.TrimSpace(m[1])
				}
				if m := codeBuddyIDE4Answers.FindStringSubmatch(it[1]); m != nil {
					for line := range strings.SplitSeq(m[1], "\n") {
						if s := strings.TrimSpace(line); s != "" {
							item.a = append(item.a, s)
						}
					}
				}
				if item.q != "" {
					items = append(items, item)
				}
			}
		}
	}
	rest := strings.TrimSpace(codeBuddyIDE4QuestionBlock.ReplaceAllString(text, ""))
	var lines []string
	for _, it := range items {
		if len(it.a) > 0 {
			lines = append(lines, it.q+" → "+strings.Join(it.a, ", "))
		}
	}
	if len(lines) == 0 {
		return rest
	}
	if rest != "" {
		lines = append([]string{rest}, lines...)
	}
	return strings.Join(lines, "\n")
}

func (r *codeBuddyIDE4Reader) assistant(parts []map[string]any, t time.Time) {
	for _, p := range parts {
		switch str(p["type"]) {
		case "text":
			if s := strings.TrimSpace(str(p["text"])); s != "" {
				r.s.Touch(t)
				r.s.Messages = append(r.s.Messages, model.Message{Role: "assistant", Text: capParsedMessage(s), Time: t})
			}
		case "tool-call":
			r.call(p, t)
		case "tool-result":
			r.result(p, t)
		}
	}
}

func (r *codeBuddyIDE4Reader) tool(parts []map[string]any, t time.Time) {
	for _, p := range parts {
		if str(p["type"]) == "tool-result" {
			r.result(p, t)
		}
	}
}

// codeBuddyIDE4ToolNames is what the converter renames the IDE's file and
// shell tools to (mapToolName), with the argument names it renames, so the
// Claude dialect reads the call the way it reads the converted transcript's.
var codeBuddyIDE4ToolNames = map[string]string{
	"read_file": "Read", "write_to_file": "Write", "replace_in_file": "Edit",
	"execute_command": "Bash",
}

var codeBuddyIDE4ArgNames = map[string]string{
	"filePath": "file_path", "old_str": "old_string", "new_str": "new_string",
}

func (r *codeBuddyIDE4Reader) call(p map[string]any, t time.Time) {
	name, id := str(p["toolName"]), str(p["toolCallId"])
	if name == "" || id == "" {
		return
	}
	args, _ := p["args"].(map[string]any)
	if s, ok := p["args"].(string); ok {
		_ = json.Unmarshal([]byte(s), &args)
	}
	mapped := name
	in := map[string]any{}
	if to, ok := codeBuddyIDE4ToolNames[name]; ok {
		mapped = to
		for k, v := range args {
			if nk, ok := codeBuddyIDE4ArgNames[k]; ok {
				k = nk
			}
			in[k] = v
		}
	} else {
		in = args
	}
	r.s.AddToolCall(mapped, false)
	blocks := []any{map[string]any{"type": "tool_use", "id": id, "name": mapped, "input": in}}
	recs := codeBuddyWorkRecords(blocks, t)
	if len(recs) == 0 {
		return
	}
	from := len(r.s.Messages)
	r.s.Touch(t)
	r.s.Messages = append(r.s.Messages, recs...)
	if IndexCommands() {
		r.exits.note(r.s.Messages, from, commandCallsIn(blocks, claudeDialect))
	}
}

func (r *codeBuddyIDE4Reader) result(p map[string]any, t time.Time) {
	id := str(p["toolCallId"])
	res := p["result"]
	if res == nil {
		res = p["content"]
	}
	failed := p["isError"] == true
	if m, ok := res.(map[string]any); ok {
		if m["success"] == false {
			failed = true
		}
		switch str(m["status"]) {
		case "error", "failed", "cancelled":
			failed = true
		}
	}
	if failed {
		r.s.ToolErrors++
	}
	if _, ok := r.exits[id]; ok && id != "" {
		if code, ok := codeBuddyIDE4ExitCode(res); ok {
			r.exits.stamp(r.s.Messages, id, "", code)
		}
		delete(r.exits, id)
	}
	if !IndexToolOutput() {
		return
	}
	if body := strings.TrimSpace(codeBuddyIDE4ToolOutput(str(p["toolName"]), res)); body != "" {
		r.s.Touch(t)
		r.s.Messages = append(r.s.Messages, model.Message{Role: RoleToolOutput, Text: capParsedMessage(body), Time: t})
	}
}

// codeBuddyIDE4ToolOutput is a tool's result as the index keeps it: the text
// codeBuddyIDE4ResultText finds in it, else what the 5.x converter writes into
// the transcript for it: a tool it renames formatted the way it formats it
// (remapResult), anything else the result as JSON with its keys sorted
// (craftToolResultText, stableJson). So an edit, a write or a file search,
// none of which carries a text field, still leaves an output in the converted
// transcript; read here as nothing, one conversation read 6 outputs before
// the upgrade and 15 after. Two results stay empty as they do there: a lint
// run's, which the converter formats from its diagnostics alone, and an
// image, which no reader indexes.
func codeBuddyIDE4ToolOutput(tool string, res any) string {
	if s := codeBuddyIDE4ResultText(res); strings.TrimSpace(s) != "" {
		return s
	}
	if codeBuddyIDE4HoldsImage(res) {
		return ""
	}
	if s, ok := codeBuddyIDE4Formatted(tool, res); ok {
		return s
	}
	return codeBuddyIDE4StableText(res)
}

// codeBuddyIDE4Formatted is the converter's own text for a result of a tool
// it renames, for the shapes whose text codeBuddyIDE4ResultText does not
// find (formatBashResult, formatReadLintsResult, formatGrepResult,
// formatGlobResult, formatSearchFileResult, formatWebSearchResult,
// formatAskFollowupResult). ok is false where the converter's formatter gives
// up and writes the JSON.
func codeBuddyIDE4Formatted(tool string, res any) (string, bool) {
	outer, _ := res.(map[string]any)
	in := res
	if outer != nil && outer["result"] != nil {
		in = outer["result"]
	}
	m, _ := in.(map[string]any)
	if m == nil {
		return "", false
	}
	typ := str(m["type"])
	paths := func(list any) string {
		items, _ := list.([]any)
		var out []string
		for _, it := range items {
			e, _ := it.(map[string]any)
			if p := str(e["filePath"]); p != "" {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			return "No files found"
		}
		return strings.Join(out, "\n")
	}
	switch tool {
	case "execute_command":
		_, hasOut := m["stdout"].(string)
		_, hasErr := m["stderr"].(string)
		if typ != "execute_command_result" && !hasOut && !hasErr {
			return "", false
		}
		stdout := firstNonEmpty(str(m["stdout"]), "(empty)")
		stderr := str(m["stderr"])
		code, hasCode := m["exitCode"].(float64)
		if stderr == "" && hasCode && code != 0 {
			stderr = strings.TrimSpace(str(outer["errorMessage"]))
		}
		exit := "(none)"
		if hasCode {
			exit = strconv.FormatFloat(code, 'f', -1, 64)
		}
		return "Stdout: " + stdout + "\nStderr: " + firstNonEmpty(stderr, "(empty)") + "\nExit Code: " + exit + "\nSignal: (none)", true
	case "read_lints":
		_, has := m["diagnostics"]
		return "", has
	case "search_content":
		if typ != "search_content_result" {
			return "", false
		}
		return codeBuddyIDE4GrepText(m), true
	case "list_dir", "list_files":
		if typ != "list_files_result" {
			return "", false
		}
		return paths(m["files"]), true
	case "search_file":
		if typ != "search_file_result" {
			return "", false
		}
		return paths(m["results"]), true
	case "web_search":
		if typ != "web_search_tool_result" {
			return "", false
		}
		query := firstNonEmpty(str(m["query"]), str(m["searchInput"]))
		results, _ := m["results"].([]any)
		if len(results) == 0 {
			return "No search results found for query: \"" + query + "\"", true
		}
		parts := make([]string, 0, len(results))
		for i, it := range results {
			e, _ := it.(map[string]any)
			link := str(e["url"])
			body := str(e["snippet"])
			if _, ok := e["snippet"].(string); !ok {
				body = str(e["content"])
			}
			parts = append(parts, fmt.Sprintf("## %d. [%s](%s)\n\n%s\n\n**URL:** %s\n\n---", i+1, str(e["title"]), link, body, link))
		}
		return "# Search Results for \"" + query + "\"\n\n" + strings.Join(parts, "\n\n"), true
	case "ask_followup_question":
		if typ != "multi_question_result" {
			return "", false
		}
		return codeBuddyIDE4AnswersText(m), true
	}
	return "", false
}

// codeBuddyIDE4GrepText is formatGrepResult: a line a match, as the path, the
// path and its count, or the path, line and first line of the match, by the
// search's output mode. It is asked only of a search whose matches hold no
// text, since codeBuddyIDE4ResultText reads the text of the others, so a
// count is never written into a match and each counts one, as
// countFromMatch has it for a match without one.
func codeBuddyIDE4GrepText(m map[string]any) string {
	mode := firstNonEmpty(str(m["outputMode"]), "content")
	none := "No matches found"
	if mode == "files_with_matches" {
		none = "No files found"
	}
	matches, _ := m["matches"].([]any)
	var lines []string
	for _, it := range matches {
		e, _ := it.(map[string]any)
		p := str(e["filePath"])
		if p == "" {
			continue
		}
		content := str(e["content"])
		switch mode {
		case "files_with_matches":
			lines = append(lines, p)
		case "count":
			lines = append(lines, p+":1")
		default:
			first, _, _ := strings.Cut(content, "\n")
			first = strings.TrimSuffix(first, "\r")
			if line, ok := e["startLine"].(float64); ok && line > 0 {
				lines = append(lines, p+":"+strconv.FormatFloat(line, 'f', -1, 64)+":"+first)
			} else {
				lines = append(lines, p+":"+first)
			}
		}
	}
	if len(lines) == 0 {
		return none
	}
	return strings.Join(lines, "\n")
}

// codeBuddyIDE4AnswersText is formatAskFollowupResult: each question the
// person answered, " · question → answers", one a line.
func codeBuddyIDE4AnswersText(m map[string]any) string {
	questions, _ := m["questions"].([]any)
	answers, _ := m["answers"].(map[string]any)
	var lines []string
	for _, it := range questions {
		q, _ := it.(map[string]any)
		if q == nil {
			continue
		}
		var picked []string
		switch a := answers[str(q["id"])].(type) {
		case []any:
			for _, s := range a {
				if s, ok := s.(string); ok && strings.TrimSpace(s) != "" {
					picked = append(picked, strings.TrimSpace(s))
				}
			}
		case string:
			if strings.TrimSpace(a) != "" {
				picked = append(picked, strings.TrimSpace(a))
			}
		}
		if text := strings.TrimSpace(str(q["question"])); text != "" && len(picked) > 0 {
			lines = append(lines, " · "+text+" → "+strings.Join(picked, ", "))
		}
	}
	return strings.Join(lines, "\n")
}

// codeBuddyIDE4StableText is craftToolResultText: a string as it is, the
// result field of an object when it is a string, else that field or the
// whole object as JSON with sorted keys.
func codeBuddyIDE4StableText(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case map[string]any:
		if inner, ok := x["result"]; ok {
			if s, isString := inner.(string); isString {
				return s
			}
			return codeBuddyIDE4StableJSON(inner)
		}
	}
	return codeBuddyIDE4StableJSON(v)
}

// codeBuddyIDE4StableJSON is stableJson: JSON.stringify of the value with
// every object's keys sorted. A JavaScript object lists keys that are array
// indexes first, in numeric order, whatever order they were added in, so
// those lead here too; JSON.stringify escapes no HTML.
func codeBuddyIDE4StableJSON(v any) string {
	var b strings.Builder
	codeBuddyIDE4WriteStable(&b, v)
	return b.String()
}

func codeBuddyIDE4WriteStable(b *strings.Builder, v any) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.SortFunc(keys, func(a, c string) int {
			ai, aIdx := codeBuddyIDE4ArrayIndex(a)
			ci, cIdx := codeBuddyIDE4ArrayIndex(c)
			switch {
			case aIdx && cIdx:
				return cmp.Compare(ai, ci)
			case aIdx:
				return -1
			case cIdx:
				return 1
			}
			return strings.Compare(a, c)
		})
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			codeBuddyIDE4WriteStable(b, k)
			b.WriteByte(':')
			codeBuddyIDE4WriteStable(b, x[k])
		}
		b.WriteByte('}')
	case []any:
		b.WriteByte('[')
		for i, it := range x {
			if i > 0 {
				b.WriteByte(',')
			}
			codeBuddyIDE4WriteStable(b, it)
		}
		b.WriteByte(']')
	case string:
		codeBuddyIDE4WriteJSONString(b, x)
	case float64:
		// JSON.stringify writes an integer without a fraction and any other
		// number in its shortest form, as strconv does for the range a store
		// writes.
		b.WriteString(strconv.FormatFloat(x, 'f', -1, 64))
	case bool:
		b.WriteString(strconv.FormatBool(x))
	default:
		// nil, and anything decoded JSON does not hold.
		b.WriteString("null")
	}
}

// codeBuddyIDE4WriteJSONString writes s as JSON.stringify quotes a string:
// the quote, the backslash and control characters escaped, the short forms
// where JSON has one, and nothing else, so <, > and & stay as they are.
func codeBuddyIDE4WriteJSONString(b *strings.Builder, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\f':
			b.WriteString(`\f`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// codeBuddyIDE4ArrayIndex reports a key JavaScript treats as an array index:
// a canonical decimal integer below 2^32-1.
func codeBuddyIDE4ArrayIndex(k string) (uint64, bool) {
	if k == "" || len(k) > 10 || (len(k) > 1 && k[0] == '0') {
		return 0, false
	}
	n, err := strconv.ParseUint(k, 10, 64)
	return n, err == nil && n < 1<<32-1
}

// codeBuddyIDE4HoldsImage reports a result that is an image: an image block,
// a list of them, the image references a read of an image file returns, or
// the 4.x read of one, an empty content beside the picture's base64. The
// converter writes that last one out as JSON, picture and all; no reader
// indexes an image, and on a real store it was 609 outputs of base64.
func codeBuddyIDE4HoldsImage(v any) bool {
	switch x := v.(type) {
	case string:
		return codeBuddyImageRefs(x)
	case []any:
		return slices.ContainsFunc(x, codeBuddyIDE4HoldsImage)
	case map[string]any:
		if t := str(x["type"]); t == "image" || t == "image_blob_ref" {
			return true
		}
		if img, ok := x["image"].(map[string]any); ok && img["data"] != nil {
			return true
		}
		if inner, ok := x["result"]; ok && inner != nil {
			return codeBuddyIDE4HoldsImage(inner)
		}
	}
	return false
}

// codeBuddyIDE4ExitCode is the code an execute_command result reports. The
// store is decoded with encoding/json's defaults, so a number is a float64.
func codeBuddyIDE4ExitCode(v any) (int, bool) {
	m, _ := v.(map[string]any)
	inner, _ := m["result"].(map[string]any)
	n, ok := inner["exitCode"].(float64)
	if !ok || n != float64(int(n)) {
		return 0, false
	}
	return int(n), true
}

// codeBuddyIDE4ResultText is what a tool returned, as text: the output a
// shell call printed, the text an MCP tool sent back, the message the client
// wrote for the model. Images and the client's own bookkeeping are left out.
func codeBuddyIDE4ResultText(v any) string {
	switch x := v.(type) {
	case string:
		if codeBuddyImageRefs(x) {
			return ""
		}
		return x
	case []any:
		var parts []string
		for _, it := range x {
			if s := codeBuddyIDE4ResultText(it); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, "\n")
	case map[string]any:
		if t := str(x["type"]); t == "image" || t == "image_blob_ref" {
			return ""
		}
		if t := str(x["type"]); t == "text" || t == "input_text" {
			return str(x["text"])
		}
		inner := x["result"]
		if inner == nil {
			inner = x
		}
		if s, ok := inner.(string); ok {
			return s
		}
		if list, ok := inner.([]any); ok {
			return codeBuddyIDE4ResultText(list)
		}
		in, _ := inner.(map[string]any)
		if in == nil {
			return str(x["errorMessage"])
		}
		if t := str(in["type"]); t == "text" || t == "input_text" {
			return str(in["text"])
		}
		var out []string
		for _, k := range []string{"stdout", "stderr", "content", "diagnostics", "matches", "listing", "finalResult", "toolCallBrief", "message"} {
			switch s := in[k].(type) {
			case string:
				if strings.TrimSpace(s) != "" {
					out = append(out, s)
				}
			case []any:
				if txt := codeBuddyIDE4ResultText(s); txt != "" {
					out = append(out, txt)
				}
			}
		}
		if d, ok := in["data"]; ok {
			if txt := codeBuddyIDE4ResultText(d); txt != "" {
				out = append(out, txt)
			}
		}
		if len(out) == 0 {
			if e := str(x["errorMessage"]); e != "" {
				out = append(out, e)
			}
		}
		return strings.Join(out, "\n")
	}
	return ""
}
