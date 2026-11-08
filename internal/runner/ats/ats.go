// Package ats provides one adapter per supported ATS on top of the shared runner engine and tracks measured
// success rate (maturity) per adapter.
package ats

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/1xxn/getafjob/internal/runner/ats/greenhouse"
	"github.com/1xxn/getafjob/internal/runner/browser"
	"github.com/1xxn/getafjob/internal/runner/engine"
	"github.com/1xxn/getafjob/internal/runner/executor"
)

// Adapter names: eight primary ATS adapters, four secondary adapters, and the generic fallback.
const (
	Greenhouse      = "greenhouse"
	Lever           = "lever"
	Ashby           = "ashby"
	Workday         = "workday"
	SmartRecruiters = "smartrecruiters"
	Personio        = "personio"
	Recruitee       = "recruitee"
	Workable        = "workable"
	ICIMS           = "icims"
	Taleo           = "taleo"
	SuccessFactors  = "successfactors"
	Teamtailor      = "teamtailor"
	Generic         = "generic"
)

// Adapter configures a run for one ATS on top of the shared engine, planner, and executor.
type Adapter struct {
	Name         string
	Tier         int // 1: primary eight, 2: secondary four, 0: generic
	Hosts        []string
	HostSuffixes []string
	SlugHosts    func(slug string) []string
	precheck     func(ctx context.Context, client *http.Client, rawURL string) ([]browser.Field, error)
}

var adapters = []*Adapter{
	{
		Name:         Greenhouse,
		Tier:         1,
		Hosts:        []string{"boards.greenhouse.io", "job-boards.greenhouse.io", "job-boards.eu.greenhouse.io"},
		HostSuffixes: []string{".greenhouse.io"},
		precheck:     precheckGreenhouse,
	},
	{
		Name:         Lever,
		Tier:         1,
		Hosts:        []string{"jobs.lever.co", "jobs.eu.lever.co"},
		HostSuffixes: []string{".lever.co"},
	},
	{
		Name:         Ashby,
		Tier:         1,
		Hosts:        []string{"jobs.ashbyhq.com"},
		HostSuffixes: []string{".ashbyhq.com"},
	},
	{
		Name:         Workday,
		Tier:         1,
		HostSuffixes: []string{".myworkdayjobs.com", ".myworkdaysite.com"},
	},
	{
		Name:         SmartRecruiters,
		Tier:         1,
		Hosts:        []string{"jobs.smartrecruiters.com", "careers.smartrecruiters.com"},
		HostSuffixes: []string{".smartrecruiters.com"},
	},
	{
		Name:         Personio,
		Tier:         1,
		HostSuffixes: []string{".jobs.personio.de", ".jobs.personio.com", ".personio.de", ".personio.com"},
		SlugHosts: func(slug string) []string {
			return []string{slug + ".jobs.personio.de", slug + ".jobs.personio.com"}
		},
	},
	{
		Name:         Recruitee,
		Tier:         1,
		HostSuffixes: []string{".recruitee.com"},
		SlugHosts: func(slug string) []string {
			return []string{slug + ".recruitee.com"}
		},
	},
	{
		Name:         Workable,
		Tier:         1,
		Hosts:        []string{"apply.workable.com", "jobs.workable.com"},
		HostSuffixes: []string{".workable.com"},
	},
	{
		Name:         ICIMS,
		Tier:         2,
		HostSuffixes: []string{".icims.com"},
		SlugHosts: func(slug string) []string {
			return []string{slug + ".icims.com", "careers-" + slug + ".icims.com", "jobs-" + slug + ".icims.com"}
		},
	},
	{
		Name:         Taleo,
		Tier:         2,
		HostSuffixes: []string{".taleo.net"},
		SlugHosts: func(slug string) []string {
			return []string{slug + ".taleo.net"}
		},
	},
	{
		Name:         SuccessFactors,
		Tier:         2,
		Hosts:        []string{"career.successfactors.eu", "career.successfactors.com"},
		HostSuffixes: []string{".successfactors.com", ".successfactors.eu", ".sapsf.com", ".sapsf.eu"},
	},
	{
		Name:         Teamtailor,
		Tier:         2,
		Hosts:        []string{"career.teamtailor.com"},
		HostSuffixes: []string{".teamtailor.com"},
		SlugHosts: func(slug string) []string {
			return []string{slug + ".teamtailor.com"}
		},
	},
}

var genericAdapter = &Adapter{Name: Generic, Tier: 0}

// All returns the ordered list of all adapter names (tier 1, tier 2, and generic).
func All() []string {
	out := make([]string, 0, len(adapters)+1)
	for _, a := range adapters {
		out = append(out, a.Name)
	}
	return append(out, Generic)
}

// Lookup returns the adapter with the given name, or the generic adapter if unknown.
func Lookup(name string) *Adapter {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, a := range adapters {
		if a.Name == name {
			return a
		}
	}
	return genericAdapter
}

// For returns the adapter matching a job's source (e.g. "greenhouse:acme") or URLs, falling back to generic.
func For(source string, urls ...string) *Adapter {
	if kind, _, ok := strings.Cut(strings.ToLower(strings.TrimSpace(source)), ":"); ok {
		if a := Lookup(kind); a.Name != Generic {
			return a
		}
	} else if a := Lookup(source); a.Name != Generic {
		return a
	}
	for _, raw := range urls {
		u, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || u.Host == "" {
			continue
		}
		host := strings.ToLower(u.Hostname())
		for _, a := range adapters {
			if a.MatchHost(host) {
				return a
			}
		}
	}
	return genericAdapter
}

// Detect returns the canonical adapter name for a job's source and URLs.
func Detect(source string, urls ...string) string {
	return For(source, urls...).Name
}

// MatchHost reports whether host belongs to the adapter.
func (a *Adapter) MatchHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "" {
		return false
	}
	if slices.Contains(a.Hosts, host) {
		return true
	}
	for _, suf := range a.HostSuffixes {
		if strings.HasSuffix(host, suf) || host == strings.TrimPrefix(suf, ".") {
			return true
		}
	}
	return false
}

// MatchURL reports whether rawURL points to a host of this adapter.
func (a *Adapter) MatchURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Host == "" {
		return false
	}
	return a.MatchHost(u.Hostname())
}

// NewEngine creates an Engine configured for this adapter on top of the shared engine, planner, and executor.
func (a *Adapter) NewEngine(exec *executor.Executor, pl engine.Planner, coord engine.Coordinator, wait time.Duration) *engine.Engine {
	return &engine.Engine{
		Exec:    exec,
		Planner: pl,
		Coord:   coord,
		Wait:    wait,
	}
}

// Precheck fetches form questions ahead of opening the browser when the ATS provides a public schema API.
func (a *Adapter) Precheck(ctx context.Context, client *http.Client, rawURL string) ([]browser.Field, error) {
	if a == nil || a.precheck == nil {
		return nil, nil
	}
	return a.precheck(ctx, client, rawURL)
}

func precheckGreenhouse(ctx context.Context, client *http.Client, rawURL string) ([]browser.Field, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	if !slices.Contains([]string{"boards.greenhouse.io", "job-boards.greenhouse.io", "job-boards.eu.greenhouse.io"}, strings.ToLower(u.Hostname())) {
		return nil, nil
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 || parts[1] != "jobs" {
		return nil, nil
	}
	qs, err := (&greenhouse.Client{HTTP: client}).Questions(ctx, parts[0], parts[2])
	if errors.Is(err, greenhouse.ErrInvalid) {
		return nil, nil
	}
	return qs, err
}

// Origins returns the HTTPS origins where the run may fill and submit fields: the job's own HTTPS hosts plus
// the ATS hosts when the job source names an ATS board ("<ats>:<slug>").
func Origins(source string, urls ...string) []string {
	var out []string
	add := func(host string) {
		host = strings.ToLower(strings.TrimSpace(host))
		if host == "" {
			return
		}
		o := "https://" + host
		if !slices.Contains(out, o) {
			out = append(out, o)
		}
	}
	for _, raw := range urls {
		if u, err := url.Parse(raw); err == nil && strings.EqualFold(u.Scheme, "https") && u.Host != "" {
			add(u.Host)
		}
	}
	if kind, slug, ok := strings.Cut(strings.ToLower(strings.TrimSpace(source)), ":"); ok {
		a := Lookup(kind)
		for _, h := range a.Hosts {
			add(h)
		}
		slug = cleanSlug(slug)
		if slug != "" && a.SlugHosts != nil {
			for _, h := range a.SlugHosts(slug) {
				add(h)
			}
		}
	}
	return out
}

func cleanSlug(slug string) string {
	slug, _, _ = strings.Cut(strings.TrimSpace(slug), "/")
	for _, r := range slug {
		if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
			return ""
		}
	}
	return slug
}
