package ntfy_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/1xxn/getafjob/internal/notify"
	"github.com/1xxn/getafjob/internal/notify/adapters/ntfy"
)

func TestSendNtfy(t *testing.T) {
	var (
		receivedMethod string
		receivedPath   string
		receivedTitle  string
		receivedPrio   string
		receivedClick  string
		receivedTags   string
		receivedAuth   string
		receivedBody   string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		receivedPath = r.URL.Path
		receivedTitle = r.Header.Get("Title")
		receivedPrio = r.Header.Get("Priority")
		receivedClick = r.Header.Get("Click")
		receivedTags = r.Header.Get("Tags")
		receivedAuth = r.Header.Get("Authorization")

		body, _ := io.ReadAll(r.Body)
		receivedBody = string(body)

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := ntfy.NewClientWithHTTP(server.Client())
	ctx := context.Background()

	n := notify.Notification{
		UserID:    uuid.New(),
		Event:     notify.EventNeedsInput,
		Title:     "Action Required",
		Body:      "Please solve the CAPTCHA.",
		URL:       "https://getafjob.example.org/applications/123",
		Priority:  notify.PriorityUrgent,
		Tags:      []string{"warning", "robot"},
		CreatedAt: time.Now(),
	}

	err := client.SendNtfy(ctx, server.URL, "my-job-alerts", "secret-token", n)
	if err != nil {
		t.Fatalf("SendNtfy error: %v", err)
	}

	if receivedMethod != http.MethodPost {
		t.Errorf("method = %s, want POST", receivedMethod)
	}
	if receivedPath != "/my-job-alerts" {
		t.Errorf("path = %s, want /my-job-alerts", receivedPath)
	}
	if receivedTitle != "Action Required" {
		t.Errorf("title = %s, want Action Required", receivedTitle)
	}
	if receivedPrio != "5" {
		t.Errorf("priority = %s, want 5", receivedPrio)
	}
	if receivedClick != "https://getafjob.example.org/applications/123" {
		t.Errorf("click = %s", receivedClick)
	}
	if receivedTags != "warning,robot" {
		t.Errorf("tags = %s, want warning,robot", receivedTags)
	}
	if receivedAuth != "Bearer secret-token" {
		t.Errorf("auth = %s, want Bearer secret-token", receivedAuth)
	}
	if receivedBody != "Please solve the CAPTCHA." {
		t.Errorf("body = %s", receivedBody)
	}
}
