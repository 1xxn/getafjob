package app

import (
	"context"
	"fmt"
	"net/http"

	"github.com/1xxn/getafjob/internal/discovery"
	"github.com/1xxn/getafjob/internal/discovery/adapters/pgstore"
	"github.com/1xxn/getafjob/internal/discovery/adapters/sources"
	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db"
)

const boardsUsage = `usage: getafjob admin boards <import | list>
  import   add the built-in catalog of company boards (known boards keep their state)
  list     print the enabled boards
`

// adminBoards manages the company boards the crawler reads.
func adminBoards(ctx context.Context, args []string, env Env) int {
	if len(args) != 1 || (args[0] != "import" && args[0] != "list") {
		_, _ = fmt.Fprint(env.Stderr, boardsUsage)
		return ExitUsage
	}
	cfg, err := config.Load(env.Config)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "configuration error:\n%v\n", err)
		return ExitUsage
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-admin")
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	defer pool.Close()
	store := pgstore.New(pool)

	if args[0] == "list" {
		boards, err := store.EnabledBoards(ctx)
		if err != nil {
			_, _ = fmt.Fprintln(env.Stderr, "list failed:", err)
			return ExitError
		}
		for _, b := range boards {
			_, _ = fmt.Fprintf(env.Stdout, "%-16s %-28s %s\n", b.ATS, b.Slug, b.Company)
		}
		return ExitOK
	}

	catalog, err := sources.Catalog()
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	check := sources.NewClient(http.DefaultClient) // only for validation, nothing is fetched
	boards := make([]discovery.Board, 0, len(catalog))
	for _, b := range catalog {
		if _, err := sources.FromBoard(check, b); err != nil {
			_, _ = fmt.Fprintln(env.Stderr, err)
			return ExitError
		}
		boards = append(boards, discovery.Board{ATS: b.ATS, Slug: b.Slug, Company: b.Company, Config: b.Config})
	}
	added, updated, err := store.ImportBoards(ctx, boards)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, "import failed:", err)
		return ExitError
	}
	_, _ = fmt.Fprintf(env.Stdout, "%d boards added, %d updated\n", added, updated)
	return ExitOK
}
