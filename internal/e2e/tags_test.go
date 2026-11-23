//go:build e2e

package e2e

import (
	"strconv"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/input"
)

// List fields are one-line tag inputs (#597): Enter adds a chip, the hidden textarea holds one entry per line for
// the form and autosave, and a chip's button removes it again.
func TestTagInput(t *testing.T) {
	tab := OpenIn(t, Demo(t), 1440, 900, "light")
	tab.Go("/profile")
	box := `[data-tags-box="skills"]`
	tab.El(box)
	skill := "Zig" + strconv.FormatInt(time.Now().UnixNano()%100000, 10)
	field := tab.El(`[data-tags-input="skills"]`)
	must(t, field.Input(skill))
	must(t, field.Type(input.Enter))
	tab.WaitFor(`document.querySelector('` + box + ` .ui-tag[data-tag="` + skill + `"]') !== null`)
	tab.WaitWithin(10*time.Second, `document.querySelector('textarea[name="skills"]').value.split('\n').includes('`+skill+`') && document.querySelector('[data-save-state]')?.dataset.saveState === 'saved'`)
	tab.Axe("/profile tag input")

	tab.Click(box + ` .ui-tag[data-tag="` + skill + `"] .ui-tag-remove`)
	tab.WaitWithin(10*time.Second, `!document.querySelector('textarea[name="skills"]').value.split('\n').includes('`+skill+`')`)
}
