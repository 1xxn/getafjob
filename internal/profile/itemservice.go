package profile

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Items manages the answer bank and the text blocks; each entry is sealed on its own.
type Items struct {
	store  ItemStore
	sealer Sealer
	now    func() time.Time
}

// NewItems returns the answer bank and text block service.
func NewItems(store ItemStore, sealer Sealer) *Items {
	return &Items{store: store, sealer: sealer, now: time.Now}
}

func itemPurpose(kind string, id uuid.UUID) string { return "item:" + kind + ":" + id.String() }

func (s *Items) load(ctx context.Context, userID uuid.UUID, kind string, each func(id uuid.UUID, plain []byte) error) error {
	rows, err := s.store.Items(ctx, userID, kind)
	if err != nil {
		return err
	}
	for _, r := range rows {
		plain, err := s.sealer.Open(ctx, userID, itemPurpose(kind, r.ID), r.Sealed)
		if err != nil {
			return err
		}
		if err := each(r.ID, plain); err != nil {
			return err
		}
	}
	return nil
}

// save inserts (id nil) or updates one item after validation.
func (s *Items) save(ctx context.Context, userID uuid.UUID, kind string, id uuid.UUID, v any, errs []FieldError) (uuid.UUID, error) {
	if len(errs) > 0 {
		return uuid.Nil, &ValidationError{Fields: errs}
	}
	insert := id == uuid.Nil
	if insert {
		n, err := s.store.CountItems(ctx, userID, kind)
		if err != nil {
			return uuid.Nil, err
		}
		if n >= MaxItems {
			return uuid.Nil, ErrTooManyItems
		}
		if id, err = uuid.NewV7(); err != nil {
			return uuid.Nil, err
		}
	}
	plain, err := json.Marshal(v)
	if err != nil {
		return uuid.Nil, err
	}
	sealed, err := s.sealer.Seal(ctx, userID, itemPurpose(kind, id), plain)
	if err != nil {
		return uuid.Nil, err
	}
	it := StoredItem{ID: id, Sealed: sealed, UpdatedAt: s.now()}
	if insert {
		return id, s.store.InsertItem(ctx, userID, kind, it)
	}
	ok, err := s.store.UpdateItem(ctx, userID, kind, it)
	if err == nil && !ok {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

// Answers returns the user's answer bank.
func (s *Items) Answers(ctx context.Context, userID uuid.UUID) ([]Answer, error) {
	var out []Answer
	err := s.load(ctx, userID, ItemAnswer, func(id uuid.UUID, plain []byte) error {
		var a Answer
		if err := json.Unmarshal(plain, &a); err != nil {
			return err
		}
		a.ID = id
		out = append(out, a)
		return nil
	})
	return out, err
}

// SaveAnswer stores a new (ID nil) or changed answer. A question that is already in the bank updates that entry.
func (s *Items) SaveAnswer(ctx context.Context, userID uuid.UUID, a Answer) (uuid.UUID, error) {
	a.Question, a.Answer = cleanLine(a.Question), trimText(a.Answer)
	if a.ID == uuid.Nil {
		if existing, err := s.Answers(ctx, userID); err != nil {
			return uuid.Nil, err
		} else if old, ok := FindAnswer(existing, a.Question); ok {
			a.ID = old.ID
		}
	}
	return s.save(ctx, userID, ItemAnswer, a.ID, a, a.Validate())
}

// TextBlocks returns the user's text blocks.
func (s *Items) TextBlocks(ctx context.Context, userID uuid.UUID) ([]TextBlock, error) {
	var out []TextBlock
	err := s.load(ctx, userID, ItemText, func(id uuid.UUID, plain []byte) error {
		var t TextBlock
		if err := json.Unmarshal(plain, &t); err != nil {
			return err
		}
		t.ID = id
		out = append(out, t)
		return nil
	})
	return out, err
}

// SaveTextBlock stores a new (ID nil) or changed text block.
func (s *Items) SaveTextBlock(ctx context.Context, userID uuid.UUID, t TextBlock) (uuid.UUID, error) {
	t.Name, t.Body = cleanLine(t.Name), trimText(t.Body)
	return s.save(ctx, userID, ItemText, t.ID, t, t.Validate())
}

// Delete removes an answer or text block.
func (s *Items) Delete(ctx context.Context, userID uuid.UUID, kind string, id uuid.UUID) error {
	ok, err := s.store.DeleteItem(ctx, userID, kind, id)
	if err == nil && !ok {
		return ErrNotFound
	}
	return err
}

// WithClock replaces the clock (tests).
func (s *Items) WithClock(now func() time.Time) *Items { s.now = now; return s }
