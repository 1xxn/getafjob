package app

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/1xxn/getafjob/internal/platform/config"
	"github.com/1xxn/getafjob/internal/platform/db"
	"github.com/1xxn/getafjob/internal/web"
)

// systemFacts reads what the admin system page shows: the database and the configured channels.
type systemFacts struct {
	pool *pgxpool.Pool
	cfg  config.Config
}

func (f systemFacts) Facts(ctx context.Context) (web.SystemFacts, error) {
	out := web.SystemFacts{EgressProxy: f.cfg.EgressProxy != nil, Mail: f.cfg.SMTPURL != "", PublicURL: f.cfg.PublicURL.String()}
	err := db.InSystemTx(ctx, f.pool, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "SELECT current_setting('server_version')").Scan(&out.Postgres); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "SELECT coalesce(max(version_id), 0) FROM goose_db_version WHERE is_applied").Scan(&out.Migration)
	})
	return out, err
}
