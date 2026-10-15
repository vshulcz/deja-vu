package embed

import (
	"context"
	"sync"

	"github.com/vshulcz/deja-vu/internal/redact"
)

// queryMemoSize bounds the vectors kept per process. A recall ranks the same
// query twice, the whole machine and then the project, and the MCP server
// lives across many calls; a handful covers the first and keeps the second
// small.
const queryMemoSize = 16

var queryMemo = struct {
	sync.Mutex
	order []string
	vecs  map[string][]float32
}{vecs: map[string][]float32{}}

// EmbedQuery is the vector for a search query, the one piece of a search that
// goes to the endpoint.
//
// The query is redacted by the same pass a record goes through before it is
// indexed: a record's secret never reaches the endpoint, and a query that
// pastes the same token sent it verbatim (#5053). The vector is kept per
// endpoint and model, so a recall that ranks twice asks once.
func (c *Client) EmbedQuery(ctx context.Context, query string) ([]float32, error) {
	text, _ := redact.Text(query)
	key := c.URL + "\x00" + c.Model + "\x00" + text
	queryMemo.Lock()
	v, ok := queryMemo.vecs[key]
	queryMemo.Unlock()
	if ok {
		return v, nil
	}
	out, err := c.Embed(ctx, []string{text})
	if err != nil {
		return nil, err
	}
	if len(out) != 1 {
		return nil, errBadQueryVector
	}
	queryMemo.Lock()
	defer queryMemo.Unlock()
	if _, ok := queryMemo.vecs[key]; !ok {
		if len(queryMemo.order) >= queryMemoSize {
			delete(queryMemo.vecs, queryMemo.order[0])
			queryMemo.order = queryMemo.order[1:]
		}
		queryMemo.order = append(queryMemo.order, key)
	}
	queryMemo.vecs[key] = out[0]
	return out[0], nil
}
