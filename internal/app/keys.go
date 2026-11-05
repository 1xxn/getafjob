package app

import (
	"context"
	"fmt"

	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/crypto"
	"github.com/1xxn/getafjob/internal/platform/db"
)

// adminRotate re-wraps every data key with the current master key (docs/runbooks/key-rotation.md).
func adminRotate(ctx context.Context, args []string, env Env) int {
	if len(args) != 0 {
		_, _ = fmt.Fprintln(env.Stderr, "usage: getafjob admin rotate-master-key")
		return ExitUsage
	}
	cfg, err := config.Load(env.Config)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "configuration error:\n%v\n", err)
		return ExitUsage
	}
	if cfg.MasterKey == "" {
		_, _ = fmt.Fprintln(env.Stderr, "GETAFJOB_MASTER_KEY is not set")
		return ExitUsage
	}
	ring, err := crypto.NewKeyring(cfg.MasterKey, cfg.MasterKeyOld)
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitUsage
	}
	pool, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabasePassword, "getafjob-admin")
	if err != nil {
		_, _ = fmt.Fprintln(env.Stderr, err)
		return ExitError
	}
	defer pool.Close()
	n, err := crypto.NewEnvelope(ring, pool).Rotate(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(env.Stderr, "rotation stopped after %d keys: %v\n", n, err)
		return ExitError
	}
	_, _ = fmt.Fprintf(env.Stdout, "%d data keys re-wrapped with master key %s\n", n, ring.CurrentID())
	return ExitOK
}
