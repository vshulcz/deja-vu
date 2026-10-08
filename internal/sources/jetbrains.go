package sources

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

// JetBrains AI Assistant keeps its chats in the IDE's config directory, one
// directory per product and version (IntelliJIdea2026.2, PyCharm2026.1, …):
//
//	<config>/workspace/<projectWorkspaceId>.xml   a project's IDE state; the chats
//	                                              are its ChatSessionStateTemp component
//	<config>/aia-task-history/<chat uid>.events   an agent chat's blocks (aui.go)
//	<config>/aia-task-history/<chat uid>.agentsession
//	                                              acp.registry.<agent>:<its own session id>
//	<config>/options/recentProjects.xml           project path → projectWorkspaceId
//
// The workspace file is named after the project's workspace id, not its path
// (ProjectStoreImpl in intellij-community puts PRODUCT_WORKSPACE_FILE at
// workspace/<projectWorkspaceId>.xml), and recentProjects.xml maps the id back:
// <entry key="$USER_HOME$/src/app"><value><RecentProjectMetaInfo
// projectWorkspaceId="…">.
//
// A chat is a SerializedChat of <option name=…> children: uid, chatModelId,
// title/SerializedChatTitle/text, statisticInformation/ChatStatisticInformation
// /timestamp, modifiedAt, and messages/list/SerializedChatMessage with author
// ("Assistant", absent for the person), displayContent and internalContent. An
// agent chat (chatModelId agent_…) keeps only the prompts there: its work is in
// the .events file named after the chat's uid. When .agentsession names an
// agent whose own store deja reads (Junie, Claude, Codex, Copilot, Cline, Kilo,
// opencode), that store has the whole conversation and the chat is left to it.
// Checked against chats and task files from AI Assistant 2025.1 to 2026.2.

// JetBrainsRoot is the vendor config directory: ~/Library/Application
// Support/JetBrains, %APPDATA%\JetBrains, or $XDG_CONFIG_HOME/JetBrains.
// DEJA_JETBRAINS_ROOT replaces it.
func JetBrainsRoot() string {
	if v := os.Getenv("DEJA_JETBRAINS_ROOT"); v != "" {
		return v
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(Home(), "Library", "Application Support", "JetBrains")
	case "windows":
		app := os.Getenv("APPDATA")
		if app == "" {
			app = filepath.Join(Home(), "AppData", "Roaming")
		}
		return filepath.Join(app, "JetBrains")
	default:
		cfg := os.Getenv("XDG_CONFIG_HOME")
		if cfg == "" {
			cfg = filepath.Join(Home(), ".config")
		}
		return filepath.Join(cfg, "JetBrains")
	}
}

// JetBrainsSessionFiles lists every product's workspace files. Most hold no
// chat; the reader skips those on a byte search before parsing anything.
func JetBrainsSessionFiles() []string {
	products, err := os.ReadDir(JetBrainsRoot())
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range products {
		if !p.IsDir() {
			continue
		}
		matches, _ := filepath.Glob(filepath.Join(JetBrainsRoot(), p.Name(), "workspace", "*.xml"))
		out = append(out, matches...)
	}
	return out
}

func isJetBrainsWorkspace(p string) bool {
	return filepath.Ext(p) == ".xml" && filepath.Base(filepath.Dir(p)) == "workspace" &&
		filepath.Dir(filepath.Dir(filepath.Dir(p))) == filepath.Clean(JetBrainsRoot())
}

// jetBrainsSidecar fingerprints the agent task logs beside a workspace file,
// which the reader reads for its agent chats.
func jetBrainsSidecar(p string) (int64, int64) {
	logs, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(p)), "aia-task-history", "*.events"))
	return sidecarStat(logs...)
}

func LoadJetBrains() []model.Session {
	return parseFiles(JetBrainsSessionFiles(), ParseJetBrainsFile)
}

const jetBrainsChatMarker = `<component name="ChatSessionStateTemp">`

// jetBrainsNativeAgents are the ACP agents that keep a store of their own deja
// reads, by the id AI Assistant gives them.
var jetBrainsNativeAgents = map[string]bool{
	"junie": true, "claude-acp": true, "codex-acp": true, "github-copilot": true,
	"cline": true, "kilo": true, "opencode": true,
}

// xnode is an element of the workspace file, kept whole.
type xnode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Nodes   []xnode    `xml:",any"`
}

func (n xnode) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

// option is the <option name=…> child named name.
func (n xnode) option(name string) (xnode, bool) {
	for _, c := range n.Nodes {
		if c.XMLName.Local == "option" && c.attr("name") == name {
			return c, true
		}
	}
	return xnode{}, false
}

func (n xnode) value(name string) string {
	o, _ := n.option(name)
	return o.attr("value")
}

func (n xnode) child(tag string) (xnode, bool) {
	for _, c := range n.Nodes {
		if c.XMLName.Local == tag {
			return c, true
		}
	}
	return xnode{}, false
}

// walk visits every element under n.
func (n xnode) walk(fn func(xnode)) {
	for _, c := range n.Nodes {
		fn(c)
		c.walk(fn)
	}
}

// ParseJetBrainsFile reads the chats of one workspace file.
func ParseJetBrainsFile(path string) ([]model.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if !bytes.Contains(b, []byte(jetBrainsChatMarker)) {
		return nil, nil
	}
	component, err := jetBrainsChatComponent(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	config := filepath.Dir(filepath.Dir(path))
	workspaceID := strings.TrimSuffix(filepath.Base(path), ".xml")
	project := jetBrainsProjectPath(filepath.Join(config, "options", "recentProjects.xml"), workspaceID)
	var out []model.Session
	component.walk(func(n xnode) {
		if n.XMLName.Local != "SerializedChat" {
			return
		}
		if s, ok := jetBrainsChat(n, path, config, project); ok {
			out = append(out, s)
		}
	})
	return out, nil
}

// jetBrainsChatComponent decodes the one component that holds chats, without
// building the rest of the file.
func jetBrainsChatComponent(r io.Reader) (xnode, error) {
	dec := xml.NewDecoder(r)
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			return xnode{}, nil
		}
		if err != nil {
			return xnode{}, err
		}
		se, ok := tok.(xml.StartElement)
		if !ok || se.Name.Local != "component" {
			continue
		}
		for _, a := range se.Attr {
			if a.Name.Local == "name" && a.Value == "ChatSessionStateTemp" {
				var n xnode
				err := dec.DecodeElement(&n, &se)
				return n, err
			}
		}
	}
}

func jetBrainsChat(n xnode, path, config, project string) (model.Session, bool) {
	uid := n.value("uid")
	if uid == "" {
		return model.Session{}, false
	}
	s := model.Session{Harness: "jetbrains", ID: uid, Path: path}
	if project != "" {
		s.Project = projectName(project)
	}
	if t, ok := n.option("title"); ok {
		if title, ok := t.child("SerializedChatTitle"); ok {
			s.Title = firstLineTrim(title.value("text"))
		}
	}
	modified := jetBrainsMillis(n.value("modifiedAt"))
	start := modified
	if st, ok := n.option("statisticInformation"); ok {
		if info, ok := st.child("ChatStatisticInformation"); ok {
			if t := jetBrainsMillis(info.value("timestamp")); !t.IsZero() {
				start = t
			}
		}
	}
	if strings.HasPrefix(n.value("chatModelId"), "agent_") {
		history := filepath.Join(config, "aia-task-history")
		if jetBrainsNativeAgents[jetBrainsAgentID(filepath.Join(history, uid+".agentsession"))] {
			return model.Session{}, false
		}
		if events := jetBrainsTaskEvents(filepath.Join(history, uid+".events"), start); len(events) > 0 {
			for _, m := range auiRecords(events, project) {
				s.Touch(m.Time)
				s.Messages = append(s.Messages, m)
			}
			s.Touch(modified)
			return s, len(s.Messages) > 0
		}
	}
	i := 0
	if msgs, ok := n.option("messages"); ok {
		if list, ok := msgs.child("list"); ok {
			for _, m := range list.Nodes {
				if m.XMLName.Local != "SerializedChatMessage" {
					continue
				}
				text := m.value("displayContent")
				if strings.TrimSpace(text) == "" {
					text = m.value("internalContent")
				}
				text = strings.TrimSpace(text)
				if text == "" {
					continue
				}
				role := "user"
				if m.value("author") == "Assistant" {
					role = "assistant"
				}
				// The file stores no time per message: the chat's start, one
				// millisecond per message, keeps two identical turns apart.
				at := start.Add(time.Duration(i) * time.Millisecond)
				i++
				s.Touch(at)
				s.Messages = append(s.Messages, model.Message{Role: role, Text: text, Time: at})
			}
		}
	}
	s.Touch(modified)
	return s, len(s.Messages) > 0
}

// jetBrainsAgentID is the ACP registry id of the agent that ran a task, from
// its .agentsession pointer ("acp.registry.codex-acp:<session id>"). A task
// with no pointer never reached an agent's own store, so "" leaves it to this
// reader whatever the chat's model id says.
func jetBrainsAgentID(pointer string) string {
	b, err := os.ReadFile(pointer)
	if err != nil {
		return ""
	}
	head, _, ok := strings.Cut(strings.TrimSpace(string(b)), ":")
	if !ok {
		return ""
	}
	return strings.TrimPrefix(head, "acp.registry.")
}

// jetBrainsTaskEvents reads an agent task's log: AUI_EVENTS_V1, then one
// base64 JSON record per line. A record is a ChatSessionUserPromptEvent, or a
// ChatSessionMessageBlockEvent whose event is an AUI block. Records carry no
// time; start, one millisecond per record, keeps them in order.
func jetBrainsTaskEvents(path string, start time.Time) []auiEvent {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []auiEvent
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 64<<20)
	n := 0
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 || string(line) == "AUI_EVENTS_V1" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(string(line))
		if err != nil {
			continue
		}
		var rec map[string]any
		if json.Unmarshal(raw, &rec) != nil {
			continue
		}
		at := start.Add(time.Duration(n) * time.Millisecond)
		n++
		typ, _ := rec["type"].(string)
		switch auiKind(typ) {
		case "ChatSessionUserPromptEvent":
			text, _ := rec["prompt"].(string)
			out = append(out, auiEvent{Kind: auiPrompt, Body: map[string]any{"prompt": text}, At: at})
		case "ChatSessionMessageBlockEvent":
			ev, _ := rec["event"].(map[string]any)
			kind, _ := ev["kind"].(string)
			out = append(out, auiEvent{Kind: auiKind(kind), Body: ev, At: at})
		}
	}
	return out
}

// jetBrainsProjectPath is the project directory recentProjects.xml files
// under a workspace id, with $USER_HOME$ expanded; "" when it names none.
func jetBrainsProjectPath(recent, workspaceID string) string {
	f, err := os.Open(recent)
	if err != nil || workspaceID == "" {
		return ""
	}
	defer f.Close()
	dec := xml.NewDecoder(f)
	key := ""
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		switch se.Name.Local {
		case "entry":
			key = ""
			for _, a := range se.Attr {
				if a.Name.Local == "key" {
					key = a.Value
				}
			}
		case "RecentProjectMetaInfo":
			for _, a := range se.Attr {
				if a.Name.Local == "projectWorkspaceId" && a.Value == workspaceID && key != "" {
					return filepath.FromSlash(strings.ReplaceAll(key, "$USER_HOME$", filepath.ToSlash(Home())))
				}
			}
		}
	}
}

func jetBrainsMillis(v string) time.Time {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil || n <= 0 {
		return time.Time{}
	}
	return time.UnixMilli(n)
}
