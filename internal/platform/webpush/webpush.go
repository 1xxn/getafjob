package webpush

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// ErrSubscriptionExpired reports a push service answer that indicates the subscription is gone (HTTP 404 or 410).
var ErrSubscriptionExpired = errors.New("web push subscription expired or removed")

// Subscription holds a browser push subscription.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// Client sends encrypted Web Push notifications to push services.
type Client struct {
	HTTPClient *http.Client
	VAPIDKey   *ecdsa.PrivateKey
	Subject    string
}

// Send encrypts payload and posts it to the subscription's endpoint.
func (c *Client) Send(ctx context.Context, sub Subscription, payload []byte) error {
	if c.VAPIDKey == nil {
		return errors.New("web push: VAPID key is not configured")
	}
	body, err := Encrypt(payload, sub.P256dh, sub.Auth)
	if err != nil {
		return fmt.Errorf("web push encrypt: %w", err)
	}

	auth, err := VAPIDAuthorization(c.VAPIDKey, sub.Endpoint, c.Subject, time.Now())
	if err != nil {
		return fmt.Errorf("web push vapid: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Length", strconv.Itoa(len(body)))
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", "high")
	req.Header.Set("Authorization", auth)

	client := c.HTTPClient
	if client == nil {
		client = http.DefaultClient
	}

	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("web push post: %w", err)
	}
	defer func() {
		_ = res.Body.Close()
	}()

	switch res.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted:
		return nil
	case http.StatusNotFound, http.StatusGone:
		return ErrSubscriptionExpired
	default:
		b, _ := io.ReadAll(io.LimitReader(res.Body, 1024))
		return fmt.Errorf("web push service answered status %d: %s", res.StatusCode, string(b))
	}
}
