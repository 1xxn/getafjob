package pgstore

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/profile/adapters/pgstore/q"
)

var _ profile.DocumentStore = (*Store)(nil)

func toStored(id uuid.UUID, kind string, name []byte, mime string, size int32, label, lang string, focus []string, at time.Time) profile.StoredDocument {
	return profile.StoredDocument{SealedName: name, Document: profile.Document{ID: id, Kind: kind, Mime: mime, Size: int(size),
		Label: label, Language: lang, Focus: focus, CreatedAt: at}}
}

// InsertDocument stores document metadata.
func (s *Store) InsertDocument(ctx context.Context, userID uuid.UUID, d profile.StoredDocument) error {
	if d.Size <= 0 || d.Size > profile.MaxDocument {
		return profile.ErrFileTooLarge
	}
	focus := d.Focus
	if focus == nil {
		focus = []string{}
	}
	return s.user(ctx, userID, func(qs *q.Queries) error {
		return qs.InsertDocument(ctx, q.InsertDocumentParams{ID: d.ID, UserID: userID, Kind: d.Kind, SealedName: d.SealedName,
			Mime: d.Mime, Size: int32(d.Size), Label: d.Label, Language: d.Language, Focus: focus, CreatedAt: d.CreatedAt}) //nolint:gosec // checked above
	})
}

// Documents lists the user's documents.
func (s *Store) Documents(ctx context.Context, userID uuid.UUID) (out []profile.StoredDocument, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		rows, err := qs.ListDocuments(ctx, userID)
		for _, r := range rows {
			out = append(out, toStored(r.ID, r.Kind, r.SealedName, r.Mime, r.Size, r.Label, r.Language, r.Focus, r.CreatedAt))
		}
		return err
	})
	return out, err
}

// Document returns one document of the user.
func (s *Store) Document(ctx context.Context, userID, id uuid.UUID) (d profile.StoredDocument, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		r, err := qs.GetDocument(ctx, q.GetDocumentParams{ID: id, UserID: userID})
		if errors.Is(err, pgx.ErrNoRows) {
			return profile.ErrNotFound
		}
		if err != nil {
			return err
		}
		d = toStored(r.ID, r.Kind, r.SealedName, r.Mime, r.Size, r.Label, r.Language, r.Focus, r.CreatedAt)
		return nil
	})
	return d, err
}

// CountDocuments counts the user's documents.
func (s *Store) CountDocuments(ctx context.Context, userID uuid.UUID) (n int, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		c, err := qs.CountDocuments(ctx, userID)
		n = int(c)
		return err
	})
	return n, err
}

// UpdateVariant sets the variant fields of a CV.
func (s *Store) UpdateVariant(ctx context.Context, userID, id uuid.UUID, label, language string, focus []string) (ok bool, err error) {
	if focus == nil {
		focus = []string{}
	}
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.UpdateVariant(ctx, q.UpdateVariantParams{ID: id, UserID: userID, Label: label, Language: language, Focus: focus})
		ok = n == 1
		return err
	})
	return ok, err
}

// DeleteDocument removes document metadata.
func (s *Store) DeleteDocument(ctx context.Context, userID, id uuid.UUID) (ok bool, err error) {
	err = s.user(ctx, userID, func(qs *q.Queries) error {
		n, err := qs.DeleteDocument(ctx, q.DeleteDocumentParams{ID: id, UserID: userID})
		ok = n == 1
		return err
	})
	return ok, err
}
