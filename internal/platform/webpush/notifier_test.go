package webpush

import (
	"context"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/google/uuid"
)

type memStore struct {
	mu   sync.Mutex
	subs map[uuid.UUID][]Subscription
}

func newMemStore() *memStore {
	return &memStore{subs: make(map[uuid.UUID][]Subscription)}
}

func (m *memStore) Upsert(_ context.Context, userID uuid.UUID, sub Subscription) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subs[userID] = append(m.subs[userID], sub)
	return nil
}

func (m *memStore) Delete(_ context.Context, userID uuid.UUID, endpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var kept []Subscription
	for _, s := range m.subs[userID] {
		if s.Endpoint != endpoint {
			kept = append(kept, s)
		}
	}
	m.subs[userID] = kept
	return nil
}

func (m *memStore) DeleteEndpoint(_ context.Context, endpoint string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for u, list := range m.subs {
		var kept []Subscription
		for _, s := range list {
			if s.Endpoint != endpoint {
				kept = append(kept, s)
			}
		}
		m.subs[u] = kept
	}
	return nil
}

func (m *memStore) ByUser(_ context.Context, userID uuid.UUID) ([]Subscription, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]Subscription(nil), m.subs[userID]...), nil
}

func TestNotifier(t *testing.T) {
	var received int
	var mu sync.Mutex

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		received++
		mu.Unlock()

		if r.Header.Get("Content-Encoding") != "aes128gcm" {
			t.Errorf("expected Content-Encoding aes128gcm, got %s", r.Header.Get("Content-Encoding"))
		}
		if r.Header.Get("Authorization") == "" {
			t.Error("missing Authorization header")
		}
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Error("empty body")
		}

		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()

	recPriv, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	p256dh := base64.RawURLEncoding.EncodeToString(recPriv.PublicKey().Bytes())
	authSecret := base64.RawURLEncoding.EncodeToString([]byte("1234567890123456"))

	store := newMemStore()
	user := uuid.New()
	_ = store.Upsert(t.Context(), user, Subscription{Endpoint: srv.URL, P256dh: p256dh, Auth: authSecret})

	vapidPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}

	client := &Client{HTTPClient: srv.Client(), VAPIDKey: vapidPriv, Subject: "mailto:test@example.com"}
	notifier := &Notifier{Store: store, Client: client, Logger: slog.Default()}

	err = notifier.Notify(t.Context(), user, NoticePayload{Title: "getafjob", Body: "Your turn", URL: "/applications/1"})
	if err != nil {
		t.Fatalf("Notify: %v", err)
	}

	mu.Lock()
	count := received
	mu.Unlock()
	if count != 1 {
		t.Fatalf("expected 1 push message, got %d", count)
	}
}

func TestNotifierExpiredSubscription(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusGone) // 410 Gone
	}))
	defer srv.Close()

	recPriv, _ := ecdh.P256().GenerateKey(rand.Reader)
	p256dh := base64.RawURLEncoding.EncodeToString(recPriv.PublicKey().Bytes())
	authSecret := base64.RawURLEncoding.EncodeToString([]byte("1234567890123456"))

	store := newMemStore()
	user := uuid.New()
	_ = store.Upsert(t.Context(), user, Subscription{Endpoint: srv.URL, P256dh: p256dh, Auth: authSecret})

	vapidPriv, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	client := &Client{HTTPClient: srv.Client(), VAPIDKey: vapidPriv, Subject: "mailto:test@example.com"}
	notifier := &Notifier{Store: store, Client: client}

	err := notifier.Notify(t.Context(), user, NoticePayload{Title: "getafjob", Body: "Your turn", URL: "/applications/1"})
	if err != nil {
		t.Fatalf("Notify should not fail on expired: %v", err)
	}

	subs, _ := store.ByUser(t.Context(), user)
	if len(subs) != 0 {
		t.Fatalf("expected expired subscription to be deleted, got %d", len(subs))
	}
}
