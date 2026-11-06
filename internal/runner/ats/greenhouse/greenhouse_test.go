package greenhouse

import (
	_ "embed"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The fixture is embedded: the browser suite runs the test binaries without the source tree.
//
//go:embed testdata/job.json
var body []byte

func server(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/muster/jobs/4012345":
			if r.URL.Query().Get("questions") != "true" {
				http.Error(w, "no questions", http.StatusBadRequest)
				return
			}
			_, _ = w.Write(body)
		case "/muster/jobs/1":
			_, _ = w.Write([]byte(`{"questions": [` + strings.Repeat(`{"label":"x"},`, 300000) + `{}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestQuestions(t *testing.T) {
	srv := server(t)
	c := &Client{HTTP: srv.Client(), Base: srv.URL + "/"}
	fs, err := c.Questions(t.Context(), "muster", "4012345")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, f := range fs {
		got = append(got, f.Kind+":"+f.Label)
	}
	want := "text:First Name|text:Last Name|text:Email|file:Resume/CV|text:LinkedIn Profile|" +
		"select:Will you now or in the future require visa sponsorship?|select:Which of our values speaks to you most?|" +
		"text:Location (City)|select:Gender"
	if strings.Join(got, "|") != want {
		t.Fatalf("fields = %s", strings.Join(got, "|"))
	}
	if !fs[0].Required || fs[4].Required || len(fs[8].Options) != 3 || fs[5].Options[1].Label != "No" {
		t.Fatalf("details: %+v", fs)
	}
}

func TestQuestionsRefuses(t *testing.T) {
	srv := server(t)
	c := &Client{HTTP: srv.Client(), Base: srv.URL + "/"}
	for _, tc := range []struct{ board, job string }{
		{"../admin", "1"}, {"Muster", "1"}, {"muster", "1/../2"}, {"muster", "abc"}, {"", "1"}, {"muster", ""},
	} {
		if _, err := c.Questions(t.Context(), tc.board, tc.job); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q/%q: %v", tc.board, tc.job, err)
		}
	}
	if _, err := c.Questions(t.Context(), "muster", "999"); err == nil || !strings.Contains(err.Error(), "404") {
		t.Errorf("missing job: %v", err)
	}
	if _, err := c.Questions(t.Context(), "muster", "1"); err == nil {
		t.Error("oversized answer accepted")
	}
}
