//go:build e2e

package e2e

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"strings"

	"github.com/go-rod/rod/lib/proto"
)

// axe-core 4.10.3 (MPL-2.0, testdata/axe/LICENSE) from the npm registry; used by the journeys only, never served.
//
//go:embed testdata/axe/axe.min.js
var axeSource string

const axeSHA256 = "880970c081707360e64f34cea25ff91892f5bc95675b0776925b9709dd8a68bb"

// axeTags are the rule sets checked: WCAG 2.0 to 2.2, levels A and AA.
const axeTags = `['wcag2a', 'wcag2aa', 'wcag21a', 'wcag21aa', 'wcag22aa']`

type axeViolation struct {
	ID     string `json:"id"`
	Impact string `json:"impact"`
	Nodes  []struct {
		Target []string `json:"target"`
	} `json:"nodes"`
}

// Axe runs axe-core on the page and records every WCAG A/AA violation as a problem of the journey. The script goes
// in through the DevTools protocol, so the page's content security policy stays as it is.
func (tab *Tab) Axe(what string) {
	tab.t.Helper()
	if sum := sha256.Sum256([]byte(axeSource)); hex.EncodeToString(sum[:]) != axeSHA256 {
		tab.t.Fatal("axe-core does not match its pinned hash")
	}
	if tab.Eval("return typeof window.axe") != `"object"` {
		if _, err := (proto.RuntimeEvaluate{Expression: axeSource}).Call(tab.Page); err != nil {
			tab.t.Fatalf("load axe: %v", err)
		}
	}
	raw := tab.EvalAsync(`const r = await axe.run(document, {runOnly: {type: 'tag', values: ` + axeTags + `}, resultTypes: ['violations']});
		return JSON.stringify(r.violations.map(v => ({id: v.id, impact: v.impact, nodes: v.nodes.slice(0, 3).map(n => ({target: n.target}))})));`)
	var s string
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		tab.t.Fatalf("axe result: %v", err)
	}
	var vs []axeViolation
	if err := json.Unmarshal([]byte(s), &vs); err != nil {
		tab.t.Fatalf("axe result: %v", err)
	}
	for _, v := range vs {
		var targets []string
		for _, n := range v.Nodes {
			targets = append(targets, strings.Join(n.Target, " "))
		}
		tab.problem("axe " + what + ": " + v.ID + " (" + v.Impact + ") at " + strings.Join(targets, ", "))
	}
}
