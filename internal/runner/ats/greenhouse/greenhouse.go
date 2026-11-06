// Package greenhouse reads the questions of a Greenhouse job from the public board API, so that a run knows
// the form before it opens it: unknown required questions are asked before the browser starts.
package greenhouse

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/1xxn/getafjob/internal/platform/htmltext"
	"github.com/1xxn/getafjob/internal/platform/netguard"
	"github.com/1xxn/getafjob/internal/runner/browser"
)

// Base is the public board API.
const Base = "https://boards-api.greenhouse.io/v1/boards/"

// maxBody bounds the answer; a job with all its questions is far smaller.
const maxBody = 2 << 20

var (
	boardRx = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,99}$`)
	jobRx   = regexp.MustCompile(`^[0-9]{1,20}$`)
)

// ErrInvalid reports a board token or job ID that is not one.
var ErrInvalid = errors.New("invalid greenhouse board or job")

// Client fetches job questions.
type Client struct {
	HTTP *http.Client // through the egress proxy (netguard)
	Base string       // default Base; tests use a local server
}

// question is the API's form of a question.
type question struct {
	Label    string `json:"label"`
	Required bool   `json:"required"`
	Fields   []struct {
		Name   string `json:"name"`
		Type   string `json:"type"`
		Values []struct {
			Label string `json:"label"`
		} `json:"values"`
	} `json:"fields"`
}

type job struct {
	Questions         []question `json:"questions"`
	LocationQuestions []question `json:"location_questions"`
	Compliance        []struct {
		Questions []question `json:"questions"`
	} `json:"compliance"`
}

// kinds maps the API's field types to the extraction's field kinds.
var kinds = map[string]string{
	"input_text": "text", "textarea": "textarea", "input_file": "file",
	"multi_value_single_select": "select", "multi_value_multi_select": "checkbox_group",
}

// Questions returns the job's form questions as fields (no IDs: they are not on a page yet).
func (c *Client) Questions(ctx context.Context, board, jobID string) ([]browser.Field, error) {
	if !boardRx.MatchString(board) || !jobRx.MatchString(jobID) {
		return nil, fmt.Errorf("%w: %q/%q", ErrInvalid, board, jobID)
	}
	base := c.Base
	if base == "" {
		base = Base
	}
	u := base + url.PathEscape(board) + "/jobs/" + jobID + "?questions=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("greenhouse %s/%s: http %d", board, jobID, resp.StatusCode)
	}
	b, err := netguard.ReadLimited(resp.Body, maxBody)
	if err != nil {
		return nil, err
	}
	var j job
	if err := json.Unmarshal(b, &j); err != nil {
		return nil, fmt.Errorf("greenhouse %s/%s: %w", board, jobID, err)
	}
	all := append(append([]question{}, j.Questions...), j.LocationQuestions...)
	for _, c := range j.Compliance {
		all = append(all, c.Questions...)
	}
	var out []browser.Field
	for _, q := range all {
		if len(out) >= 200 {
			break
		}
		if len(q.Fields) == 0 {
			continue
		}
		f := q.Fields[0] // "Resume/CV" offers a file or a text; the file comes first
		kind, ok := kinds[f.Type]
		if !ok {
			continue // hidden fields
		}
		field := browser.Field{Kind: kind, Label: clean(q.Label), Required: q.Required, Name: f.Name}
		for _, v := range f.Values {
			field.Options = append(field.Options, browser.Option{Label: clean(v.Label)})
		}
		out = append(out, field)
	}
	return out, nil
}

// clean turns question labels (they may carry HTML) into plain text.
func clean(s string) string {
	t := htmltext.Text(s)
	if len(t) > 300 {
		t = t[:300]
	}
	return strings.TrimSpace(t)
}
