package sources

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vshulcz/deja-vu/internal/model"
)

// Windsurf, now shipped as Devin Desktop, keeps each Cascade conversation as
// one encrypted protobuf under its config root (#308):
//
//	~/.codeium/windsurf/cascade/<cascade-id>.pb   a CortexTrajectory
//	~/.codeium/windsurf/implicit/<id>.pb          context trajectories, not read
//	~/.codeium/windsurf/brain/                    plans and other state, not read
//
// The file is nonce(12) || ciphertext || tag(16), AES-256-GCM with no
// additional data and a key the language server carries as a constant. A file
// that fails authentication is skipped whole and counted, never decoded: what
// GCM refuses is not the conversation, and indexing it would put garbage in
// recall. Field numbers are the generated descriptors' in Devin Desktop 3.5.17:
//
//	CortexTrajectory      1 trajectory_id, 2 steps, 6 cascade_id, 7 metadata,
//	                      10 renamed_title
//	…Metadata             1 workspaces, 2 created_at
//	…WorkspaceMetadata    1 workspace_folder_absolute_uri, 2 git_root_absolute_uri
//	CortexTrajectoryStep  5 metadata, 19 user_input, 20 planner_response
//	CortexStepMetadata    1 created_at, 8 completed_at
//	CortexStepUserInput   1 query, 2 user_response
//	…PlannerResponse      1 response, 3 thinking, 8 modified_response

// WindsurfRoot is the Cascade config root, ~/.codeium/windsurf on every OS.
// DEJA_WINDSURF_ROOT replaces it.
func WindsurfRoot() string {
	return EnvPath("DEJA_WINDSURF_ROOT", filepath.Join(Home(), ".codeium", "windsurf"))
}

// WindsurfSessionFiles lists the explicit Cascade conversations.
func WindsurfSessionFiles() []string {
	files, _ := filepath.Glob(filepath.Join(WindsurfRoot(), "cascade", "*.pb"))
	return files
}

func isWindsurfTrajectory(p string) bool {
	return filepath.Ext(p) == ".pb" && filepath.Dir(p) == filepath.Join(WindsurfRoot(), "cascade")
}

func LoadWindsurf() []model.Session {
	return parseFiles(WindsurfSessionFiles(), ParseWindsurfFile)
}

// errWindsurfAuth is a trajectory GCM would not open: tampered, truncated, or
// written with a key this build does not know.
var errWindsurfAuth = errors.New("trajectory failed authentication")

var windsurfKey = []byte("safeCodeiumworldKeYsecretBalloon")

const windsurfNonce, windsurfTag = 12, 16

func windsurfOpen(b []byte) ([]byte, error) {
	if len(b) < windsurfNonce+windsurfTag {
		return nil, errWindsurfAuth
	}
	block, err := aes.NewCipher(windsurfKey)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, b[:windsurfNonce], b[windsurfNonce:], nil)
	if err != nil {
		return nil, errWindsurfAuth
	}
	return plain, nil
}

// windsurfAuthSeen remembers each file's verdict by size and mtime, so doctor
// and the index run asking for the count do not decrypt the store twice.
var (
	windsurfAuthMu   sync.Mutex
	windsurfAuthSeen = map[string]windsurfAuthVerdict{}
)

type windsurfAuthVerdict struct {
	size, stamp int64
	failed      bool
}

// WindsurfAuthFailures counts the trajectories GCM refuses. They are out of
// recall, and a store where some are reads the same as a shorter history
// unless something says so (#474).
func WindsurfAuthFailures() int {
	n := 0
	for _, p := range WindsurfSessionFiles() {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		windsurfAuthMu.Lock()
		v, ok := windsurfAuthSeen[p]
		windsurfAuthMu.Unlock()
		if !ok || v.size != fi.Size() || v.stamp != fi.ModTime().UnixNano() {
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			_, err = windsurfOpen(b)
			v = windsurfAuthVerdict{size: fi.Size(), stamp: fi.ModTime().UnixNano(), failed: errors.Is(err, errWindsurfAuth)}
			windsurfAuthMu.Lock()
			windsurfAuthSeen[p] = v
			windsurfAuthMu.Unlock()
		}
		if v.failed {
			n++
		}
	}
	return n
}

func windsurfSkipReason() string {
	n := WindsurfAuthFailures()
	if n == 0 {
		return ""
	}
	if n == 1 {
		return "1 trajectory failed authentication"
	}
	return fmt.Sprintf("%d trajectories failed authentication", n)
}

type windsurfTrajectory struct {
	cascadeID, title  string
	workspace, gitDir string
	created           time.Time
	steps             []windsurfStep
}

type windsurfStep struct {
	created, completed  time.Time
	user, query         string
	response, rewritten string
}

// ParseWindsurfFile reads one Cascade trajectory: the typed prompts and the
// planner's replies. Thinking, tool calls, edits and terminal state are left
// out.
func ParseWindsurfFile(path string) ([]model.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	// One trajectory per file, as in cline: a file that will not open or
	// decode is a path deja could not read, not a line (#2232).
	plain, err := windsurfOpen(b)
	if err != nil {
		diagFileError(path, err)
		return nil, nil
	}
	tr, err := decodeWindsurfTrajectory(plain)
	if err != nil {
		diagFileError(path, fmt.Errorf("decode trajectory: %w", err))
		return nil, nil
	}
	id := tr.cascadeID
	if id == "" {
		id = strings.TrimSuffix(filepath.Base(path), ".pb")
	}
	s := model.Session{Harness: "windsurf", ID: id, Path: path, Title: firstLineTrim(tr.title)}
	// The git root names the project better than the folder opened, which
	// can be a subdirectory of it.
	if dir := tr.gitDir; dir != "" {
		s.Project = projectName(dir)
	} else if tr.workspace != "" {
		s.Project = projectName(tr.workspace)
	}
	s.Touch(tr.created)
	// A step with no clock of its own takes the one before it, and the
	// trajectory's; the file's mtime only when neither has one.
	at := tr.created
	if at.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			at = fi.ModTime()
		}
	}
	for _, st := range tr.steps {
		if !st.created.IsZero() {
			at = st.created
		}
		// user_response is what a typed prompt fills on 3.5.17; query is
		// kept for builds that wrote it there.
		text := strings.TrimSpace(st.user)
		if text == "" {
			text = strings.TrimSpace(st.query)
		}
		if text != "" {
			s.Touch(at)
			s.Messages = append(s.Messages, model.Message{Role: "user", Text: text, Time: at})
			if s.Title == "" {
				s.Title = firstLineTrim(text)
			}
		}
		// response first, modified_response when it is empty. The precedence
		// is unverified against a successful turn: the only live trajectory
		// examined so far was refused before the model answered (#308).
		text = strings.TrimSpace(st.response)
		if text == "" {
			text = strings.TrimSpace(st.rewritten)
		}
		if text != "" {
			if !st.completed.IsZero() {
				at = st.completed
			}
			s.Touch(at)
			s.Messages = append(s.Messages, model.Message{Role: "assistant", Text: text, Time: at})
		}
	}
	if len(s.Messages) == 0 {
		return nil, nil
	}
	return []model.Session{s}, nil
}

func decodeWindsurfTrajectory(b []byte) (windsurfTrajectory, error) {
	var tr windsurfTrajectory
	err := pbWalk(b, func(num uint64, wire int, _ uint64, data []byte) error {
		if wire != pbBytes {
			return nil
		}
		switch num {
		case 2:
			st, err := decodeWindsurfStep(data)
			if err != nil {
				return err
			}
			tr.steps = append(tr.steps, st)
		case 6:
			tr.cascadeID = pbString(data)
		case 7:
			return decodeWindsurfMetadata(data, &tr)
		case 10:
			tr.title = pbString(data)
		}
		return nil
	})
	return tr, err
}

func decodeWindsurfMetadata(b []byte, tr *windsurfTrajectory) error {
	return pbWalk(b, func(num uint64, wire int, _ uint64, data []byte) error {
		if wire != pbBytes {
			return nil
		}
		switch num {
		case 1:
			// The first workspace that names a folder; a git root beside it
			// wins.
			if tr.workspace != "" {
				return nil
			}
			var folder, git string
			if err := pbWalk(data, func(num uint64, wire int, _ uint64, data []byte) error {
				if wire == pbBytes && num == 1 {
					folder = pbString(data)
				} else if wire == pbBytes && num == 2 {
					git = pbString(data)
				}
				return nil
			}); err != nil {
				return err
			}
			if folder != "" {
				tr.workspace, tr.gitDir = folder, git
			}
		case 2:
			t, err := pbTimestamp(data)
			tr.created = t
			return err
		}
		return nil
	})
}

func decodeWindsurfStep(b []byte) (windsurfStep, error) {
	var st windsurfStep
	err := pbWalk(b, func(num uint64, wire int, _ uint64, data []byte) error {
		if wire != pbBytes {
			return nil
		}
		switch num {
		case 5:
			return pbWalk(data, func(num uint64, wire int, _ uint64, data []byte) error {
				var err error
				if wire == pbBytes && num == 1 {
					st.created, err = pbTimestamp(data)
				} else if wire == pbBytes && num == 8 {
					st.completed, err = pbTimestamp(data)
				}
				return err
			})
		case 19:
			return pbWalk(data, func(num uint64, wire int, _ uint64, data []byte) error {
				if wire == pbBytes && num == 1 {
					st.query = pbString(data)
				} else if wire == pbBytes && num == 2 {
					st.user = pbString(data)
				}
				return nil
			})
		case 20:
			return pbWalk(data, func(num uint64, wire int, _ uint64, data []byte) error {
				if wire == pbBytes && num == 1 {
					st.response = pbString(data)
				} else if wire == pbBytes && num == 8 {
					st.rewritten = pbString(data)
				}
				return nil
			})
		}
		return nil
	})
	return st, err
}

// Protobuf wire types. Groups (3 and 4) are not in the trajectory schema and
// read as a malformed message.
const (
	pbVarint  = 0
	pbFixed64 = 1
	pbBytes   = 2
	pbFixed32 = 5
)

var errPBMalformed = errors.New("malformed protobuf")

// pbWalk calls fn for each field of one protobuf message, in wire order:
// varints and fixed values in v, length-delimited fields in data. A field fn
// does not know is skipped by its wire type, which is what lets the reader
// outlive a schema that grows.
func pbWalk(b []byte, fn func(num uint64, wire int, v uint64, data []byte) error) error {
	for len(b) > 0 {
		key, n := binary.Uvarint(b)
		if n <= 0 || key>>3 == 0 {
			return errPBMalformed
		}
		b = b[n:]
		num, wire := key>>3, int(key&7)
		var v uint64
		var data []byte
		switch wire {
		case pbVarint:
			if v, n = binary.Uvarint(b); n <= 0 {
				return errPBMalformed
			}
			b = b[n:]
		case pbFixed64:
			if len(b) < 8 {
				return errPBMalformed
			}
			v, b = binary.LittleEndian.Uint64(b), b[8:]
		case pbFixed32:
			if len(b) < 4 {
				return errPBMalformed
			}
			v, b = uint64(binary.LittleEndian.Uint32(b)), b[4:]
		case pbBytes:
			l, n := binary.Uvarint(b)
			if n <= 0 || l > uint64(len(b)-n) {
				return errPBMalformed
			}
			data, b = b[n:n+int(l)], b[n+int(l):]
		default:
			return errPBMalformed
		}
		if err := fn(num, wire, v, data); err != nil {
			return err
		}
	}
	return nil
}

// pbString is a string field as text: protobuf lets a NUL or a broken byte
// into one, and nothing downstream should inherit it.
func pbString(b []byte) string {
	s := string(b)
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "�")
	}
	return strings.ReplaceAll(s, "\x00", "")
}

// pbTimestamp reads a google.protobuf.Timestamp: 1 seconds, 2 nanos.
func pbTimestamp(b []byte) (time.Time, error) {
	var sec, nsec int64
	err := pbWalk(b, func(num uint64, wire int, v uint64, _ []byte) error {
		if wire != pbVarint {
			return nil
		}
		switch num {
		case 1:
			sec = int64(v)
		case 2:
			nsec = int64(int32(v))
		}
		return nil
	})
	if err != nil || (sec == 0 && nsec == 0) {
		return time.Time{}, err
	}
	return time.Unix(sec, nsec), nil
}
