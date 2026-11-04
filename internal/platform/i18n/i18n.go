// Package i18n holds the message catalogs (English default, German) for everything shown to people: UI and mail.
//
// Catalogs are flat JSON maps from key to message with {name} placeholders. Texts created in the backend travel
// as a Msg (code plus params) and are rendered in the reader's language.
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Locale is a supported language.
type Locale string

// Supported locales; English is the default.
const (
	EN Locale = "en"
	DE Locale = "de"
)

// Default is used when no other locale matches.
const Default = EN

// Locales lists all supported locales.
var Locales = []Locale{EN, DE}

//go:embed catalog/*.json
var files embed.FS

var catalogs = map[Locale]map[string]string{}

func init() {
	for _, l := range Locales {
		b, err := files.ReadFile("catalog/" + string(l) + ".json")
		if err != nil {
			panic(err)
		}
		m := map[string]string{}
		if err := json.Unmarshal(b, &m); err != nil {
			panic(fmt.Sprintf("catalog %s: %v", l, err))
		}
		catalogs[l] = m
	}
}

// Parse returns the locale for a tag like "de-DE" or "en", or the default.
func Parse(tag string) Locale {
	base, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(tag)), "-")
	for _, l := range Locales {
		if string(l) == base {
			return l
		}
	}
	return Default
}

// FromAcceptLanguage picks the best supported locale from an Accept-Language header (weights respected).
func FromAcceptLanguage(header string) Locale {
	best, bestQ := Default, -1.0
	for _, part := range strings.Split(header, ",") {
		tag, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		q := 1.0
		if v, ok := strings.CutPrefix(strings.TrimSpace(params), "q="); ok {
			if _, err := fmt.Sscanf(v, "%f", &q); err != nil {
				continue
			}
		}
		base, _, _ := strings.Cut(strings.ToLower(tag), "-")
		for _, l := range Locales {
			if string(l) == base && q > bestQ {
				best, bestQ = l, q
			}
		}
	}
	return best
}

// T renders key in locale l with params; missing keys fall back to English, then to the key itself.
func T(l Locale, key string, params map[string]any) string {
	msg, ok := catalogs[l][key]
	if !ok {
		msg, ok = catalogs[Default][key]
	}
	if !ok {
		return key
	}
	return fill(msg, params)
}

// fill replaces {name} placeholders; unknown placeholders stay visible so they are noticed.
func fill(msg string, params map[string]any) string {
	if len(params) == 0 || !strings.Contains(msg, "{") {
		return msg
	}
	var b strings.Builder
	for {
		i := strings.IndexByte(msg, '{')
		if i < 0 {
			b.WriteString(msg)
			return b.String()
		}
		j := strings.IndexByte(msg[i:], '}')
		if j < 0 {
			b.WriteString(msg)
			return b.String()
		}
		name := msg[i+1 : i+j]
		b.WriteString(msg[:i])
		if v, ok := params[name]; ok {
			fmt.Fprint(&b, v)
		} else {
			b.WriteString(msg[i : i+j+1])
		}
		msg = msg[i+j+1:]
	}
}

// Msg is a backend text: a stable code plus parameters, rendered in the reader's language.
type Msg struct {
	Code   string         `json:"code"`
	Params map[string]any `json:"params,omitempty"`
}

// Render returns the message text for l (catalog key "msg.<code>").
func (m Msg) Render(l Locale) string { return T(l, "msg."+m.Code, m.Params) }

// placeholders returns the sorted placeholder names of a message.
func placeholders(msg string) []string {
	var out []string
	for {
		i := strings.IndexByte(msg, '{')
		if i < 0 {
			break
		}
		j := strings.IndexByte(msg[i:], '}')
		if j < 0 {
			break
		}
		out = append(out, msg[i+1:i+j])
		msg = msg[i+j+1:]
	}
	sort.Strings(out)
	return out
}
