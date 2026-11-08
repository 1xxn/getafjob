// Package ntfy publishes notifications to an ntfy.sh instance or self-hosted ntfy server.
package ntfy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/1xxn/getafjob/internal/notify"
)

// Client sends HTTP requests to an ntfy server.
type Client struct {
	httpClient *http.Client
}

// NewClient returns a new ntfy client with default timeout.
func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// NewClientWithHTTP returns a new ntfy client with a custom HTTP client (useful in tests).
func NewClientWithHTTP(client *http.Client) *Client {
	if client == nil {
		return NewClient()
	}
	return &Client{httpClient: client}
}

// SendNtfy publishes a notification to the specified ntfy topic.
func (c *Client) SendNtfy(ctx context.Context, serverURL, topic, token string, n notify.Notification) error {
	serverURL = strings.TrimSpace(serverURL)
	if serverURL == "" {
		serverURL = "https://ntfy.sh"
	}
	u, err := url.Parse(serverURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return fmt.Errorf("invalid ntfy server URL: %s", serverURL)
	}

	topic = strings.Trim(strings.TrimSpace(topic), "/")
	if topic == "" {
		return fmt.Errorf("ntfy topic is empty")
	}

	target := strings.TrimRight(serverURL, "/") + "/" + topic

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, strings.NewReader(n.Body))
	if err != nil {
		return fmt.Errorf("building ntfy request: %w", err)
	}

	req.Header.Set("Title", n.Title)
	req.Header.Set("Priority", strconv.Itoa(int(n.Priority)))
	if n.URL != "" {
		req.Header.Set("Click", n.URL)
	}
	if len(n.Tags) > 0 {
		req.Header.Set("Tags", strings.Join(n.Tags, ","))
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("sending ntfy request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("ntfy request failed (status %d): %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	return nil
}
