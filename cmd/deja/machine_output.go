package main

import (
	"encoding/json"
	"io"

	"github.com/vshulcz/deja-vu/internal/jsonout"
	"github.com/vshulcz/deja-vu/internal/model"
)

type recentJSON struct {
	SchemaVersion int             `json:"schema_version"`
	Sessions      []model.Session `json:"sessions"`
}

type sessionWindow struct {
	Offset   int `json:"offset"`
	Limit    int `json:"limit"`
	Total    int `json:"total"`
	Returned int `json:"returned"`
}

type sessionJSON struct {
	SchemaVersion int           `json:"schema_version"`
	Session       model.Session `json:"session"`
	Window        sessionWindow `json:"window"`
}

func printRecentJSON(w io.Writer, sessions []model.Session, sourceInstance string) error {
	for i := range sessions {
		sessions[i].Messages = nil
		sessions[i].SetSource(sourceInstance)
	}
	if sessions == nil {
		sessions = []model.Session{}
	}
	return json.NewEncoder(w).Encode(recentJSON{SchemaVersion: jsonout.Version, Sessions: sessions})
}

// sliceMessages applies --offset and --limit. Both are documented for `deja
// show` and, until #709, only the JSON path honoured them: the human-readable
// output printed the whole session, which on a 200k-message transcript is
// 600 001 lines and exactly what --limit exists to avoid.
func sliceMessages(ms []model.Message, offset, limit int) []model.Message {
	total := len(ms)
	if offset > total {
		offset = total
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return ms[offset:end]
}

func printSessionJSON(w io.Writer, session model.Session, offset, limit int, sourceInstance string) error {
	total := len(session.Messages)
	session.Messages = sliceMessages(session.Messages, offset, limit)
	session.SetSource(sourceInstance)
	return json.NewEncoder(w).Encode(sessionJSON{
		SchemaVersion: jsonout.Version,
		Session:       session,
		Window: sessionWindow{
			Offset: offset, Limit: limit, Total: total, Returned: len(session.Messages),
		},
	})
}
