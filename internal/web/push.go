package web

// Push notifications (#31): subscription endpoints and service worker serving.

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/1xxn/getafjob/internal/identity"
	"github.com/1xxn/getafjob/internal/platform/webpush"
)

type subscribeRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

type unsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

func (s *Server) vapidPublicKey(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"publicKey": s.VAPIDPublicKey,
	})
}

func (s *Server) pushSubscribe(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.PushStore == nil {
		http.Error(w, "push not configured", http.StatusServiceUnavailable)
		return
	}
	var req subscribeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return
	}
	if req.Endpoint == "" || len(req.Endpoint) > 2000 || req.Keys.P256dh == "" || len(req.Keys.P256dh) > 200 ||
		req.Keys.Auth == "" || len(req.Keys.Auth) > 100 {
		http.Error(w, "invalid subscription keys or endpoint", http.StatusBadRequest)
		return
	}
	sub := webpush.Subscription{
		Endpoint: req.Endpoint,
		P256dh:   req.Keys.P256dh,
		Auth:     req.Keys.Auth,
	}
	if err := s.PushStore.Upsert(r.Context(), u.ID, sub); err != nil {
		s.Logger.ErrorContext(r.Context(), "save push subscription", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) pushUnsubscribe(w http.ResponseWriter, r *http.Request, u *identity.User) {
	if u == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.PushStore == nil {
		http.Error(w, "push not configured", http.StatusServiceUnavailable)
		return
	}
	var req unsubscribeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8192)).Decode(&req); err != nil {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return
	}
	if req.Endpoint == "" || len(req.Endpoint) > 2000 {
		http.Error(w, "invalid endpoint", http.StatusBadRequest)
		return
	}
	if err := s.PushStore.Delete(r.Context(), u.ID, req.Endpoint); err != nil {
		s.Logger.ErrorContext(r.Context(), "delete push subscription", "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) serviceWorker(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Service-Worker-Allowed", "/")
	f, err := staticFiles.Open("static/sw.js")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func() { _ = f.Close() }()
	_, _ = io.Copy(w, f)
}
