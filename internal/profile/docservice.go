package profile

import (
	"context"
	"time"

	"github.com/google/uuid"
)

// Documents manages the user's files: type checked by content, contents and names sealed.
type Documents struct {
	store  DocumentStore
	files  Files
	sealer Sealer
	now    func() time.Time
}

// NewDocuments returns the document service.
func NewDocuments(store DocumentStore, files Files, sealer Sealer) *Documents {
	return &Documents{store: store, files: files, sealer: sealer, now: time.Now}
}

func fileKey(id uuid.UUID) string     { return "documents/" + id.String() }
func namePurpose(id uuid.UUID) string { return "document-name:" + id.String() }

// Upload checks and stores a file. The type comes from the content; the name is only shown back to the user.
func (s *Documents) Upload(ctx context.Context, userID uuid.UUID, kind, name string, data []byte) (Document, error) {
	if !ValidKind(kind) {
		return Document{}, ErrFileType
	}
	mime, err := Sniff(data)
	if err != nil {
		return Document{}, err
	}
	if n, err := s.store.CountDocuments(ctx, userID); err != nil {
		return Document{}, err
	} else if n >= MaxDocuments {
		return Document{}, ErrTooManyFiles
	}
	id, err := uuid.NewV7()
	if err != nil {
		return Document{}, err
	}
	d := Document{ID: id, Kind: kind, Name: CleanName(name, mime), Mime: mime, Size: len(data), CreatedAt: s.now()}
	sealed, err := s.sealer.Seal(ctx, userID, namePurpose(id), []byte(d.Name))
	if err != nil {
		return Document{}, err
	}
	if err := s.files.Put(ctx, userID, fileKey(id), data); err != nil {
		return Document{}, err
	}
	if err := s.store.InsertDocument(ctx, userID, StoredDocument{Document: d, SealedName: sealed}); err != nil {
		_ = s.files.Delete(context.WithoutCancel(ctx), userID, fileKey(id))
		return Document{}, err
	}
	return d, nil
}

func (s *Documents) open(ctx context.Context, userID uuid.UUID, sd StoredDocument) (Document, error) {
	name, err := s.sealer.Open(ctx, userID, namePurpose(sd.ID), sd.SealedName)
	if err != nil {
		return Document{}, err
	}
	d := sd.Document
	d.Name = string(name)
	return d, nil
}

// List returns the user's documents, oldest first.
func (s *Documents) List(ctx context.Context, userID uuid.UUID) ([]Document, error) {
	rows, err := s.store.Documents(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Document, 0, len(rows))
	for _, r := range rows {
		d, err := s.open(ctx, userID, r)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, nil
}

// Read returns a document with its content.
func (s *Documents) Read(ctx context.Context, userID, id uuid.UUID) (Document, []byte, error) {
	sd, err := s.store.Document(ctx, userID, id)
	if err != nil {
		return Document{}, nil, err
	}
	d, err := s.open(ctx, userID, sd)
	if err != nil {
		return Document{}, nil, err
	}
	data, err := s.files.Get(ctx, userID, fileKey(id))
	return d, data, err
}

// SetVariant sets label, language and focus words of a CV.
func (s *Documents) SetVariant(ctx context.Context, userID, id uuid.UUID, label, language string, focus []string) error {
	label, language, focus, err := CleanVariant(label, language, focus)
	if err != nil {
		return err
	}
	ok, err := s.store.UpdateVariant(ctx, userID, id, label, language, focus)
	if err == nil && !ok {
		return ErrNotFound
	}
	return err
}

// Delete removes a document and its file.
func (s *Documents) Delete(ctx context.Context, userID, id uuid.UUID) error {
	ok, err := s.store.DeleteDocument(ctx, userID, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return s.files.Delete(ctx, userID, fileKey(id))
}

// WithClock replaces the clock (tests).
func (s *Documents) WithClock(now func() time.Time) *Documents { s.now = now; return s }
