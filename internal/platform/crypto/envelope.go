package crypto

//go:generate go tool sqlc generate -f ../../../sqlc.yaml

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/crypto/q"
	"github.com/1xxn/getafjob/internal/platform/db"
)

// ErrNoKey reports that a user has no data key (never created, or forgotten: the data is gone).
var ErrNoKey = errors.New("user has no data key")

// rotateBatch bounds the keys re-wrapped per transaction.
const rotateBatch = 500

// Envelope seals and opens data per user. Data keys live in user_keys (row-level security), wrapped with the
// master key; they are created on first use.
type Envelope struct {
	ring *Keyring
	pool *pgxpool.Pool
	now  func() time.Time
}

// NewEnvelope returns an envelope over the keys in pool.
func NewEnvelope(ring *Keyring, pool *pgxpool.Pool) *Envelope {
	return &Envelope{ring: ring, pool: pool, now: time.Now}
}

// keyAAD binds a wrapped data key to its user.
func keyAAD(userID uuid.UUID) []byte { return []byte("getafjob/v1/key/" + userID.String()) }

// dataAAD binds a ciphertext to its user and purpose ("document:42", "mail_password").
func dataAAD(userID uuid.UUID, purpose string) []byte {
	return []byte("getafjob/v1/data/" + userID.String() + "/" + purpose)
}

// Seal encrypts plaintext for userID; purpose must be the same when opening.
func (e *Envelope) Seal(ctx context.Context, userID uuid.UUID, purpose string, plaintext []byte) ([]byte, error) {
	if purpose == "" {
		return nil, errors.New("seal without purpose")
	}
	dk, err := e.dataKey(ctx, userID, true)
	if err != nil {
		return nil, err
	}
	a, err := newAEAD(dk)
	if err != nil {
		return nil, err
	}
	return seal(a, plaintext, dataAAD(userID, purpose))
}

// Open decrypts what Seal returned for the same user and purpose.
func (e *Envelope) Open(ctx context.Context, userID uuid.UUID, purpose string, ciphertext []byte) ([]byte, error) {
	dk, err := e.dataKey(ctx, userID, false)
	if err != nil {
		return nil, err
	}
	a, err := newAEAD(dk)
	if err != nil {
		return nil, err
	}
	return open(a, ciphertext, dataAAD(userID, purpose))
}

// Forget deletes the user's data key; everything sealed for the user becomes unreadable.
func (e *Envelope) Forget(ctx context.Context, userID uuid.UUID) (bool, error) {
	var n int64
	err := db.InUserTx(ctx, e.pool, userID, func(tx pgx.Tx) error {
		var err error
		n, err = q.New(tx).DeleteDataKey(ctx, userID)
		return err
	})
	return n > 0, err
}

func (e *Envelope) dataKey(ctx context.Context, userID uuid.UUID, create bool) ([]byte, error) {
	var dk []byte
	err := db.InUserTx(ctx, e.pool, userID, func(tx pgx.Tx) error {
		qs := q.New(tx)
		row, err := qs.DataKey(ctx, userID)
		if errors.Is(err, pgx.ErrNoRows) && create {
			if err := e.createKey(ctx, qs, userID); err != nil {
				return err
			}
			row, err = qs.DataKey(ctx, userID)
		}
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoKey
		}
		if err != nil {
			return err
		}
		dk, err = e.ring.unwrap(row.MasterKeyID, row.Wrapped, keyAAD(userID))
		return err
	})
	return dk, err
}

func (e *Envelope) createKey(ctx context.Context, qs *q.Queries, userID uuid.UUID) error {
	dk := make([]byte, KeySize)
	if _, err := rand.Read(dk); err != nil {
		return err
	}
	id, wrapped, err := e.ring.wrap(dk, keyAAD(userID))
	if err != nil {
		return err
	}
	return qs.InsertDataKey(ctx, q.InsertDataKeyParams{UserID: userID, MasterKeyID: id, Wrapped: wrapped, CreatedAt: e.now()})
}

// Rotate re-wraps every data key that is not wrapped with the current master key (the old key must be in the
// keyring). It returns the number of keys re-wrapped; the data itself is not touched.
func (e *Envelope) Rotate(ctx context.Context) (int, error) {
	total := 0
	for {
		n := 0
		err := db.InSystemTx(ctx, e.pool, func(tx pgx.Tx) error {
			qs := q.New(tx)
			rows, err := qs.KeysToRewrap(ctx, q.KeysToRewrapParams{CurrentID: e.ring.CurrentID(), MaxRows: rotateBatch})
			if err != nil {
				return err
			}
			at := e.now()
			for _, r := range rows {
				aad := keyAAD(r.UserID)
				dk, err := e.ring.unwrap(r.MasterKeyID, r.Wrapped, aad)
				if err != nil {
					return err
				}
				id, wrapped, err := e.ring.wrap(dk, aad)
				if err != nil {
					return err
				}
				done, err := qs.RewrapDataKey(ctx, q.RewrapDataKeyParams{NewID: id, Wrapped: wrapped, At: &at,
					UserID: r.UserID, OldID: r.MasterKeyID})
				if err != nil {
					return err
				}
				n += int(done)
			}
			return nil
		})
		total += n
		if err != nil || n == 0 {
			return total, err
		}
	}
}
