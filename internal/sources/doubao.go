package sources

// Doubao Desktop (ByteDance's Doubao, 豆包桌面版) keeps its conversations in a
// Chromium IndexedDB database under
// `%LOCALAPPDATA%\Doubao\User Data\<profile>\IndexedDB\chrome_doubao-chat_0.indexeddb.leveldb`
// (macOS and Linux place the same layout under the Electron app-data roots).
// The reader in doubao_leveldb.go and doubao_idb.go does the three-layer
// decoding; this file locates the store, pulls conversations out of the
// decoded records, and registers the harness.
//
// Two record kinds carry anything:
//
//   - `{uid, schema, data}` records whose `schema` starts with
//     `conversation-list-data-` hold conversation metadata (`conversation_id`,
//     `name`, `create_time`, `update_time`). Doubao writes several schema
//     variants of the same list; fields are merged per conversation.
//   - the protocol record `default-chat-im-protocol-store` — under 2.35+
//     written as `im-sdk:user:<uid>:default-chat-im-protocol-store` — holds
//     `{state: {mainTaskDataMap}, version}`. One `mainTaskDataMap[taskId]` is
//     one question-answer task: the user prompt in
//     `requestQuery.syncTask.messages[].content_block[]`, the streaming reply
//     in `receivedMessages[id].extra.content_blocks_v2[]`, both with
//     `block_type` 10000 as the only text-bearing block.
//
// The protocol record is rewritten on every streaming increment and cleared
// when generation ends, so the current value alone reads as zero sessions;
// the answers live in superseded versions, which readIndexedDb keeps. Tasks
// merge across snapshots by taking the longest reply per taskId.
//
// Only conversations whose local snapshots actually hold text are indexed:
// Doubao keeps full bodies server-side and caches the recently opened ones
// locally. A conversation without readable turns is skipped, not stubbed.
//
// Format attested by Nwflower/dsh-chat-import (lib/convert/doubao.mjs, drift
// adapted in PR #123) and checked against a real 2.35.12 → 2.37.8 store.

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/vshulcz/deja-vu/internal/model"
)

const (
	// DoubaoIndexedDbSuffix ends every Chromium IndexedDB directory.
	DoubaoIndexedDbSuffix = ".indexeddb.leveldb"
	// doubaoChatOrigin prefixes the chat window's database; auxiliary windows
	// (background, launcher, text-picker) keep no session bodies.
	doubaoChatOrigin = "chrome_doubao-chat"

	doubaoTextBlock    = 10000
	doubaoProtocolKey  = "default-chat-im-protocol-store"
	doubaoListSchema   = "conversation-list-data-"
	doubaoMaxDepthWalk = 8
)

// DoubaoRoot returns the Doubao user-data root. DEJA_DOUBAO_ROOT replaces it.
func DoubaoRoot() string {
	if p := strings.TrimSpace(os.Getenv("DEJA_DOUBAO_ROOT")); p != "" {
		return p
	}
	switch runtime.GOOS {
	case "windows":
		local := strings.TrimSpace(os.Getenv("LOCALAPPDATA"))
		if local == "" {
			if h := Home(); h != "" {
				local = filepath.Join(h, "AppData", "Local")
			}
		}
		return filepath.Join(local, "Doubao", "User Data")
	case "darwin":
		return filepath.Join(Home(), "Library", "Application Support", "Doubao")
	default:
		return filepath.Join(Home(), ".config", "Doubao")
	}
}

// DoubaoIndexedDbDirs lists the chat window's IndexedDB LevelDB directories
// under the root. Profiles nest arbitrarily (Doubao writes `aha/users/<id>/…`),
// so the walk is depth-bounded rather than shape-exact, and a matched database
// directory is not descended into.
func DoubaoIndexedDbDirs() []string {
	root := DoubaoRoot()
	if root == "" {
		return nil
	}
	var dirs []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			// An unreadable directory takes nothing out of the walk; there is
			// no error to report upward through WalkDir's own channel.
			return nil //nolint: nilerr // skip unreadable directories
		}
		if !d.IsDir() {
			return nil
		}
		base := d.Name()
		if strings.HasSuffix(base, DoubaoIndexedDbSuffix) && strings.HasPrefix(base, doubaoChatOrigin) {
			dirs = append(dirs, p)
			return filepath.SkipDir
		}
		if depth := strings.Count(strings.TrimPrefix(p, root), string(filepath.Separator)); depth >= doubaoMaxDepthWalk {
			return filepath.SkipDir
		}
		return nil
	})
	sort.Strings(dirs)
	return dirs
}

// DoubaoFiles lists the LevelDB files the parser reads, one path per change
// the walk can notice. The directories themselves are not listed: surfaces
// that size a store count regular files.
func DoubaoFiles() []string {
	var out []string
	for _, dir := range DoubaoIndexedDbDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.Type().IsRegular() {
				continue
			}
			switch strings.ToLower(filepath.Ext(e.Name())) {
			case ".ldb", ".log":
				out = append(out, filepath.Join(dir, e.Name()))
			}
		}
	}
	sort.Strings(out)
	return out
}

// IsDoubaoLevelDbFile reports whether p is one of the chat database's files.
func IsDoubaoLevelDbFile(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".ldb", ".log":
	default:
		return false
	}
	base := filepath.Base(filepath.Dir(p))
	if !strings.HasSuffix(base, DoubaoIndexedDbSuffix) || !strings.HasPrefix(base, doubaoChatOrigin) {
		return false
	}
	rel, err := filepath.Rel(filepath.Clean(DoubaoRoot()), filepath.Clean(p))
	return err == nil && rel != "." && rel != ".." && rel != "" && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// doubaoStats counts what a store read left on the floor, for tests and for
// the empty-store diagnostics.
type doubaoStats struct {
	skippedBlocks       int
	emptyBlocks         int
	restarted           int
	emptyConversations  int
	orphanConversations int
}

// doubaoTime converts a Doubao timestamp: seconds or milliseconds as a
// number, either as a numeric string, or an ISO date string. Values below
// 1e11 count as seconds.
func doubaoTime(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return doubaoTimeMs(float64(x))
	case float64:
		return doubaoTimeMs(x)
	case idbKeyDouble:
		return doubaoTimeMs(float64(x))
	case string:
		trimmed := strings.TrimSpace(x)
		if trimmed == "" {
			return 0, false
		}
		if n, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return doubaoTimeMs(n)
		}
		if t, err := time.Parse(time.RFC3339Nano, trimmed); err == nil {
			return t.UnixMilli(), true
		}
		for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02"} {
			if t, err := time.ParseInLocation(layout, trimmed, time.Local); err == nil {
				return t.UnixMilli(), true
			}
		}
	}
	return 0, false
}

func doubaoTimeMs(v float64) (int64, bool) {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	if v < 1e11 {
		v *= 1000
	}
	ms := math.Round(v)
	if math.Abs(ms) > 9e15 {
		return 0, false
	}
	return int64(ms), true
}

// doubaoTextOfBlocks joins the text of one content-block array. Blocks without
// text (images, cards, tool calls) are counted as skipped, not turned into
// placeholder text.
func doubaoTextOfBlocks(blocks []any, stats *doubaoStats) string {
	var parts []string
	for _, b := range blocks {
		obj, ok := v8AsObj(b)
		if !ok {
			continue
		}
		bt, _ := obj.get("block_type")
		blockType, _ := v8AsInt(bt)
		if blockType != doubaoTextBlock {
			stats.skippedBlocks++
			continue
		}
		text := ""
		if content, ok := v8AsObjAt(obj, "content"); ok {
			if tb, ok := v8AsObjAt(content, "text_block"); ok {
				text, _ = v8StringAt(tb, "text")
			}
		}
		if text != "" {
			parts = append(parts, text)
		} else {
			stats.emptyBlocks++
		}
	}
	return strings.Join(parts, "\n")
}

// doubaoTask is one question-answer turn after normalization.
type doubaoTask struct {
	conversationID string
	taskID         string
	prompt         string
	reply          string
	promptTime     int64
	promptTimeOK   bool
	replyTime      int64
	replyTimeOK    bool
}

// doubaoNormalizeTask turns one mainTaskDataMap entry into a turn. It needs a
// conversation id, a prompt or a reply; anything less returns not-ok.
//
// 2.37+ writes client_meta.conversation_id as an empty string and carries the
// id on each received message's extra instead — all messages of one task
// belong to one conversation, so the first non-empty one wins.
func doubaoNormalizeTask(taskID string, task *v8Obj, stats *doubaoStats) (doubaoTask, bool) {
	if task == nil {
		return doubaoTask{}, false
	}
	requestQuery, _ := task.get("requestQuery")
	var sync *v8Obj
	if rq, ok := v8AsObj(requestQuery); ok {
		sync, _ = v8AsObjAt(rq, "syncTask")
	}
	var meta *v8Obj
	if sync != nil {
		meta, _ = v8AsObjAt(sync, "client_meta")
	}
	conversationID := ""
	if meta != nil {
		if id, ok := v8StringAt(meta, "conversation_id"); ok && id != "" {
			conversationID = id
		}
	}
	if conversationID == "" {
		if received, ok := v8AsObjAt(task, "receivedMessages"); ok {
			for _, id := range received.keys {
				r, _ := received.get(id)
				ro, ok := v8AsObj(r)
				if !ok {
					continue
				}
				extra, ok := v8AsObjAt(ro, "extra")
				if !ok {
					continue
				}
				if id2, ok := v8StringAt(extra, "conversation_id"); ok && id2 != "" {
					conversationID = id2
					break
				}
			}
		}
	}
	if conversationID == "" {
		return doubaoTask{}, false
	}

	prompt := ""
	if sync != nil {
		var blocks []any
		if messages, ok := v8ArrAt(sync, "messages"); ok {
			for _, m := range messages {
				mo, ok := v8AsObj(m)
				if !ok {
					continue
				}
				if mb, ok := v8ArrAt(mo, "content_block"); ok {
					blocks = append(blocks, mb...)
				}
			}
		}
		prompt = doubaoTextOfBlocks(blocks, stats)
	}

	var replies []string
	replyTimeOK := false
	var replyTime int64
	if received, ok := v8AsObjAt(task, "receivedMessages"); ok {
		for _, id := range received.keys {
			r, _ := received.get(id)
			ro, ok := v8AsObj(r)
			if !ok {
				continue
			}
			extra, ok := v8AsObjAt(ro, "extra")
			if !ok {
				continue
			}
			if blocks, ok := v8ArrAt(extra, "content_blocks_v2"); ok {
				if text := doubaoTextOfBlocks(blocks, stats); text != "" {
					replies = append(replies, text)
				}
			}
			if !replyTimeOK {
				if t, ok := v8TimeAt(extra, "create_time"); ok {
					replyTime, replyTimeOK = t, true
				}
			}
		}
	}
	reply := strings.Join(replies, "\n")
	if prompt == "" && reply == "" {
		return doubaoTask{}, false
	}

	out := doubaoTask{
		conversationID: conversationID,
		taskID:         taskID,
		prompt:         prompt,
		reply:          reply,
	}
	if sync != nil {
		if option, ok := v8AsObjAt(sync, "option"); ok {
			if t, ok := v8TimeAt(option, "create_time_ms"); ok {
				out.promptTime, out.promptTimeOK = t, true
			}
		}
	}
	if !out.promptTimeOK {
		if t, ok := v8TimeAt(task, "createTime"); ok {
			out.promptTime, out.promptTimeOK = t, true
		}
	}
	out.replyTime, out.replyTimeOK = replyTime, replyTimeOK
	return out, true
}

// doubaoCollectTasks merges every streaming snapshot of every protocol record
// into one turn per taskId, keeping the longest reply seen. A reply that grew
// without starting with the previous one is a regenerate, counted as
// restarted.
func doubaoCollectTasks(records []idbRecord, stats *doubaoStats) (map[string]doubaoTask, int) {
	best := map[string]doubaoTask{}
	restarted := 0
	for _, record := range records {
		for _, snapshot := range record.versions {
			root, ok := v8AsObj(snapshot)
			if !ok {
				continue
			}
			state, ok := v8AsObjAt(root, "state")
			if !ok {
				continue
			}
			tasks, ok := v8AsObjAt(state, "mainTaskDataMap")
			if !ok {
				continue
			}
			for _, taskID := range tasks.keys {
				raw, _ := tasks.get(taskID)
				taskObj, _ := v8AsObj(raw)
				normalized, ok := doubaoNormalizeTask(taskID, taskObj, stats)
				if !ok {
					continue
				}
				prev, seen := best[normalized.taskID]
				if !seen {
					best[normalized.taskID] = normalized
					continue
				}
				if len(normalized.reply) > len(prev.reply) {
					if prev.reply != "" && normalized.reply != "" && !strings.HasPrefix(normalized.reply, prev.reply) {
						restarted++
					}
					if normalized.prompt == "" {
						normalized.prompt = prev.prompt
					}
					best[normalized.taskID] = normalized
				} else if normalized.prompt != "" && prev.prompt == "" {
					prev.prompt = normalized.prompt
					best[normalized.taskID] = prev
				}
			}
		}
	}
	return best, restarted
}

// doubaoConversation is merged conversation-list metadata.
type doubaoConversation struct {
	id        string
	title     string
	createdAt int64
	createdOK bool
	updatedAt int64
	updatedOK bool
}

// doubaoCollectConversations merges the conversation-list records. The same
// conversation appears in several schema variants with different fields; each
// field takes the value wherever one exists.
func doubaoCollectConversations(records []idbRecord) map[string]*doubaoConversation {
	out := map[string]*doubaoConversation{}
	for _, record := range records {
		value, ok := v8AsObj(record.value)
		if !ok {
			continue
		}
		schema, ok := v8StringAt(value, "schema")
		if !ok || !strings.HasPrefix(schema, doubaoListSchema) {
			continue
		}
		data, ok := v8AsObjAt(value, "data")
		if !ok {
			continue
		}
		list, ok := v8ArrAt(data, "conversations")
		if !ok {
			continue
		}
		for _, item := range list {
			io, ok := v8AsObj(item)
			if !ok {
				continue
			}
			id, ok := v8StringAt(io, "conversation_id")
			if !ok || id == "" {
				continue
			}
			title, _ := v8StringAt(io, "name")
			createdAt, createdOK := v8TimeAt(io, "create_time")
			updatedAt, updatedOK := v8TimeAt(io, "update_time")
			prev, seen := out[id]
			if !seen {
				out[id] = &doubaoConversation{
					id:        id,
					title:     strings.TrimSpace(title),
					createdAt: createdAt, createdOK: createdOK,
					updatedAt: updatedAt, updatedOK: updatedOK,
				}
				continue
			}
			if prev.title == "" && title != "" {
				prev.title = strings.TrimSpace(title)
			}
			if createdOK && (!prev.createdOK || createdAt < prev.createdAt) {
				prev.createdAt, prev.createdOK = createdAt, true
			}
			if updatedOK && (!prev.updatedOK || updatedAt > prev.updatedAt) {
				prev.updatedAt, prev.updatedOK = updatedAt, true
			}
		}
	}
	return out
}

// doubaoSessions assembles the decoded records into sessions, one per
// conversation with at least one readable turn. No conversation metadata is
// not an error — the turns still carry their own times (counted as orphans).
func doubaoSessions(records []idbRecord, dir string, stats *doubaoStats) []model.Session {
	conversations := doubaoCollectConversations(records)
	var protocol []idbRecord
	for _, r := range records {
		if idbKeyMatchesSuffix(r.key, doubaoProtocolKey) {
			protocol = append(protocol, r)
		}
	}
	tasks, restarted := doubaoCollectTasks(protocol, stats)
	stats.restarted = restarted

	byConversation := map[string][]doubaoTask{}
	ids := []string{}
	for _, t := range tasks {
		if _, seen := byConversation[t.conversationID]; !seen {
			ids = append(ids, t.conversationID)
		}
		byConversation[t.conversationID] = append(byConversation[t.conversationID], t)
	}

	var sessions []model.Session
	for _, conversationID := range ids {
		turns := byConversation[conversationID]
		usable := turns[:0:0]
		for _, t := range turns {
			if t.prompt != "" || t.reply != "" {
				usable = append(usable, t)
			}
		}
		if len(usable) == 0 {
			stats.emptyConversations++
			continue
		}
		// Oldest question first; turns without a time sort last, stably.
		sort.SliceStable(usable, func(i, j int) bool {
			return doubaoTurnKey(usable[i]) < doubaoTurnKey(usable[j])
		})
		meta := conversations[conversationID]
		if meta == nil {
			stats.orphanConversations++
		}
		var started, updated time.Time
		if meta != nil && meta.createdOK {
			started = time.UnixMilli(meta.createdAt)
		}
		if meta != nil && meta.updatedOK {
			updated = time.UnixMilli(meta.updatedAt)
		}
		title := ""
		if meta != nil {
			title = meta.title
		}
		s := model.Session{
			Harness: "doubao",
			ID:      conversationID,
			Path:    dir,
			// A chat app has no working directory; the constant keeps the
			// sessions groupable under their source.
			Project: "Doubao",
			Title:   title,
			Started: started,
			Updated: updated,
		}
		for _, t := range usable {
			promptTime := t.promptTime
			if !t.promptTimeOK {
				promptTime = t.replyTime
			}
			if t.prompt != "" {
				s.Messages = append(s.Messages, model.Message{
					Role: "user",
					Text: capParsedMessage(t.prompt),
					Time: time.UnixMilli(promptTime),
				})
			}
			if t.reply != "" {
				rt := t.replyTime
				if !t.replyTimeOK {
					rt = promptTime
				}
				s.Messages = append(s.Messages, model.Message{
					Role: "assistant",
					Text: capParsedMessage(t.reply),
					Time: time.UnixMilli(rt),
				})
			}
		}
		if len(s.Messages) == 0 {
			stats.emptyConversations++
			continue
		}
		if s.Started.IsZero() {
			s.Started = s.Messages[0].Time
		}
		if s.Updated.IsZero() {
			s.Updated = s.Messages[len(s.Messages)-1].Time
		}
		if s.Title == "" {
			for _, m := range s.Messages {
				if m.Role == "user" && m.Text != "" {
					s.Title = m.Text
					break
				}
			}
		}
		sessions = append(sessions, s)
	}
	sort.SliceStable(sessions, func(i, j int) bool {
		return sessions[i].Updated.After(sessions[j].Updated)
	})
	return sessions
}

func doubaoTurnKey(t doubaoTask) int64 {
	switch {
	case t.promptTimeOK:
		return t.promptTime
	case t.replyTimeOK:
		return t.replyTime
	default:
		return math.MaxInt64
	}
}

// parseDoubaoStore reads one IndexedDB LevelDB directory into sessions. An
// unreadable store yields no sessions rather than an error, matching how the
// other store-level readers treat a directory mid-write.
func parseDoubaoStore(dir string) ([]model.Session, doubaoStats) {
	var stats doubaoStats
	records, _, _ := readIndexedDb(dir)
	ss := doubaoSessions(records, dir, &stats)
	return ss, stats
}

// ParseDoubaoDir reads the Doubao chat database held in dir.
func ParseDoubaoDir(dir string) ([]model.Session, error) {
	ss, _ := parseDoubaoStore(dir)
	return ss, nil
}

// ParseDoubaoFile parses the database a LevelDB file belongs to. The walk
// hands over individual files, but the store is the directory: any file's
// change re-reads it whole.
func ParseDoubaoFile(path string) ([]model.Session, error) {
	return ParseDoubaoDir(filepath.Dir(path))
}

// ParseDoubaoFileSince is the incremental read: sessions updated after t.
func ParseDoubaoFileSince(path string, t time.Time) ([]model.Session, error) {
	ss, err := ParseDoubaoFile(path)
	if err != nil || t.IsZero() {
		return ss, err
	}
	var out []model.Session
	for _, s := range ss {
		if s.Updated.After(t) {
			out = append(out, s)
		}
	}
	return out, nil
}

// LoadDoubao reads every Doubao chat database on this machine.
func LoadDoubao() []model.Session {
	dirs := DoubaoIndexedDbDirs()
	out := make([]model.Session, 0, len(dirs))
	for _, dir := range dirs {
		ss, err := ParseDoubaoDir(dir)
		out = append(out, storeRead(dir, ss, err)...)
	}
	return out
}
