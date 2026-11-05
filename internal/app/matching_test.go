package app

import (
	"slices"
	"testing"

	"github.com/google/uuid"
)

func TestUnion(t *testing.T) {
	lists := [][]string{{"Go Developer", "SRE", "Platform Engineer"}, {"go developer", " Data Engineer "}, nil}
	if got := union(lists, 10); !slices.Equal(got, []string{"Go Developer", "SRE", "Data Engineer", "Platform Engineer"}) {
		t.Fatalf("union = %q", got)
	}
	if got := union(lists, 2); !slices.Equal(got, []string{"Go Developer", "SRE"}) {
		t.Fatalf("limited = %q", got)
	}
	if got := union(nil, 5); got != nil {
		t.Fatalf("empty = %q", got)
	}
}

func TestMatchArgsUniqueOnlyForAll(t *testing.T) {
	if !(MatchArgs{}).InsertOpts().UniqueOpts.ByArgs {
		t.Error("run over all users is not unique")
	}
	if (MatchArgs{UserID: uuid.New()}).InsertOpts().UniqueOpts.ByState != nil {
		t.Error("per-user runs are merged")
	}
}
