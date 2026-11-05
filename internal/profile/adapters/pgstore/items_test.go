package pgstore_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/platform/db/dbtest"
	"github.com/1xxn/getafjob/internal/profile"
	"github.com/1xxn/getafjob/internal/profile/adapters/pgstore"
)

func TestItems(t *testing.T) {
	pool := dbtest.AppPool(t)
	ctx := t.Context()
	items := profile.NewItems(pgstore.New(pool), sealer(t, pool))
	max, erika := user(t, pool, "max.mustermann@example.org"), user(t, pool, "erika.mustermann@example.org")

	id, err := items.SaveAnswer(ctx, max, profile.Answer{Question: "Do you have a disability? *", Answer: "I prefer not to say"})
	if err != nil {
		t.Fatal(err)
	}
	// The same question again updates the entry instead of adding one.
	if id2, err := items.SaveAnswer(ctx, max, profile.Answer{Question: "do you have a disability", Answer: "No"}); err != nil || id2 != id {
		t.Fatalf("second save = %v, %v", id2, err)
	}
	answers, err := items.Answers(ctx, max)
	if err != nil || len(answers) != 1 || answers[0].Answer != "No" {
		t.Fatalf("answers = %+v, %v", answers, err)
	}
	var ve *profile.ValidationError
	if _, err := items.SaveAnswer(ctx, max, profile.Answer{Question: "?", Answer: ""}); !errors.As(err, &ve) {
		t.Fatalf("empty answer: %v", err)
	}

	tid, err := items.SaveTextBlock(ctx, max, profile.TextBlock{Name: "Intro", Language: "en", Body: "Dear {company} team"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := items.SaveTextBlock(ctx, max, profile.TextBlock{ID: tid, Name: "Intro", Body: "Hello {employer}"}); !errors.As(err, &ve) {
		t.Fatalf("typo in placeholder: %v", err)
	}
	if blocks, err := items.TextBlocks(ctx, max); err != nil || len(blocks) != 1 || blocks[0].Body != "Dear {company} team" {
		t.Fatalf("text blocks = %+v, %v", blocks, err)
	}

	// Sealed at rest, invisible and untouchable for another user.
	var raw []byte
	if err := db.InSystemTx(ctx, pool, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, "SELECT sealed FROM profile_items WHERE id = $1", id).Scan(&raw)
	}); err != nil || bytes.Contains(raw, []byte("disability")) {
		t.Fatalf("stored plaintext or %v", err)
	}
	if a, err := items.Answers(ctx, erika); err != nil || len(a) != 0 {
		t.Fatalf("erika sees %+v, %v", a, err)
	}
	if _, err := items.SaveAnswer(ctx, erika, profile.Answer{ID: id, Question: "x", Answer: "y"}); !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("erika updates: %v", err)
	}
	if err := items.Delete(ctx, erika, profile.ItemAnswer, id); !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("erika deletes: %v", err)
	}
	if err := items.Delete(ctx, max, profile.ItemText, id); !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("wrong kind deletes: %v", err)
	}
	if err := items.Delete(ctx, max, profile.ItemAnswer, id); err != nil {
		t.Fatal(err)
	}
	if err := items.Delete(ctx, max, profile.ItemAnswer, uuid.New()); !errors.Is(err, profile.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
}
