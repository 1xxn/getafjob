// Package maintenance stores the instance-wide maintenance switch (#518). While it is on, visitors see a
// maintenance page, only admins use the app, and no background work starts.
package maintenance

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/db"
)

// MaxMessage is the longest note an admin can show on the maintenance page, in characters.
const MaxMessage = 200

// cacheFor bounds how long a process trusts the last read: a switch reaches every role within this time.
const cacheFor = 2 * time.Second

// State is the maintenance switch.
type State struct {
	On      bool
	Message string    // optional note for visitors, e.g. when it ends
	Since   time.Time // zero while off
}

// Switch reads and sets the maintenance state; reads are cached for a short time.
type Switch struct {
	pool *pgxpool.Pool
	now  func() time.Time

	mu     sync.Mutex
	cached State
	read   time.Time
}

// New returns a switch on the database of pool.
func New(pool *pgxpool.Pool) *Switch {
	return &Switch{pool: pool, now: time.Now}
}

// State returns the current state. When the database cannot be read it returns the last known state and the
// error; a process that never read it treats the instance as not in maintenance.
func (s *Switch) State(ctx context.Context) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.read.IsZero() && s.now().Sub(s.read) < cacheFor {
		return s.cached, nil
	}
	var st State
	err := db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, "SELECT message, since FROM maintenance").Scan(&st.Message, &st.Since)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		st.On = err == nil
		return err
	})
	if err != nil {
		return s.cached, err
	}
	s.cached, s.read = st, s.now()
	return st, nil
}

// On reports whether the instance is in maintenance; a read error keeps the last known state.
func (s *Switch) On(ctx context.Context) bool {
	st, _ := s.State(ctx)
	return st.On
}

// Set turns maintenance on with message or off. Turning it on again replaces the message and keeps the start.
func (s *Switch) Set(ctx context.Context, on bool, message string) error {
	message = CleanMessage(message)
	err := db.InSystemTx(ctx, s.pool, func(tx pgx.Tx) error {
		if !on {
			_, err := tx.Exec(ctx, "DELETE FROM maintenance")
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO maintenance (message) VALUES ($1)
			ON CONFLICT (id) DO UPDATE SET message = excluded.message`, message)
		return err
	})
	s.mu.Lock()
	s.read = time.Time{}
	s.mu.Unlock()
	return err
}

// CleanMessage trims a note to one line of at most MaxMessage characters without control characters.
func CleanMessage(m string) string {
	m = strings.Join(strings.FieldsFunc(m, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }), " ")
	if r := []rune(m); len(r) > MaxMessage {
		m = strings.TrimSpace(string(r[:MaxMessage]))
	}
	return m
}
