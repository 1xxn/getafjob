//go:build e2e

package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// The demo user (admin demo -fixtures) has a profile, a CV and jobs that point at the fixture site, so journeys
// with real runs use it. It signs in with a mail link read from Mailpit, in its own browser context.
var (
	demoOnce    sync.Once
	demoBrowser *rod.Browser
	demoErr     error
	linkRx      = regexp.MustCompile(`https?://[^\s]+/login/link/[A-Za-z0-9_-]+`)
)

// Demo returns the signed-in browser context of the demo user; the journey is skipped without Mailpit.
func Demo(t *testing.T) *rod.Browser {
	t.Helper()
	mailpit := os.Getenv("GETAFJOB_E2E_MAILPIT")
	if mailpit == "" {
		t.Skip("GETAFJOB_E2E_MAILPIT not set: no demo user")
	}
	demoOnce.Do(func() {
		demoBrowser, demoErr = resumeDemo()
		if demoErr != nil || demoBrowser == nil {
			demoBrowser, demoErr = signInDemo(mailpit)
		}
		if demoErr == nil {
			saveDemo(demoBrowser)
		}
	})
	if demoErr != nil {
		t.Fatalf("demo sign in: %v", demoErr)
	}
	return demoBrowser
}

// demoCookies keeps the demo user's session between local runs (bin/e2e, ignored by git), because mail links are
// rate limited. Only the development stack's demo account is stored there.
func demoCookies() string { return filepath.Join(outDir, "..", "demo-session.json") }

// resumeDemo restores a stored session; nil without error when there is none or it expired.
func resumeDemo() (*rod.Browser, error) {
	raw, err := os.ReadFile(demoCookies())
	if err != nil {
		return nil, nil // no stored session: sign in with a mail link
	}
	var cookies []*proto.NetworkCookieParam
	if err := json.Unmarshal(raw, &cookies); err != nil {
		return nil, nil // unreadable: sign in again
	}
	b, err := shared.Incognito()
	if err != nil {
		return nil, err
	}
	if err := b.SetCookies(cookies); err != nil {
		return nil, err
	}
	p, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = p.Close() }()
	p = p.Timeout(30 * time.Second)
	if err := p.Navigate(base + "/"); err != nil {
		return nil, err
	}
	if err := p.WaitLoad(); err != nil {
		return nil, err
	}
	if ok, _, err := p.Has(`body.shell`); err != nil || !ok {
		_ = b.Close()
		return nil, nil // expired session: sign in again
	}
	return b, nil
}

// saveDemo stores the session cookies of the demo context.
func saveDemo(b *rod.Browser) {
	cookies, err := b.GetCookies()
	if err != nil {
		return
	}
	params := make([]*proto.NetworkCookieParam, 0, len(cookies))
	for _, c := range cookies {
		params = append(params, &proto.NetworkCookieParam{Name: c.Name, Value: c.Value, Domain: c.Domain, Path: c.Path,
			Secure: c.Secure, HTTPOnly: c.HTTPOnly, SameSite: c.SameSite, Expires: c.Expires})
	}
	if raw, err := json.Marshal(params); err == nil {
		_ = os.WriteFile(demoCookies(), raw, 0o600)
	}
}

func signInDemo(mailpit string) (*rod.Browser, error) {
	email := os.Getenv("GETAFJOB_E2E_DEMO_EMAIL")
	if email == "" {
		email = "max.mustermann@example.org"
	}
	b, err := shared.Incognito()
	if err != nil {
		return nil, err
	}
	p, err := b.Page(proto.TargetCreateTarget{})
	if err != nil {
		return nil, err
	}
	defer func() { _ = p.Close() }()
	p = p.Timeout(30 * time.Second)
	since := time.Now().Add(-2 * time.Second)
	if err := p.Navigate(base + "/login"); err != nil {
		return nil, err
	}
	field, err := p.Element(`input[name="email"]`)
	if err != nil {
		return nil, err
	}
	if err := field.Input(email); err != nil {
		return nil, err
	}
	if err := submit(p, `main form button[type="submit"]`); err != nil {
		return nil, err
	}
	link, err := mailLink(mailpit, email, since)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(link)
	if err != nil {
		return nil, err
	}
	if err := p.Navigate(base + u.RequestURI()); err != nil {
		return nil, err
	}
	if err := submit(p, `main form button[type="submit"]`); err != nil {
		return nil, err
	}
	if _, err := p.Element(`body.shell`); err != nil {
		return nil, fmt.Errorf("no app shell after the mail link: %w", err)
	}
	return b, nil
}

func submit(p *rod.Page, selector string) error {
	btn, err := p.Element(selector)
	if err != nil {
		return err
	}
	wait := p.WaitNavigation(proto.PageLifecycleEventNameNetworkAlmostIdle)
	if err := btn.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return err
	}
	wait()
	return nil
}

type mailpitList struct {
	Messages []struct {
		ID      string    `json:"ID"`
		Created time.Time `json:"Created"`
		To      []struct {
			Address string `json:"Address"`
		} `json:"To"`
	} `json:"messages"`
}

// mailLink waits for the newest mail to email since the given time and returns its sign-in link.
func mailLink(mailpit, email string, since time.Time) (string, error) {
	c := &http.Client{Timeout: 5 * time.Second}
	get := func(path string, v any) error {
		res, err := c.Get(mailpit + path) // test helper with a client timeout
		if err != nil {
			return err
		}
		defer func() { _ = res.Body.Close() }()
		if res.StatusCode != http.StatusOK {
			return fmt.Errorf("mailpit %s: %s", path, res.Status)
		}
		b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
		if err != nil {
			return err
		}
		return json.Unmarshal(b, v)
	}
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(500 * time.Millisecond) {
		var list mailpitList
		if err := get("/api/v1/messages?limit=20", &list); err != nil {
			return "", err
		}
		for _, m := range list.Messages {
			if m.Created.Before(since) || len(m.To) == 0 || m.To[0].Address != email {
				continue
			}
			var msg struct {
				Text string `json:"Text"`
			}
			if err := get("/api/v1/message/"+m.ID, &msg); err != nil {
				return "", err
			}
			if link := linkRx.FindString(msg.Text); link != "" {
				return link, nil
			}
		}
	}
	return "", fmt.Errorf("no sign-in mail for %s", email)
}
