package sources

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/1xxn/getafjob/internal/discovery"
)

//go:embed catalog.json
var catalogJSON []byte

// Catalog returns the built-in boards: tech employers with remote or Germany jobs, each checked against its live
// API (TestCatalog, nightly). Google, Apple and amazon.jobs are sources of their own (NewGoogle, NewApple, NewAmazon).
func Catalog() ([]Board, error) {
	var c struct {
		Boards []Board `json:"boards"`
	}
	if err := json.Unmarshal(catalogJSON, &c); err != nil {
		return nil, fmt.Errorf("catalog: %w", err)
	}
	return c.Boards, nil
}

var (
	inGermany = regexp.MustCompile(`(?i)(\b(germany|deutschland|berlin|hamburg|frankfurt|cologne|stuttgart|leipzig|dresden|hannover|` +
		`hanover|nuremberg|karlsruhe|bonn|potsdam|essen|dortmund|mannheim|heidelberg|freiburg|aachen|munich|deu)\b|` +
		`münchen|muenchen|köln|koeln|düsseldorf|duesseldorf|dusseldorf|nürnberg|(,|^)\s*de\s*($|[,;)]))`)
	remoteRegion = regexp.MustCompile(`(?i)\b(europe|european|emea|eu|eea|dach|worldwide|anywhere|global|international|cet|cest)\b`)
	remoteWords  = regexp.MustCompile(`(?i)\b(remote|fully|first|friendly|home office|homeoffice|work from home|wfh|flexible|or|and|only|based)\b|100%|[-\x{2013},;:/|()\[\]\x{2022}.]+`)
)

// ForGermany reports whether a posting is in Germany or remote and open to Germany: remote without a region, or
// remote for Europe, EMEA or worldwide. "Remote, Spain" or "Remote - US" is not.
func ForGermany(p discovery.Posting) bool {
	loc := p.Location
	if inGermany.MatchString(loc) {
		return true
	}
	if p.Workplace != discovery.WorkplaceRemote && !strings.Contains(strings.ToLower(loc), "remote") {
		return false
	}
	if remoteRegion.MatchString(loc) {
		return true
	}
	return strings.TrimSpace(remoteWords.ReplaceAllString(loc, " ")) == ""
}
